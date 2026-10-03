package devseed

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/graph"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
)

// Synthetic data shape (plan §7 "Synthetic 10k graph"): seven days of posts at a
// mean of one per user per day, a share of them replies to a friend's earlier
// post, and likes from friends.
const (
	MaxSynthetic  = 1_000_000
	synthDays     = 7
	postsPerDay   = 1.0
	replyShare    = 0.15
	likeChance    = 0.08
	synthUserAge  = 400 * 24 * time.Hour
	synthEdgeBase = 390 * 24 * time.Hour // edges form within the year after the users joined
)

// synthWords is the vocabulary of synthetic post bodies (user content, not UI
// text; comma-separated so it is one token list, not a sentence).
var synthWords = strings.Split("the,a,purple,chicken,cult,meets,at,dawn,bread,garden,river,walk,letter,coffee,rain,"+
	"tomorrow,quiet,loud,bright,small,field,song,friend,market,map,window,early,late,kitchen,bicycle,lantern,"+
	"mountain,thread,story,question,answer,picnic,harbor,library,orchard", ",")

// synthetic builds the batch for Synthetic: users first … first+n-1 mirror the
// nodes of graph.Synth(n, model, seed).
func synthetic(n int, model string, seed, first int64, now time.Time) (*batch, error) {
	if n <= 0 || n > MaxSynthetic {
		return nil, fmt.Errorf("devseed: users must be within 1…%d", MaxSynthetic)
	}
	if model != "ba" && model != "ws" {
		return nil, fmt.Errorf("devseed: unknown model %q (ba or ws)", model)
	}
	if first < 1 || first+int64(n) > math.MaxUint32 {
		return nil, fmt.Errorf("devseed: user IDs %d…%d out of range", first, first+int64(n)-1)
	}
	nodes := graph.Synth(n, model, seed)
	id := func(i int) core.UserID { return core.UserID(first + int64(i)) }
	b := &batch{users: make([]userRow, 0, n)}
	joined := now.Add(-synthUserAge)
	for i, ns := range nodes {
		m := core.UserMeta{ID: id(i), ExtID: ids.Named(fmt.Sprintf("syn-user:%d", id(i))),
			Handle: "syn_" + strconv.FormatUint(uint64(id(i)), 10), DisplayName: fmt.Sprintf("Synthetic %d", i),
			Status: core.StatusActive, CreatedAt: joined, Suggestible: true, NamedInLikes: true, NameInChains: i%3 == 0,
			Preset: core.PresetBalanced, Locale: "en", Languages: []string{"en"}}
		// The inviter is the oldest earlier neighbour; node 0 and nodes with no
		// earlier neighbour (possible under "ws" rewiring) hang off node 0.
		switch {
		case i == 0:
			m.Genesis = true
		case len(ns) > 0 && int(ns[0]) < i:
			m.InvitedBy = id(int(ns[0]))
		default:
			m.InvitedBy = id(0)
		}
		b.users = append(b.users, userRow{meta: m, webauth: placeholderHash("webauthn:" + m.ExtID.String())})
		for _, j := range ns {
			if int(j) > i {
				b.edges = append(b.edges, edgeRow{a: id(i), b: id(int(j)), level: core.LevelFriend,
					since: graph.SynthSince(i, int(j), now.Add(-synthEdgeBase))})
			}
		}
	}
	//nolint:gosec // G404: deterministic synthetic data, not security sensitive.
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(first)))
	window := time.Duration(synthDays) * 24 * time.Hour
	byUser := make([][]int, n) // indexes into b.posts
	for i := range nodes {
		for k := range poisson(rng, postsPerDay*synthDays) {
			m := core.PostMeta{ID: ids.Named(fmt.Sprintf("syn-post:%d:%d", id(i), k)), Author: id(i), Version: 1, Lang: "en",
				CreatedAt: now.Add(-time.Duration(rng.Int64N(int64(window)))).Truncate(time.Millisecond)}
			switch r := rng.Float64(); {
			case r < 0.06:
				m.Audience = core.AudD1
			case r < 0.12:
				m.Audience = core.AudD2
			case r < 0.18:
				m.Audience = core.AudD3
			}
			switch r := rng.Float64(); {
			case r < 0.03:
				m.RefPolicy = core.PolNobody
			case r < 0.06:
				m.RefPolicy = core.PolFriends
			case r < 0.10:
				m.RefPolicy = core.PolTwoHops
			}
			byUser[i] = append(byUser[i], len(b.posts))
			b.posts = append(b.posts, postRow{meta: m, body: synthBody(rng)})
		}
	}
	for i, ns := range nodes {
		for _, pi := range byUser[i] {
			child := &b.posts[pi].meta
			if len(ns) > 0 && rng.Float64() < replyShare {
				if parent := pickParent(rng, b, byUser[ns[rng.IntN(len(ns))]], child.CreatedAt); parent != nil {
					child.IsReply = true
					b.links = append(b.links, core.LinkMeta{Child: child.ID, Parent: parent.ID, Kind: core.LinkRef,
						ChildAuthor: child.Author, ParentAuthor: parent.Author, CreatedAt: child.CreatedAt})
				}
			}
			for _, j := range ns {
				if rng.Float64() < likeChance {
					delay := time.Duration(rng.Int64N(int64(now.Sub(child.CreatedAt)) + 1))
					b.likes = append(b.likes, likeRow{user: id(int(j)), post: child.ID, at: child.CreatedAt.Add(delay)})
				}
			}
		}
	}
	return b, nil
}

// pickParent returns a random post among candidates that is older than at and
// may be referenced by a friend of its author (not slippery).
func pickParent(rng *rand.Rand, b *batch, candidates []int, at time.Time) *core.PostMeta {
	if len(candidates) == 0 {
		return nil
	}
	start := rng.IntN(len(candidates))
	for k := range candidates {
		m := &b.posts[candidates[(start+k)%len(candidates)]].meta
		if m.CreatedAt.Before(at) && m.RefPolicy != core.PolNobody {
			return m
		}
	}
	return nil
}

// poisson samples a Poisson(lambda) count (Knuth; lambda is small).
func poisson(rng *rand.Rand, lambda float64) int {
	l, k, p := math.Exp(-lambda), 0, 1.0
	for {
		p *= rng.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func synthBody(rng *rand.Rand) string {
	words := make([]string, 4+rng.IntN(9))
	for i := range words {
		words[i] = synthWords[rng.IntN(len(synthWords))]
	}
	return strings.Join(words, " ")
}
