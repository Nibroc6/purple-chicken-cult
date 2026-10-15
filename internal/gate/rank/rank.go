// Package rank is the pure ranking function of ARCHITECTURE §5.1, §5.5, §5.6:
// a bounded time shift within a tier. It imports no store and no other domain
// package; anything may import it (archtest A8).
//
//	t_eff = created + clamp(λ·τ·ln((1+n_eff)/(1+b)), −S, +S) − λ·R·is_reply − λ·min(ShowLess,3)·ShowLessStep
//	n_eff = n + min(β·Σ KidsN, n)
//
// For a post younger than ColdStart the clamp term is floored at 0. The reply
// and show-less penalties sit outside the clamp and vanish at λ = 0, so Latest
// is exactly chronological. RankTier sorts a whole tier with Less and then
// applies the author cap (≤ 2 posts per author in any 10 consecutive slots,
// off under Latest). Ranking only reorders: the output is always a permutation
// of the input (§5 invariant).
//
// Owner: P0-W06 → P1-W08.
package rank

import (
	"bytes"
	"container/heap"
	"math"
	"slices"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Author cap (§5.1): at most CapPerAuthor posts by one author in any CapWindow
// consecutive slots.
const (
	CapWindow    = 10
	CapPerAuthor = 2
	// ShowLessMax bounds the show-less count that lowers an author (fidelity #14).
	ShowLessMax = 3
)

// Reason codes of a ranked post (WhyView.reasons, §5.3; closed list).
const (
	ReasonChronological = "chronological"
	ReasonNetworkLikes  = "network_likes"
	ReasonBelowUsual    = "below_author_usual"
	ReasonReplyPenalty  = "reply_penalty"
	ReasonGoodReplies   = "good_replies"
	ReasonColdStart     = "cold_start"
	ReasonShowLess      = "show_less"
)

// Params are the ranking parameters of a preset.
type Params struct {
	Lambda, Beta            float64
	Tau, S, R, ShowLessStep time.Duration
	ColdStart               time.Duration
	K                       int
	AuthorCap               bool
}

// ParamsFor returns the parameters of p: Latest λ=0, Balanced λ=0.5, Top λ=1;
// τ=24h, S=λ·τ·ln4, β=0.3, R=12h, ShowLessStep=8h, cold start 2h, K=20.
func ParamsFor(p core.Preset) Params {
	lambda := 0.5
	switch p {
	case core.PresetLatest:
		lambda = 0
	case core.PresetTop:
		lambda = 1
	}
	tau := 24 * time.Hour
	return Params{
		Lambda: lambda, Beta: 0.3, Tau: tau, S: time.Duration(lambda * float64(tau) * math.Log(4)),
		R: 12 * time.Hour, ShowLessStep: 8 * time.Hour, ColdStart: 2 * time.Hour, K: 20, AuthorCap: lambda > 0,
	}
}

// Candidate is a post to rank within one tier.
type Candidate struct {
	Key           [16]byte
	Author        uint32
	CreatedAt     time.Time
	IsReply       bool
	N             float64
	KidsN         []float64 // n_kid(C) per visible child (P1: already including β·grandchildren)
	B             float64   // author baseline; 0 in P0/P1
	FirstHopPlus  bool
	FirstHopSince time.Time
	FirstHopID    uint32
	ShowLess      int // the reader's show_less count for this author
}

// Scored is a ranked candidate with its components (logged for §5.5 comparison).
type Scored struct {
	Candidate
	TEff                                         time.Time
	Shift, ReplyPenalty, ShowLessPenalty, Credit time.Duration
	NEff                                         float64
	Reasons                                      []string
}

// finite maps NaN and negative values to 0 (n, b and n_kid are never negative).
func finite(x float64) float64 {
	if math.IsNaN(x) || x < 0 {
		return 0
	}
	if math.IsInf(x, 1) {
		return math.MaxFloat64
	}
	return x
}

// shift is clamp(λ·τ·ln((1+n)/(1+b)), −S, +S).
func shift(p Params, n, b float64) time.Duration {
	x := p.Lambda * float64(p.Tau) * math.Log((1+n)/(1+b))
	s := float64(p.S)
	switch {
	case math.IsNaN(x):
		return 0
	case x > s:
		x = s
	case x < -s:
		x = -s
	}
	return time.Duration(x)
}

// Score computes t_eff and its components. Under λ = 0 (Latest) every term
// vanishes: t_eff = created_at, reason "chronological".
func Score(p Params, now time.Time, c Candidate) Scored {
	s := Scored{Candidate: c, TEff: c.CreatedAt, NEff: finite(c.N)}
	if p.Lambda <= 0 {
		s.Reasons = []string{ReasonChronological}
		return s
	}
	n, b := finite(c.N), finite(c.B)
	var kids float64
	for _, k := range c.KidsN {
		kids += finite(k)
	}
	s.NEff = n + min(p.Beta*kids, n)
	full, base := shift(p, s.NEff, b), shift(p, n, b)
	cold := now.Sub(c.CreatedAt) < p.ColdStart
	if cold {
		full, base = max(full, 0), max(base, 0)
	}
	s.Shift, s.Credit = full, full-base
	if c.IsReply {
		s.ReplyPenalty = time.Duration(p.Lambda * float64(p.R))
	}
	if sl := min(max(c.ShowLess, 0), ShowLessMax); sl > 0 {
		s.ShowLessPenalty = time.Duration(p.Lambda * float64(sl) * float64(p.ShowLessStep))
	}
	s.TEff = c.CreatedAt.Add(s.Shift - s.ReplyPenalty - s.ShowLessPenalty)

	switch {
	case s.Shift > 0:
		s.Reasons = append(s.Reasons, ReasonNetworkLikes)
	case s.Shift < 0:
		s.Reasons = append(s.Reasons, ReasonBelowUsual)
	}
	if s.Credit > 0 {
		s.Reasons = append(s.Reasons, ReasonGoodReplies)
	}
	if s.ReplyPenalty > 0 {
		s.Reasons = append(s.Reasons, ReasonReplyPenalty)
	}
	if s.ShowLessPenalty > 0 {
		s.Reasons = append(s.Reasons, ReasonShowLess)
	}
	if cold {
		s.Reasons = append(s.Reasons, ReasonColdStart)
	}
	return s
}

// LogArgs are the t_eff components as slog key/value pairs with allowlisted
// keys (logx): callers log them at debug next to logx.UID(viewer) (§5.5
// "every t_eff component is logged for comparison against chronological").
func (s Scored) LogArgs(p Params) []any {
	return []any{
		"lambda", p.Lambda, "n", s.NEff, "shift_min", s.Shift.Minutes(), "credit_min", s.Credit.Minutes(),
		"penalty_min", (s.ReplyPenalty + s.ShowLessPenalty).Minutes(), "reason", s.Reasons,
	}
}

// Less is THE comparator for feed tiers and tree rows: TEff desc, then friend+
// first hop first, then older first-hop edge, then lower first-hop ID, then Key asc.
func Less(a, b Scored) bool {
	if !a.TEff.Equal(b.TEff) {
		return a.TEff.After(b.TEff)
	}
	if a.FirstHopPlus != b.FirstHopPlus {
		return a.FirstHopPlus
	}
	if !a.FirstHopSince.Equal(b.FirstHopSince) {
		return a.FirstHopSince.Before(b.FirstHopSince)
	}
	if a.FirstHopID != b.FirstHopID {
		return a.FirstHopID < b.FirstHopID
	}
	return bytes.Compare(a.Key[:], b.Key[:]) < 0
}

func cmp(a, b Scored) int {
	switch {
	case Less(a, b):
		return -1
	case Less(b, a):
		return 1
	}
	return 0
}

// RankTier scores every candidate, sorts with Less and applies the author cap
// (≤ 2 per author in any 10 consecutive slots; off when Lambda == 0 or
// !AuthorCap). The output is always a permutation of the input.
func RankTier(p Params, now time.Time, cs []Candidate) []Scored {
	out := make([]Scored, len(cs))
	for i, c := range cs {
		out[i] = Score(p, now, c)
	}
	slices.SortStableFunc(out, cmp)
	if !p.AuthorCap || p.Lambda <= 0 || len(out) <= CapPerAuthor {
		return out
	}
	return capAuthors(out)
}

// capAuthors reorders a sorted slice so no author holds more than CapPerAuthor
// of any CapWindow consecutive slots. Greedy and stable: each slot takes the
// best-ranked remaining post whose author is under the cap in the previous
// CapWindow−1 slots. Only when every remaining author is capped (the tail of a
// tier dominated by one author) does the best remaining post go next anyway.
func capAuthors(sorted []Scored) []Scored {
	queues := map[uint32][]int{}
	for i, s := range sorted {
		queues[s.Author] = append(queues[s.Author], i)
	}
	h := &authorHeap{}
	for a, q := range queues {
		*h = append(*h, authorHead{author: a, head: q[0]})
	}
	heap.Init(h)

	out := make([]Scored, 0, len(sorted))
	inWindow := map[uint32]int{}
	var recent []uint32 // the last CapWindow−1 placed authors
	var parked []authorHead
	for h.Len() > 0 {
		parked = parked[:0]
		var pick authorHead
		found := false
		for h.Len() > 0 {
			top := heap.Pop(h).(authorHead)
			if inWindow[top.author] < CapPerAuthor {
				pick, found = top, true
				break
			}
			parked = append(parked, top) // at most (CapWindow−1)/CapPerAuthor authors are capped
		}
		if !found { // everyone left is capped: take the best-ranked remaining post
			best := 0
			for i := range parked {
				if parked[i].head < parked[best].head {
					best = i
				}
			}
			pick = parked[best]
			parked = slices.Delete(parked, best, best+1)
		}
		for _, a := range parked {
			heap.Push(h, a)
		}

		out = append(out, sorted[pick.head])
		q := queues[pick.author][1:]
		queues[pick.author] = q
		if len(q) > 0 {
			heap.Push(h, authorHead{author: pick.author, head: q[0]})
		}
		recent = append(recent, pick.author)
		inWindow[pick.author]++
		if len(recent) > CapWindow-1 {
			inWindow[recent[0]]--
			recent = recent[1:]
		}
	}
	return out
}

type authorHead struct {
	author uint32
	head   int // index (in sorted order) of the author's best remaining post
}

type authorHeap []authorHead

func (h authorHeap) Len() int           { return len(h) }
func (h authorHeap) Less(i, j int) bool { return h[i].head < h[j].head }
func (h authorHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *authorHeap) Push(x any)        { *h = append(*h, x.(authorHead)) }
func (h *authorHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
