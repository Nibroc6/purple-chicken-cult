// Package padtest holds the timing test helpers of plan §5.1 / §7 (security
// #19): the two-sample Kolmogorov–Smirnov test over response durations, the
// "same grid point" check over durations measured by a client, and the
// server-side histogram check over pad.Stats. INT-1 runs them over the whole
// padded set; P1-W07's own tests use them per route.
//
// Owner: P1-W07.
package padtest

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad"
)

// KS returns the two-sample Kolmogorov–Smirnov statistic D of a and b and its
// asymptotic p-value (the probability of a D at least this large if both come
// from one distribution). Empty samples give D = 1, p = 0.
func KS(a, b []time.Duration) (d, p float64) {
	if len(a) == 0 || len(b) == 0 {
		return 1, 0
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	n, m := float64(len(x)), float64(len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		v := min(x[i], y[j])
		for i < len(x) && x[i] == v {
			i++
		}
		for j < len(y) && y[j] == v {
			j++
		}
		d = math.Max(d, math.Abs(float64(i)/n-float64(j)/m))
	}
	en := math.Sqrt(n * m / (n + m))
	return d, kolmogorov((en + 0.12 + 0.11/en) * d)
}

// kolmogorov is Q_KS(λ) = 2 Σ (−1)^(k−1) e^(−2k²λ²).
func kolmogorov(lambda float64) float64 {
	if lambda < 1e-9 {
		return 1
	}
	sum, sign := 0.0, 1.0
	for k := 1; k <= 100; k++ {
		term := sign * 2 * math.Exp(-2*float64(k*k)*lambda*lambda)
		sum += term
		if math.Abs(term) < 1e-12 {
			break
		}
		sign = -sign
	}
	return math.Min(math.Max(sum, 0), 1)
}

// Points maps client-measured durations to the grid point each one lies in:
// the largest point not after it (network and client overhead only add time,
// and stay under one step on a local test server).
func Points(g pad.Grid, ds []time.Duration) []int {
	out := make([]int, len(ds))
	for k, d := range ds {
		i := 1
		for g.Point(i+1) <= d {
			i++
		}
		if d < g.Point(1) {
			i = 0 // faster than the first point: not padded
		}
		out[k] = i
	}
	return out
}

// SameBucket checks that every duration of found and notFound lies in the same
// grid point and that none came back faster than the grid's first point.
func SameBucket(g pad.Grid, found, notFound []time.Duration) error {
	all := append(slices.Clone(found), notFound...)
	pts := Points(g, all)
	for k, p := range pts {
		if p == 0 {
			return fmt.Errorf("padtest: a response took %v, under the first grid point %v", all[k], g.Point(1))
		}
		if p != pts[0] {
			return fmt.Errorf("padtest: responses in grid points %d and %d (%v vs %v)", pts[0], p, all[0], all[k])
		}
	}
	return nil
}

// SameHistogram checks the server-side histogram of route (pad.Stats): every
// status in statuses has responses, all of them at one and the same grid point.
func SameHistogram(stats map[string]map[int]map[int]int, route string, statuses ...int) error {
	byStatus := stats[route]
	point := 0
	for _, st := range statuses {
		h := byStatus[st]
		if len(h) == 0 {
			return fmt.Errorf("padtest: %s has no %d responses", route, st)
		}
		if len(h) != 1 {
			return fmt.Errorf("padtest: %s %d responses spread over grid points %v", route, st, h)
		}
		for p := range h {
			if point != 0 && p != point {
				return fmt.Errorf("padtest: %s answers %d at point %d and %d at point %d", route, statuses[0], point, st, p)
			}
			point = p
		}
	}
	return nil
}
