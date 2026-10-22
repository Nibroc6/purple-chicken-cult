package cosign_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/cosign"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

type inviteBody struct {
	Inviter           uuid.UUID  `json:"inviter"`
	Invitee           uuid.UUID  `json:"invitee"`
	IssuedAt          sigs.Time  `json:"issued_at"`
	InviteeCredential sigs.Bytes `json:"invitee_credential"`
}

// joiner is an invitee's browser during the signup ceremony.
type joiner struct {
	*client
	sess [32]byte
	ext  uuid.UUID
	cred core.Credential
	recs []core.CosignRecord
}

// join registers a new credential in a guest session and drafts the Invite
// and Friendship records with inviter (what the invites module does at
// register/finish), returning the ceremony's get options.
func (e *env) join(inviter *client, handle string) (*joiner, core.Intent) {
	e.t.Helper()
	tok, err := e.a.Auth.StartGuest(e.ctx)
	e.must(err)
	j := &joiner{client: &client{e: e, cookie: tok, auth: authtest.New(e.d.Cfg.Origin)}, sess: sha256.Sum256([]byte(tok)),
		ext: uuid.Must(uuid.NewV7())}
	webID := bytes.Repeat([]byte{7}, 32)
	opts, err := e.d.Passkeys.BeginRegistration(e.ctx, j.sess, core.PendingUser{WebAuthnID: webID, Handle: handle,
		DisplayName: handle})
	e.must(err)
	resp, err := j.auth.Create(opts)
	e.must(err)
	j.cred, err = e.d.Passkeys.FinishRegistration(e.ctx, j.sess, resp)
	e.must(err)
	now := e.d.Clock.Now()
	a, b := e.ext(inviter.id), j.ext
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	j.recs = []core.CosignRecord{
		{Kind: core.RecInvite, Body: inviteBody{Inviter: e.ext(inviter.id), Invitee: j.ext, IssuedAt: sigs.T(now),
			InviteeCredential: j.cred.ID}},
		{Kind: core.RecFriendship, Body: friendship{A: a, B: b, Level: "friend", CreatedAt: sigs.T(now)}},
	}
	in, err := e.s.BeginSignup(e.ctx, j.sess, j.cred, j.recs)
	e.must(err)
	return j, in
}

// redeem is the RegisterFinish transaction with the ceremony: create the user
// with the reserved ext_id, bind the credential, verify the assertion, sign
// both records with it, create the edge. Any error aborts everything.
func (e *env) redeem(inviter *client, j *joiner, handle string, recs []core.CosignRecord, assertion []byte) (core.UserID, error) {
	e.t.Helper()
	var uid core.UserID
	err := e.d.DB.TxSerializable(e.ctx, func(tx pgx.Tx) error {
		var err error
		uid, err = e.d.Users.Create(e.ctx, tx, core.NewUser{Handle: handle, DisplayName: handle, InvitedBy: inviter.id,
			ExtID: j.ext, Status: core.StatusActive, TOSVersion: e.d.Cfg.TOSVersion, AgeAttested: true})
		if err != nil {
			return err
		}
		if err := e.d.Passkeys.BindCredential(e.ctx, tx, uid, j.cred); err != nil {
			return err
		}
		as, err := e.d.Signup.FinishSignup(e.ctx, tx, j.sess, uid, j.cred, recs, assertion)
		if err != nil {
			return err
		}
		parties := []core.UserID{inviter.id, uid}
		hashes := make([][]byte, len(recs))
		for i, r := range recs {
			if hashes[i], err = e.d.Signer.Sign(e.ctx, tx, r.Kind, inviter.id, r.Body, parties, as[i]); err != nil {
				return err
			}
		}
		return e.d.Edges.Create(e.ctx, tx, inviter.id, uid, core.LevelFriend, hashes[1])
	})
	return uid, err
}

func (e *env) nothingCreated(handle string) {
	e.t.Helper()
	if _, ok := e.a.Users.ByHandle(e.ctx, handle); ok {
		e.t.Fatal("user created")
	}
	for sql, want := range map[string]int{
		"SELECT count(*) FROM users WHERE handle_canon = $1": 0,
	} {
		if n := e.count(sql, handle); n != want {
			e.t.Fatalf("%s = %d", sql, n)
		}
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind IN (1, 2)"); n != 0 {
		e.t.Fatalf("records written: %d", n)
	}
	if n := e.count("SELECT count(*) FROM edges"); n != 0 {
		e.t.Fatalf("edges written: %d", n)
	}
}

