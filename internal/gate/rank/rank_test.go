package rank_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/rank"
)

var now = time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC)

var (
	latest   = rank.ParamsFor(core.PresetLatest)
	balanced = rank.ParamsFor(core.PresetBalanced)
	top      = rank.ParamsFor(core.PresetTop)
)

// genCandidate draws a candidate posted within the last week by one of authors.
func genCandidate(t *rapid.T, i, authors int) rank.Candidate {
	var key [16]byte
	key[0], key[1] = byte(i>>8), byte(i)
	c := rank.Candidate{
		Key:       key,
		Author:    uint32(rapid.IntRange(1, authors).Draw(t, "author")), //nolint:gosec // small
		CreatedAt: now.Add(-time.Duration(rapid.IntRange(0, 7*24*60).Draw(t, "age_min")) * time.Minute),
		IsReply:   rapid.Bool().Draw(t, "reply"),
		N:         float64(rapid.IntRange(0, 40).Draw(t, "n2")) / 2,
		ShowLess:  rapid.IntRange(0, 6).Draw(t, "show_less"),
		// a few first-hop ties so Less's tie-breaks are exercised
		FirstHopPlus:  rapid.IntRange(0, 4).Draw(t, "plus") == 0,
		FirstHopSince: now.Add(-time.Duration(rapid.IntRange(1, 3).Draw(t, "since_d")) * 24 * time.Hour),
		FirstHopID:    uint32(rapid.IntRange(1, 3).Draw(t, "hop")), //nolint:gosec // small
	}
	if rapid.IntRange(0, 5).Draw(t, "same_time") == 0 {
		c.CreatedAt = now.Add(-time.Hour) // equal created_at: ties by Less
	}
	for k := rapid.IntRange(0, 4).Draw(t, "kids"); k > 0; k-- {
		c.KidsN = append(c.KidsN, float64(rapid.IntRange(0, 20).Draw(t, "kid2"))/2)
	}
	return c
}

func genTier(t *rapid.T, maxLen int) []rank.Candidate {
	n := rapid.IntRange(0, maxLen).Draw(t, "len")
	authors := rapid.IntRange(1, 12).Draw(t, "authors")
	cs := make([]rank.Candidate, n)
	for i := range cs {
		cs[i] = genCandidate(t, i, authors)
	}
	return cs
}

func genParams(t *rapid.T) rank.Params {
	return rapid.SampledFrom([]rank.Params{latest, balanced, top}).Draw(t, "preset")
}

func keys(ss []rank.Scored) [][16]byte {
	out := make([][16]byte, len(ss))
	for i, s := range ss {
		out[i] = s.Key
	}
	return out
}

func sortedKeys(cs []rank.Candidate) [][16]byte {
	out := make([][16]byte, len(cs))
	for i, c := range cs {
		out[i] = c.Key
	}
	slices.SortFunc(out, func(a, b [16]byte) int { return slices.Compare(a[:], b[:]) })
	return out
}

func TestParamsFor(t *testing.T) {
	if latest.Lambda != 0 || latest.S != 0 || latest.AuthorCap {
		t.Fatalf("latest %+v", latest)
	}
	if balanced.Lambda != 0.5 || !balanced.AuthorCap || balanced.Beta != 0.3 || balanced.K != 20 ||
		balanced.R != 12*time.Hour || balanced.ShowLessStep != 8*time.Hour || balanced.ColdStart != 2*time.Hour {
		t.Fatalf("balanced %+v", balanced)
	}
	// S = λ·τ·ln4: ±16.6 h under Balanced, ±33.3 h under Top (§5.1 table).
	if got := balanced.S.Hours(); math.Abs(got-12*math.Log(4)) > 1e-9 {
		t.Fatalf("balanced S %v", balanced.S)
	}
	if got := top.S.Hours(); math.Abs(got-24*math.Log(4)) > 1e-9 {
		t.Fatalf("top S %v", top.S)
	}
}

