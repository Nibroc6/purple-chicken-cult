// Package rank is the pure ranking function of ARCHITECTURE §5.1, §5.5, §5.6:
// a bounded time shift within a tier. It imports no store and no other domain
// package; anything may import it (archtest A8).
//
// Owner: P0-W06 → P1-W08. Scaffold: full signatures; Score is chronological
// (t_eff = created_at), which is exactly the Latest preset. W06 implements
//
//	t_eff = created + clamp(λ·τ·ln((1+n_eff)/(1+b)), −S, +S) − λ·R·is_reply − λ·min(ShowLess,3)·ShowLessStep
//	n_eff = n + min(β·Σ KidsN, n)
//
// with the cold-start floor and the author cap.
package rank

import (
	"bytes"
	"math"
	"slices"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
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

// Score computes t_eff. Scaffold: chronological (valid for Latest).
func Score(p Params, now time.Time, c Candidate) Scored {
	s := Scored{Candidate: c, TEff: c.CreatedAt, NEff: c.N}
	if p.Lambda == 0 {
		s.Reasons = []string{"chronological"}
	}
	return s
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

// RankTier scores every candidate and sorts with Less. W06 adds the author cap
// (≤2 per author in any 10 consecutive slots; off when Lambda == 0). The output
// is always a permutation of the input.
func RankTier(p Params, now time.Time, cs []Candidate) []Scored {
	out := make([]Scored, len(cs))
	for i, c := range cs {
		out[i] = Score(p, now, c)
	}
	slices.SortStableFunc(out, func(a, b Scored) int {
		switch {
		case Less(a, b):
			return -1
		case Less(b, a):
			return 1
		}
		return 0
	})
	return out
}