// Fidelity #21: the invitee co-signs the Invite and Friendship drafts with the
// new credential before RegisterFinish commits; the records carry the
// assertion, the account has the reserved ext_id and the export verifies with
// the co-sign cut-over on (the Invite names the first credential).
func TestSignupCeremony(t *testing.T) {
	e := newEnv(t)
	e.must(e.a.Records.EnforceCosign(e.ctx, e.d.Clock.Now()))
	bob := e.enroll("bob")
	j, in := e.join(bob, "newbie")
	var drafts []json.RawMessage
	e.must(json.Unmarshal(in.Record, &drafts))
	if len(drafts) != 2 || in.ID != "" || !bytes.Contains(in.Options, []byte(`"userVerification":"required"`)) {
		t.Fatalf("intent %s %s", in.Record, in.Options)
	}
	asr, err := j.auth.Get(in.Options)
	e.must(err)
	uid, err := e.redeem(bob, j, "newbie", j.recs, asr)
	e.must(err)
	if e.ext(uid) != j.ext {
		t.Fatal("reserved ext_id not used")
	}
	if n := e.count(`SELECT count(*) FROM signed_records WHERE kind IN (1, 2) AND assertions IS NOT NULL
		AND key_id LIKE 'usr-%' AND cosigs IS NOT NULL`); n != 2 {
		t.Fatalf("co-signed records: %d", n)
	}
	// The ceremony is single use.
	_, err = e.d.Signup.FinishSignup(e.ctx, e.d.DB.Q(), j.sess, uid, j.cred, j.recs, asr)
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("replayed ceremony: %v", err)
	}
	for _, who := range []core.UserID{uid, bob.id} {
		rep := e.verify(e.export(who))
		if len(rep.Failures) != 0 || rep.Valid != rep.Records || rep.Records == 0 {
			t.Fatalf("export of %d: %+v", who, rep)
		}
	}
}

// A failed get assertion aborts RegisterFinish's transaction: no account,
// credential, record or edge is created.
func TestSignupCeremonyFailureAbortsEverything(t *testing.T) {
	cases := map[string]func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte){
		"origin mismatch": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			j.auth.OriginOverride = "https://evil.example"
			return j.recs, mustGet(e, j.auth, in.Options)
		},
		"crossOrigin": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			j.auth.CrossOrigin = true
			return j.recs, mustGet(e, j.auth, in.Options)
		},
		"topOrigin": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			j.auth.TopOrigin = e.d.Cfg.Origin
			return j.recs, mustGet(e, j.auth, in.Options)
		},
		"no UV": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			j.auth.NoUV = true
			return j.recs, mustGet(e, j.auth, in.Options)
		},
		"BE flag changed": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			j.auth.BackupEligible = true
			return j.recs, mustGet(e, j.auth, in.Options)
		},
		"another credential": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			other := authtest.New(e.d.Cfg.Origin)
			_, err := other.Create([]byte(`{"publicKey":{"rp":{"id":"` + e.d.Cfg.RPID + `"},"user":{"id":"AQ"},"challenge":"AQ"}}`))
			e.must(err)
			c := other.Credentials()[0]
			j.auth.Import(c)
			asr, err := j.auth.GetWith(in.Options, c.ID)
			e.must(err)
			return j.recs, asr
		},
		"other records": func(e *env, j *joiner, in core.Intent) ([]core.CosignRecord, []byte) {
			recs := append([]core.CosignRecord(nil), j.recs...)
			f := recs[1].Body.(friendship)
			f.Level = "friend+"
			recs[1] = core.CosignRecord{Kind: core.RecFriendship, Body: f}
			return recs, mustGet(e, j.auth, in.Options)
		},
		"garbage": func(*env, *joiner, core.Intent) ([]core.CosignRecord, []byte) {
			return nil, []byte(`{"id":"x"}`)
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			bob := e.enroll("bob")
			j, in := e.join(bob, "newbie")
			recs, asr := mk(e, j, in)
			if recs == nil {
				recs = j.recs
			}
			_, err := e.redeem(bob, j, "newbie", recs, asr)
			if !errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("redeem: %v", err)
			}
			e.nothingCreated("newbie")
			if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE id = $1", j.cred.ID); n != 0 {
				t.Fatalf("credential bound: %d", n)
			}
		})
	}
	t.Run("expired", func(t *testing.T) {
		e := newEnv(t)
		bob := e.enroll("bob")
		j, in := e.join(bob, "newbie")
		asr := mustGet(e, j.auth, in.Options)
		e.advance(cosign.TTL + time.Second)
		if _, err := e.redeem(bob, j, "newbie", j.recs, asr); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("expired: %v", err)
		}
		e.nothingCreated("newbie")
	})
	t.Run("begin needs a guest session", func(t *testing.T) {
		e := newEnv(t)
		bob := e.enroll("bob")
		j, _ := e.join(bob, "newbie")
		if _, err := e.s.BeginSignup(e.ctx, [32]byte{1}, j.cred, j.recs); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("unknown session: %v", err)
		}
		if _, err := e.s.BeginSignup(e.ctx, j.sess, j.cred, nil); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("no records: %v", err)
		}
	})
}

func mustGet(e *env, a *authtest.Authenticator, opts []byte) []byte {
	e.t.Helper()
	asr, err := a.Get(opts)
	e.must(err)
	return asr
}