// Acceptance: ranking is a permutation of its input.
func TestPropertyPermutation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := genTier(t, 60)
		out := rank.RankTier(genParams(t), now, cs)
		got := keys(out)
		slices.SortFunc(got, func(a, b [16]byte) int { return slices.Compare(a[:], b[:]) })
		if !slices.Equal(got, sortedKeys(cs)) {
			t.Fatalf("not a permutation")
		}
	})
}

// Acceptance: λ = 0 ⇒ strictly chronological (ties by Less), including with
// show-less counts, replies, likes and credit present.
func TestPropertyLatestIsChronological(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := genTier(t, 60)
		out := rank.RankTier(latest, now, cs)
		for i, s := range out {
			if !s.TEff.Equal(s.CreatedAt) || s.Shift != 0 || s.ReplyPenalty != 0 || s.ShowLessPenalty != 0 || s.Credit != 0 {
				t.Fatalf("latest moved a post: %+v", s)
			}
			if !slices.Equal(s.Reasons, []string{rank.ReasonChronological}) {
				t.Fatalf("reasons %v", s.Reasons)
			}
			if i == 0 {
				continue
			}
			prev := out[i-1]
			if prev.CreatedAt.Before(s.CreatedAt) {
				t.Fatalf("slot %d is newer than slot %d", i, i-1)
			}
			if !rank.Less(prev, s) {
				t.Fatalf("slots %d/%d not ordered by Less", i-1, i)
			}
		}
	})
}

// capHolds checks the author cap at every slot, allowing a violation only
// where every remaining post's author was already capped (no choice left).
func capHolds(out []rank.Scored) (int, bool) {
	count := func(i int, a uint32) int {
		n := 0
		for j := max(0, i-(rank.CapWindow-1)); j < i; j++ {
			if out[j].Author == a {
				n++
			}
		}
		return n
	}
	for i, s := range out {
		if count(i, s.Author) < rank.CapPerAuthor {
			continue
		}
		for _, r := range out[i:] {
			if count(i, r.Author) < rank.CapPerAuthor {
				return i, false
			}
		}
	}
	return 0, true
}

// Acceptance: the author cap holds under Balanced (and Top) and is off under Latest.
func TestPropertyAuthorCap(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := genTier(t, 60)
		p := rapid.SampledFrom([]rank.Params{balanced, top}).Draw(t, "preset")
		out := rank.RankTier(p, now, cs)
		if i, ok := capHolds(out); !ok {
			t.Fatalf("author cap broken at slot %d", i)
		}
		// Off under Latest: exactly the sorted order, however many posts one author has.
		lat := rank.RankTier(latest, now, cs)
		for i := 1; i < len(lat); i++ {
			if rank.Less(lat[i], lat[i-1]) {
				t.Fatalf("latest reordered by the cap at %d", i)
			}
		}
	})
}

