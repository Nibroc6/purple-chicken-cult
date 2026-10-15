package graph

import (
	"math/rand/v2"
	"slices"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Reference oracles (plan §4.7, §7): deliberately naive, map-based and written
// independently of Search, so property tests can compare the two.

func excluded(ex core.Exclusions, u core.UserID) bool {
	return ex.Blocked != nil && ex.Blocked.Contains(uint32(u))
}

func transitForbidden(ex core.Exclusions, u core.UserID) bool {
	return ex.HiddenVia != nil && ex.HiddenVia.Contains(uint32(u))
}

// ReferenceBFS returns the exact shortest distance from v to every user v can
// reach in adj, never visiting users in ex.Blocked and never routing through
// users in ex.HiddenVia (they are reachable themselves). v maps to 0.
func ReferenceBFS(adj Adjacency, v core.UserID, ex core.Exclusions) map[core.UserID]int {
	dist := map[core.UserID]int{v: 0}
	queue := []core.UserID{v}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if u != v && transitForbidden(ex, u) {
			continue
		}
		for _, e := range adj[u] {
			if e.To == v || excluded(ex, e.To) {
				continue
			}
			if _, seen := dist[e.To]; !seen {
				dist[e.To] = dist[u] + 1
				queue = append(queue, e.To)
			}
		}
	}
	return dist
}

// ReferenceFirstHop returns, for every user at distance ≥ 2 from v, the best
// first hop among ALL shortest paths v→u (D22: friend+ first, then the
// reader's oldest edge, then the lower ID). It computes the full set of first
// hops reachable along shortest paths for every user (an exhaustive
// enumeration of shortest-path prefixes) and only then picks the best.
func ReferenceFirstHop(adj Adjacency, v core.UserID, ex core.Exclusions) map[core.UserID]core.UserID {
	dist := ReferenceBFS(adj, v, ex)
	byDist := map[int][]core.UserID{}
	maxD := 0
	for u, d := range dist {
		byDist[d] = append(byDist[d], u)
		maxD = max(maxD, d)
	}
	firsts := map[core.UserID]map[core.UserID]bool{}
	for _, f := range byDist[1] {
		firsts[f] = map[core.UserID]bool{f: true}
	}
	for d := 1; d < maxD; d++ {
		for _, p := range byDist[d] {
			if transitForbidden(ex, p) {
				continue
			}
			for _, e := range adj[p] {
				if dd, ok := dist[e.To]; !ok || dd != d+1 {
					continue
				}
				set := firsts[e.To]
				if set == nil {
					set = map[core.UserID]bool{}
					firsts[e.To] = set
				}
				for f := range firsts[p] {
					set[f] = true
				}
			}
		}
	}
	own := map[core.UserID]core.Edge{}
	for _, e := range adj[v] {
		own[e.To] = e
	}
	better := func(a, b core.UserID) bool {
		ea, eb := own[a], own[b]
		pa, pb := ea.Level == core.LevelFriendPlus, eb.Level == core.LevelFriendPlus
		if pa != pb {
			return pa
		}
		if !ea.Since.Equal(eb.Since) {
			return ea.Since.Before(eb.Since)
		}
		return a < b
	}
	out := map[core.UserID]core.UserID{}
	for u, d := range dist {
		if d < 2 {
			continue
		}
		var best core.UserID
		for f := range firsts[u] {
			if best == 0 || better(f, best) {
				best = f
			}
		}
		out[u] = best
	}
	return out
}

// Synth returns a synthetic undirected graph over n nodes as sorted adjacency
// lists of node indices (0…n-1): "ba" is Barabási–Albert with m = 3 (mean
// degree ≈ 6), "ws" is Watts–Strogatz with k = 6 and β = 0.1. The same seed
// always yields the same graph. Unknown models or n ≤ 0 yield nil.
func Synth(n int, model string, seed int64) [][]uint32 {
	if n <= 0 {
		return nil
	}
	//nolint:gosec // G404: deterministic synthetic data, not security sensitive.
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	sets := make([]map[uint32]struct{}, n)
	for i := range sets {
		sets[i] = map[uint32]struct{}{}
	}
	link := func(a, b int) bool {
		if a == b {
			return false
		}
		if _, ok := sets[a][uint32(b)]; ok {
			return false
		}
		sets[a][uint32(b)] = struct{}{}
		sets[b][uint32(a)] = struct{}{}
		return true
	}
	switch model {
	case "ba":
		const m = 3
		core := min(n, m+1)
		for a := range core {
			for b := a + 1; b < core; b++ {
				link(a, b)
			}
		}
		// repeated holds every edge endpoint, so a uniform pick is degree-proportional.
		var repeated []int
		for a := range core {
			for range len(sets[a]) {
				repeated = append(repeated, a)
			}
		}
		for u := core; u < n; u++ {
			targets := map[int]struct{}{}
			for len(targets) < min(m, u) {
				var t int
				if len(repeated) == 0 {
					t = rng.IntN(u)
				} else {
					t = repeated[rng.IntN(len(repeated))]
				}
				targets[t] = struct{}{}
			}
			ts := make([]int, 0, len(targets))
			for t := range targets {
				ts = append(ts, t)
			}
			slices.Sort(ts) // map order must not leak into the result
			for _, t := range ts {
				if link(u, t) {
					repeated = append(repeated, u, t)
				}
			}
		}
	case "ws":
		const k, beta = 6, 0.1
		half := min(k/2, (n-1)/2)
		for u := range n {
			for j := 1; j <= half; j++ {
				link(u, (u+j)%n)
			}
		}
		if n > k+1 {
			for j := 1; j <= half; j++ {
				for u := range n {
					w := (u + j) % n
					if rng.Float64() >= beta {
						continue
					}
					if _, ok := sets[u][uint32(w)]; !ok {
						continue
					}
					if len(sets[u]) >= n-1 {
						continue
					}
					for {
						t := rng.IntN(n)
						if t == u {
							continue
						}
						if _, ok := sets[u][uint32(t)]; ok {
							continue
						}
						delete(sets[u], uint32(w))
						delete(sets[w], uint32(u))
						link(u, t)
						break
					}
				}
			}
		}
	default:
		return nil
	}
	out := make([][]uint32, n)
	for i, s := range sets {
		l := make([]uint32, 0, len(s))
		for j := range s {
			l = append(l, j)
		}
		slices.Sort(l)
		out[i] = l
	}
	return out
}

// SynthAdjacency turns Synth output into an Adjacency over user IDs
// base, base+1, … with friend-level edges whose since is derived
// deterministically from the endpoints (distinct ages exercise the D22 rule).
func SynthAdjacency(nodes [][]uint32, base core.UserID, epoch time.Time) Adjacency {
	adj := make(Adjacency, len(nodes))
	for i, ns := range nodes {
		u := base + core.UserID(i)
		es := make([]core.Edge, 0, len(ns))
		for _, j := range ns {
			es = append(es, core.Edge{To: base + core.UserID(j), Level: core.LevelFriend, Since: SynthSince(i, int(j), epoch)})
		}
		adj[u] = es
	}
	return adj
}

// SynthSince is the deterministic, symmetric edge age used for synthetic edge
// i–j (minutes after epoch).
func SynthSince(i, j int, epoch time.Time) time.Time {
	a, b := min(i, j), max(i, j)
	h := uint64(a)*0x9e3779b97f4a7c15 ^ uint64(b)*0xc2b2ae3d27d4eb4f
	h ^= h >> 29
	return epoch.Add(time.Duration(h%(60*24*365)) * time.Minute)
}
