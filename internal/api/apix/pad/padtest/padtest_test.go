package padtest_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad/padtest"
)

func sample(r *rand.Rand, n int, base time.Duration) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = base + time.Duration(r.IntN(10_000))*time.Microsecond
	}
	return out
}

func TestKS(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	a, b := sample(r, 500, 150*time.Millisecond), sample(r, 500, 150*time.Millisecond)
	if d, p := padtest.KS(a, b); p < 0.01 {
		t.Fatalf("same distribution rejected: D=%v p=%v", d, p)
	}
	c := sample(r, 500, 153*time.Millisecond)
	if d, p := padtest.KS(a, c); p > 0.01 {
		t.Fatalf("shifted distribution accepted: D=%v p=%v", d, p)
	}
	if d, p := padtest.KS(a, a); d != 0 || p != 1 {
		t.Fatalf("identical: D=%v p=%v", d, p)
	}
	if _, p := padtest.KS(nil, a); p != 0 {
		t.Fatal("empty sample")
	}
}

func TestSameBucketAndHistogram(t *testing.T) {
	ms := time.Millisecond
	if err := padtest.SameBucket(pad.Default, []time.Duration{151 * ms, 170 * ms}, []time.Duration{152 * ms}); err != nil {
		t.Fatal(err)
	}
	if padtest.SameBucket(pad.Default, []time.Duration{151 * ms}, []time.Duration{310 * ms}) == nil {
		t.Fatal("different points accepted")
	}
	if padtest.SameBucket(pad.Default, []time.Duration{20 * ms}, []time.Duration{20 * ms}) == nil {
		t.Fatal("unpadded responses accepted")
	}
	st := map[string]map[int]map[int]int{"GET /a": {200: {1: 3}, 404: {1: 2}}, "GET /b": {200: {1: 3}, 404: {2: 1}}}
	if err := padtest.SameHistogram(st, "GET /a", 200, 404); err != nil {
		t.Fatal(err)
	}
	if padtest.SameHistogram(st, "GET /b", 200, 404) == nil {
		t.Fatal("split histogram accepted")
	}
	if padtest.SameHistogram(st, "GET /a", 200, 409) == nil {
		t.Fatal("missing status accepted")
	}
}
