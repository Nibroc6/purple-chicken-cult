package auth_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// onceAt runs fn the first time the flow reaches point.
func (e *env) onceAt(point string, fn func()) {
	var once sync.Once
	e.a.Auth.SetTestHook(func(p string) {
		if p == point {
			once.Do(fn)
		}
	})
	e.t.Cleanup(func() { e.a.Auth.SetTestHook(nil) })
}

// waitLockWaiter waits until some backend of this database waits on a lock.
func (e *env) waitLockWaiter() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.count(`SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			WHERE NOT l.granted AND a.datname = current_database()`) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("no backend ever waited on a lock")
}

// twoKeys enrolls alice with a, adds a2, and returns a fresh session on a2.
func (e *env) twoKeys() (a, a2 *authtest.Authenticator, s2 *client, alice core.UserID) {
	e.t.Helper()
	a, a2 = e.authn(), e.authn()
	c, alice := e.enroll("alice", a)
	e.advance(time.Second)
	if err := e.addPasskey(c, a2, "second"); err != nil {
		e.t.Fatal(err)
	}
	s2, err := e.login(a2)
	if err != nil {
		e.t.Fatal(err)
	}
	return a, a2, s2, alice
}

func (e *env) refOf(c *client, current bool) gate.Ref {
	e.t.Helper()
	list, err := e.a.Auth.Passkeys(c.ctx())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, p := range list {
		if p.Current == current {
			return p.ID
		}
	}
	e.t.Fatalf("no passkey with current=%v", current)
	return ""
}

// A passkey removed after the login assertion verified but before the session
// is issued must not leave a session behind (the stolen-device case).
func TestLoginRefusedWhenCredentialRemovedMidway(t *testing.T) {
	e := newEnv(t)
	a, _, s2, _ := e.twoKeys()
	aRef := e.refOf(s2, false)
	credA := a.Credentials()[0].ID
	h := e.hookSession("alice") // a credential-less fresh session removes (no nested ceremony)
	e.onceAt("before-use", func() {
		if err := e.a.Auth.RemovePasskey(h.ctx(), aRef, "", nil); err != nil {
			t.Errorf("remove: %v", err)
		}
	})
	if _, err := e.login(a); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("login with a passkey removed midway: %v", err)
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE credential_id = $1", credA); n != 0 {
		t.Fatalf("%d sessions of the removed passkey", n)
	}
}

// A removal that starts while the login holds the credential waits for it and
// then ends the session the login created.
func TestRemovalWaitsForLoginAndEndsItsSession(t *testing.T) {
	e := newEnv(t)
	a, _, s2, _ := e.twoKeys()
	aRef := e.refOf(s2, false)
	credA := a.Credentials()[0].ID
	done := make(chan error, 1)
	h := e.hookSession("alice")
	e.onceAt("after-use", func() {
		go func() { done <- e.a.Auth.RemovePasskey(h.ctx(), aRef, "", nil) }()
		e.waitLockWaiter()
	})
	c, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := c.viewer(); ok {
		t.Fatal("session of a removed passkey survived")
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE credential_id = $1", credA); n != 0 {
		t.Fatalf("%d sessions of the removed passkey", n)
	}
}

// A step-up with a passkey removed midway is rejected and does not refresh auth_at.
func TestStepUpRefusedWhenCredentialRemovedMidway(t *testing.T) {
	e := newEnv(t)
	a, a2, s2, _ := e.twoKeys()
	s1, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	a2Ref := e.refOf(s2, true)
	v, _ := s1.viewer()
	loginAt := v.AuthAt
	e.advance(time.Minute)
	h := e.hookSession("alice")
	e.onceAt("before-use", func() {
		if err := e.a.Auth.RemovePasskey(h.ctx(), a2Ref, "", nil); err != nil {
			t.Errorf("remove: %v", err)
		}
	})
	wantProblem(t, e.stepUp(s1, a2), "passkey_rejected")
	if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE id = $1", a2.Credentials()[0].ID); n != 0 {
		t.Fatal("passkey not removed")
	}
	if v, ok := s1.viewer(); !ok || !v.AuthAt.Equal(loginAt) {
		t.Fatalf("auth_at refreshed by a removed passkey: %+v", v)
	}
}

// Recovery with remove_old: a login racing with an old passkey (holding its
// credential lock) must not keep a session after recovery commits.
func TestRecoveryEndsSessionOfRacingLogin(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	_, alice := e.enroll("alice", a)
	var tok string
	if err := e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		var err error
		tok, err = e.d.Passkeys.IssueEnrollment(e.ctx, tx, alice, 1, time.Time{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r := &client{e: e}
	ev, err := e.a.Auth.EnrollBegin(r.ctx(), tok)
	if err != nil {
		t.Fatal(err)
	}
	r.cookie = ev.Token
	fresh := e.authn()
	resp, err := fresh.Create(ev.Options)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		tok string
		err error
	}
	done := make(chan result, 1)
	rctx := r.ctx()
	e.onceAt("after-use", func() {
		go func() {
			tk, _, err := e.a.Auth.EnrollFinish(rctx, resp, 0, false)
			done <- result{tk, err}
		}()
		e.waitLockWaiter()
	})
	c, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if _, ok := c.viewer(); ok {
		t.Fatal("a login with an old passkey survived recovery")
	}
	if v, ok := (&client{e: e, cookie: res.tok}).viewer(); !ok || v.ID != alice {
		t.Fatal("recovered session not live")
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE user_id = $1", int64(alice)); n != 1 {
		t.Fatalf("%d sessions after recovery", n)
	}
}

// RevokeAll publishes SessionEnded only once the caller's tx commits, and
// never when it rolls back.
func TestRevokeAllPublishesAfterCommit(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	before := e.endedFor(alice)
	boom := errors.New("caller fails after RevokeAll")
	err := e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		if err := e.d.Passkeys.RevokeAll(e.ctx, tx, alice); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	e.a.Auth.WaitRevoked()
	if e.endedFor(alice) != before {
		t.Fatal("SessionEnded published for a rolled-back revocation")
	}
	if _, ok := c.viewer(); !ok {
		t.Fatal("session ended by a rolled-back revocation")
	}

	err = e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		if err := e.d.Passkeys.RevokeAll(e.ctx, tx, alice); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond) // a publisher racing ahead of commit would fire now
		if e.endedFor(alice) != before {
			t.Error("SessionEnded published before commit")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.a.Auth.WaitRevoked()
	if e.endedFor(alice)-before != 1 {
		t.Fatalf("SessionEnded events after commit: %d", e.endedFor(alice)-before)
	}
	if _, ok := c.viewer(); ok {
		t.Fatal("revoked session alive")
	}
}
