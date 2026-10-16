//go:build perf

package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

// TestSimBotIsolation is the bot-isolation simulation (plan §7, ARCHITECTURE
// §4.4 and §13, fidelity #33): on the synthetic 10k graph a 500-account bot
// subtree hangs off one real user through a single accepted edge; the bots
// (young, on probation, so every post is forced d1) post 20 a day and like each
// other. Over real readers' feeds, bot posts' share of impressions at degree
// ≤ 3 is ≤ 0.1%; after the real user unfriends the bridge it is exactly 0.
//
// Run: PCC_REQUIRE_DB=1 go test -tags perf -run TestSim ./internal/gate/conformance/
func TestSimBotIsolation(t *testing.T) {
	const (
		n        = 10_000
		seed     = 11
		bots     = 500
		perDay   = 20
		days     = 2
		randoms  = 30
		maxPages = 300
	)
	a := wiretest.NewApp(t)
	d := a.Deps()
	ctx := context.Background()
	if err := a.DevSeed.Synthetic(ctx, n, "ba", seed); err != nil {
		t.Fatal(err)
	}
	now := d.Clock.Now()
	rng := rand.New(rand.NewPCG(seed, 5)) //nolint:gosec // simulation data

	// The bridge: an ordinary real user (not a hub).
	bridge := core.UserID(5000)
	for d.Graph.Degree(bridge) > 6 {
		bridge++
	}
	bridgeHandle := fmt.Sprintf("syn_%d", bridge)

	// The bot subtree as one devseed fixture: users, a random tree, posts, likes.
	type obj = map[string]any
	var users, friends, posts, likes []obj
	botHandle := func(i int) string { return fmt.Sprintf("bot_%03d", i) }
	for i := range bots {
		inv := bridgeHandle
		if i > 0 {
			inv = botHandle(rng.IntN(i))
			friends = append(friends, obj{"a": inv, "b": botHandle(i), "age": "48h"})
		}
		users = append(users, obj{"handle": botHandle(i), "invited_by": inv, "age": "120h", "probation": "720h"})
	}
	friends = append(friends, obj{"a": bridgeHandle, "b": botHandle(0), "age": "72h"})
	window := time.Duration(days) * 24 * time.Hour
	label := 0
	var botPosts []string
	for i := range bots {
		for range perDay * days {
			label++
			l := fmt.Sprintf("bp%06d", label)
			botPosts = append(botPosts, l)
			age := time.Duration(rng.Int64N(int64(window))).Truncate(time.Second)
			posts = append(posts, obj{"label": l, "author": botHandle(i), "body": "buy now " + l, "audience": "d1",
				"audience_forced": true, "age": age.String()})
		}
	}
	for range 5 * len(botPosts) {
		likes = append(likes, obj{"user": botHandle(rng.IntN(bots)), "post": botPosts[rng.IntN(len(botPosts))]})
	}
	fx, err := json.Marshal(obj{"users": users, "friends": friends, "posts": posts, "likes": dedupLikes(likes)})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DevSeed.LoadFixtureJSON(ctx, "sim-bots", fx); err != nil {
		t.Fatal(err)
	}
	botIDs := map[core.UserID]bool{}
	firstBot, _ := a.Users.ByHandle(ctx, botHandle(0))
	for i := range bots {
		id, ok := a.Users.ByHandle(ctx, botHandle(i))
		if !ok {
			t.Fatalf("bot %d missing", i)
		}
		botIDs[id] = true
	}

	// Readers: the bridge, its real friends, and random real users.
	readers := []core.UserID{bridge}
	for _, e := range d.Graph.Friends(bridge) {
		if !botIDs[e.To] {
			readers = append(readers, e.To)
		}
	}
	for range randoms {
		readers = append(readers, core.UserID(1+rng.IntN(n)))
	}

	measure := func() (botImp, total int) {
		for _, r := range readers {
			vctx := core.WithViewer(ctx, core.Viewer{ID: r, Kind: core.SessUser, AuthAt: now})
			cursor := ""
		pages:
			for range maxPages {
				fp, err := a.Feed.Page(vctx, "", cursor)
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range fp.Items {
					var shown []gate.PostView
					if it.Post != nil {
						shown = append(shown, *it.Post)
					}
					shown = append(shown, it.Thread...)
					for _, pv := range shown {
						if pv.Tier > 3 {
							break pages
						}
						total++
						if id, ok := a.Users.ByHandle(ctx, pv.Author.Handle); ok && botIDs[id] {
							botImp++
						}
					}
				}
				if cursor = fp.Next; cursor == "" {
					break
				}
			}
		}
		return botImp, total
	}

	start := time.Now()
	botImp, total := measure()
	share := float64(botImp) / float64(max(total, 1))
	t.Logf("with the bridge: %d readers, %d impressions at degree ≤ 3, %d of bot posts (%.4f%%) in %v",
		len(readers), total, botImp, 100*share, time.Since(start))
	if share > 0.001 {
		t.Fatalf("bot share %.4f%% > 0.1%%", 100*share)
	}

	// The bridge unfriends the bot.
	if err := a.Friends.Unfriend(core.WithViewer(ctx, core.Viewer{ID: bridge, Kind: core.SessUser, AuthAt: now}),
		d.Refs.User(bridge, firstBot)); err != nil {
		t.Fatal(err)
	}
	botImp, total = measure()
	t.Logf("after the unfriend: %d impressions at degree ≤ 3, %d of bot posts", total, botImp)
	if botImp != 0 {
		t.Fatalf("%d bot impressions after the bridge was cut", botImp)
	}
}

func dedupLikes(ls []map[string]any) []map[string]any {
	seen := map[string]bool{}
	return slices.DeleteFunc(ls, func(l map[string]any) bool {
		k := fmt.Sprint(l["user"], "/", l["post"])
		if seen[k] {
			return true
		}
		seen[k] = true
		return false
	})
}
