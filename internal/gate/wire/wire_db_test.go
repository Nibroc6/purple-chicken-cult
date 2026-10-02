package wire_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

// The wired App runs end-to-end over a real database: users (complete),
// inviter labels, genesis enrollment, the DB-loaded graph and coreref-backed
// visibility.
func TestWiredAppOverDatabase(t *testing.T) {
	a := wiretest.NewApp(t)
	ctx := context.Background()
	d := a.Deps()

	alice, err := a.Users.Create(ctx, d.DB.Q(), core.NewUser{Handle: "Alice", Genesis: true})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := a.Users.Create(ctx, d.DB.Q(), core.NewUser{Handle: "bob", InvitedBy: alice})
	if err != nil {
		t.Fatal(err)
	}
	var p *gate.Problem
	if _, err := a.Users.Create(ctx, d.DB.Q(), core.NewUser{Handle: "ALICE", Genesis: true}); !errors.As(err, &p) || p.Code != "handle_taken" {
		t.Fatalf("duplicate handle: %v", err)
	}
	if _, err := a.Users.Create(ctx, d.DB.Q(), core.NewUser{Handle: "admin", Genesis: true}); !errors.As(err, &p) || p.Code != "handle_invalid" {
		t.Fatalf("reserved handle: %v", err)
	}
	if l, _ := a.Users.InviterLabel(ctx, bob); l.Kind != "user" || l.Handle != "Alice" {
		t.Fatalf("bob's inviter label %+v", l)
	}
	if by, ok := a.Users.InviterOf(bob); !ok || by != alice {
		t.Fatalf("InviterOf(bob) = %d,%v", by, ok)
	}
	if id, ok := a.Users.ByHandle(ctx, "alice"); !ok || id != alice {
		t.Fatalf("ByHandle(alice) = %d,%v", id, ok)
	}

	u, err := a.Auth.SeedGenesis(ctx, "carol", "Carol")
	if err != nil || len(u) < 20 {
		t.Fatalf("genesis: %q %v", u, err)
	}

	w := wiretest.World(t, a)
	w.User("dave")
	w.User("erin")
	w.Friends("dave", "erin")
	if err := a.Graph.Load(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := a.Visibility.SearchFor(ctx, w.ID("dave"))
	if err != nil {
		t.Fatal(err)
	}
	if dist, ok := s.Dist(w.ID("erin")); !ok || dist != 1 {
		t.Fatalf("dist(dave, erin) after reload = %d,%v", dist, ok)
	}
	if _, ok := s.Dist(alice); ok {
		t.Fatal("alice is in another component")
	}
	pv, err := a.Views.Person(ctx, s, w.ID("erin"))
	if err != nil || pv.Relation != "friend" || pv.Inviter.Kind != "genesis" {
		t.Fatalf("person view %+v %v", pv, err)
	}
	if _, err := a.Views.Person(ctx, s, alice); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("other component person: %v", err)
	}

	// Signed records verify against the published key.
	h, err := a.Records.Sign(ctx, d.DB.Q(), core.RecFriendship, 0, map[string]any{"a": "x", "b": "y", "level": "friend"}, []core.UserID{alice, bob})
	if err != nil || len(h) != 32 {
		t.Fatalf("sign: %v", err)
	}
	keys, err := a.Records.Keys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Fingerprint == "" {
		t.Fatalf("keys: %+v %v", keys, err)
	}
	_ = coretest.Epoch
}
