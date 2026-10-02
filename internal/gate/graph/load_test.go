package graph

import (
	"context"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

// Load reads edges, blocks and hide_via, and agrees with the facts written.
func TestLoadFromDatabase(t *testing.T) {
	db := storetest.NewDB(t)
	m := coretest.NewMem(t)
	w := coretest.NewWorld(t, m.D, db)
	for _, h := range []string{"ann", "bob", "cat", "dan", "eve"} {
		w.User(h)
	}
	w.Friends("ann", "bob")
	w.Friends("bob", "cat")
	w.Friends("cat", "dan")
	w.Friends("dan", "eve")
	w.Block("ann", "dan")
	w.Block("eve", "ann")
	ctx := context.Background()
	if _, err := db.Q().Exec(ctx, "INSERT INTO hide_via (user_id, via_id, at) VALUES ($1, $2, $3)",
		int64(w.ID("ann")), int64(w.ID("bob")), coretest.Epoch); err != nil {
		t.Fatal(err)
	}

	g := New(&core.Deps{DB: db, Bus: &core.Bus{}})
	before := g.Epoch()
	if err := g.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if g.Epoch() <= before {
		t.Fatal("Load must bump the epoch")
	}
	for _, p := range [][2]string{{"ann", "bob"}, {"bob", "cat"}, {"cat", "dan"}, {"dan", "eve"}} {
		lvl, ok := g.Level(w.ID(p[1]), w.ID(p[0]))
		if !ok || lvl != core.LevelFriend {
			t.Fatalf("edge %s–%s not loaded", p[0], p[1])
		}
	}
	if g.Degree(w.ID("bob")) != 2 {
		t.Fatal("degree")
	}
	if !g.Blocks(w.ID("ann"), w.ID("dan")) || !g.Blocks(w.ID("eve"), w.ID("ann")) || g.Blocks(w.ID("dan"), w.ID("ann")) {
		t.Fatal("blocks not loaded one-directionally")
	}
	ex := g.Exclusions(w.ID("ann"))
	if !ex.HiddenVia.Contains(uint32(w.ID("bob"))) || !ex.Blocked.Contains(uint32(w.ID("dan"))) {
		t.Fatal("exclusions not loaded")
	}
	// ann hid via bob: bob is reachable, nobody behind him is.
	s, err := g.Search(ctx, w.ID("ann"), ex)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Dist(w.ID("cat")); ok {
		t.Fatal("routed through a hidden-via user")
	}
	// eve blocked ann; ann's view of eve is unaffected (eve is just far away).
	s, err = g.Search(ctx, w.ID("cat"), g.Exclusions(w.ID("cat")))
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := s.Dist(w.ID("eve")); !ok || d != 2 {
		t.Fatalf("dist(cat, eve) = (%d,%v)", d, ok)
	}

	// A reload replaces everything (including memory-only mutations).
	g.AddEdge(w.ID("ann"), w.ID("eve"), core.LevelFriend, coretest.Epoch)
	if err := g.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Level(w.ID("ann"), w.ID("eve")); ok {
		t.Fatal("reload kept a memory-only edge")
	}
}

func TestLoadWithoutDatabaseIsNoop(t *testing.T) {
	g := New(&core.Deps{})
	if err := g.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
}