func TestAuthorCapExample(t *testing.T) {
	// Author 1 posted the 6 newest posts, authors 2..7 one older post each.
	var cs []rank.Candidate
	for i := range 6 {
		cs = append(cs, rank.Candidate{Key: [16]byte{byte(i)}, Author: 1, CreatedAt: now.Add(-time.Duration(i+3) * time.Hour)})
	}
	for i := range 6 {
		cs = append(cs, rank.Candidate{Key: [16]byte{byte(10 + i)}, Author: uint32(2 + i), CreatedAt: now.Add(-time.Duration(20+i) * time.Hour)}) //nolint:gosec // small
	}
	var authors []uint32
	for _, s := range rank.RankTier(balanced, now, cs) {
		authors = append(authors, s.Author)
	}
	want := []uint32{1, 1, 2, 3, 4, 5, 6, 7, 1, 1, 1, 1}
	if !slices.Equal(authors, want) {
		t.Fatalf("balanced %v, want %v", authors, want)
	}
	authors = authors[:0]
	for _, s := range rank.RankTier(latest, now, cs) {
		authors = append(authors, s.Author)
	}
	if !slices.Equal(authors, []uint32{1, 1, 1, 1, 1, 1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("latest %v", authors)
	}
}

// Acceptance: the clamp term stays within ±S; a post younger than the cold
// start is never shifted down.
func TestPropertyClampAndColdStart(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := genParams(t)
		c := genCandidate(t, 0, 3)
		c.B = float64(rapid.IntRange(0, 60).Draw(t, "b"))
		if rapid.Bool().Draw(t, "young") {
			c.CreatedAt = now.Add(-time.Duration(rapid.IntRange(0, 119).Draw(t, "young_min")) * time.Minute)
		}
		s := rank.Score(p, now, c)
		if s.Shift > p.S || s.Shift < -p.S {
			t.Fatalf("shift %v outside ±%v", s.Shift, p.S)
		}
		if now.Sub(c.CreatedAt) < p.ColdStart && s.Shift < 0 {
			t.Fatalf("cold start shifted down: %v", s.Shift)
		}
		if s.TEff != c.CreatedAt.Add(s.Shift-s.ReplyPenalty-s.ShowLessPenalty) {
			t.Fatal("t_eff does not add up")
		}
	})
}

// Acceptance: the upward credit is at most n (n_eff ≤ 2n), whatever the replies.
func TestPropertyCreditAtMostN(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := genParams(t)
		c := genCandidate(t, 0, 3)
		s := rank.Score(p, now, c)
		if s.NEff > 2*c.N+1e-12 || s.NEff < c.N {
			t.Fatalf("n_eff %v for n %v", s.NEff, c.N)
		}
		var sum float64
		for _, k := range c.KidsN {
			sum += k
		}
		if p.Lambda > 0 && math.Abs(s.NEff-(c.N+min(p.Beta*sum, c.N))) > 1e-12 {
			t.Fatalf("n_eff %v, want n + min(β·Σ, n)", s.NEff)
		}
		if s.Credit < 0 {
			t.Fatalf("negative credit %v", s.Credit)
		}
	})
}

func TestNoNetworkNoCredit(t *testing.T) {
	c := rank.Candidate{CreatedAt: now.Add(-5 * time.Hour), N: 0, KidsN: []float64{10, 10, 10}}
	s := rank.Score(balanced, now, c)
	if s.NEff != 0 || s.Credit != 0 || s.Shift != 0 || !s.TEff.Equal(c.CreatedAt) || len(s.Reasons) != 0 {
		t.Fatalf("a post nobody liked gained from its replies: %+v", s)
	}
}

// Acceptance: the reply penalty is exactly 6 h under Balanced (12 h under Top, 0 under Latest).
func TestReplyPenalty(t *testing.T) {
	c := rank.Candidate{CreatedAt: now.Add(-10 * time.Hour), IsReply: true}
	for _, tc := range []struct {
		p    rank.Params
		want time.Duration
	}{{balanced, 6 * time.Hour}, {top, 12 * time.Hour}, {latest, 0}} {
		s := rank.Score(tc.p, now, c)
		if s.ReplyPenalty != tc.want || s.TEff != c.CreatedAt.Add(-tc.want) {
			t.Errorf("λ=%v: penalty %v t_eff %v", tc.p.Lambda, s.ReplyPenalty, s.TEff)
		}
	}
	// The penalty applies to young posts too (cold start floors only the clamp term).
	young := rank.Score(balanced, now, rank.Candidate{CreatedAt: now.Add(-time.Minute), IsReply: true})
	if young.ReplyPenalty != 6*time.Hour || !slices.Contains(young.Reasons, rank.ReasonReplyPenalty) ||
		!slices.Contains(young.Reasons, rank.ReasonColdStart) {
		t.Fatalf("young reply %+v", young)
	}
}

// Acceptance: the show-less penalty is 8 h·min(n,3)·λ, outside the clamp.
func TestShowLessPenalty(t *testing.T) {
	for _, p := range []rank.Params{latest, balanced, top} {
		for n := range 7 {
			c := rank.Candidate{CreatedAt: now.Add(-30 * time.Hour), N: 50, ShowLess: n}
			s := rank.Score(p, now, c)
			want := time.Duration(8 * float64(time.Hour) * float64(min(n, 3)) * p.Lambda)
			if s.ShowLessPenalty != want {
				t.Errorf("λ=%v n=%d: penalty %v, want %v", p.Lambda, n, s.ShowLessPenalty, want)
			}
			if s.Shift != rank.Score(p, now, rank.Candidate{CreatedAt: c.CreatedAt, N: 50}).Shift {
				t.Errorf("show-less changed the clamp term")
			}
			if got := slices.Contains(s.Reasons, rank.ReasonShowLess); got != (want > 0) {
				t.Errorf("λ=%v n=%d: reasons %v", p.Lambda, n, s.Reasons)
			}
		}
	}
	if s := rank.Score(balanced, now, rank.Candidate{CreatedAt: now, ShowLess: -2}); s.ShowLessPenalty != 0 {
		t.Fatalf("negative count %v", s.ShowLessPenalty)
	}
}

// Balanced in plain language: about 8 h up per doubling of (1+n), at most ~17 h.
func TestBalancedShiftAndReasons(t *testing.T) {
	c := rank.Candidate{CreatedAt: now.Add(-20 * time.Hour), N: 1}
	s := rank.Score(balanced, now, c)
	if math.Abs(s.Shift.Hours()-12*math.Log(2)) > 1e-6 || !slices.Equal(s.Reasons, []string{rank.ReasonNetworkLikes}) {
		t.Fatalf("n=1: %v %v", s.Shift, s.Reasons)
	}
	c.N = 1e9
	if s = rank.Score(balanced, now, c); s.Shift != balanced.S {
		t.Fatalf("not clamped: %v", s.Shift)
	}
	c.N, c.B = 0, 3
	if s = rank.Score(balanced, now, c); s.Shift >= 0 || !slices.Equal(s.Reasons, []string{rank.ReasonBelowUsual}) {
		t.Fatalf("below usual: %v %v", s.Shift, s.Reasons)
	}
	c.N, c.B, c.KidsN = 2, 0, []float64{5, 5}
	s = rank.Score(balanced, now, c)
	if s.NEff != 4 || s.Credit <= 0 || !slices.Contains(s.Reasons, rank.ReasonGoodReplies) {
		t.Fatalf("credit %+v", s)
	}
	if args := s.LogArgs(balanced); len(args)%2 != 0 || args[0] != "lambda" {
		t.Fatalf("log args %v", args)
	}
}

func TestLessTieBreaks(t *testing.T) {
	at := now.Add(-time.Hour)
	a := rank.Scored{TEff: at, Candidate: rank.Candidate{FirstHopPlus: true, FirstHopID: 9}}
	b := rank.Scored{TEff: at, Candidate: rank.Candidate{FirstHopSince: now.Add(-48 * time.Hour), FirstHopID: 1}}
	c := rank.Scored{TEff: at, Candidate: rank.Candidate{FirstHopSince: now.Add(-24 * time.Hour), FirstHopID: 1}}
	d := rank.Scored{TEff: at, Candidate: rank.Candidate{FirstHopSince: now.Add(-24 * time.Hour), FirstHopID: 2}}
	e := rank.Scored{TEff: at, Candidate: rank.Candidate{FirstHopSince: now.Add(-24 * time.Hour), FirstHopID: 2, Key: [16]byte{1}}}
	newer := rank.Scored{TEff: now}
	order := []rank.Scored{newer, a, b, c, d, e}
	for i := range order {
		for j := range order {
			if got := rank.Less(order[i], order[j]); got != (i < j) {
				t.Errorf("Less(%d,%d) = %v", i, j, got)
			}
		}
	}
}
