package metrics

import "strconv"

// The closed metric list (plan §4.7 metrics). A name maps to its allowed
// buckets; latency names use the fixed LatencyBuckets. Adding a name or a
// bucket is a reviewed change to this file: nothing outside it can be recorded.
var names = map[string][]string{
	"feed.tier_reached":          degrees,
	"feed.impressions_by_degree": degrees,
	"invites.redeemed":           {""},
	"feed.preset_switch":         {"latest", "balanced", "top"},
	"feed.latency_ms":            nil, // latency buckets
	"feed.planner":               {"author", "time"},
	"graph.deep_search":          {"count"}, // P1
	"tree.opened":                {""},
	"push.delivered":             {"ok", "gone", "failed"}, // P1

}

var degrees = []string{"1", "2", "3", "4", "5", "6", "7+"}

// Bucket is one fixed latency bucket: observations ≤ UpTo ms (UpTo 0: the open
// last bucket).
type Bucket struct {
	Label string
	UpTo  int64
}

// LatencyBuckets are the fixed latency buckets, ascending.
var LatencyBuckets = func() []Bucket {
	bounds := []int64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}
	out := make([]Bucket, 0, len(bounds)+1)
	for _, b := range bounds {
		out = append(out, Bucket{Label: "le_" + strconv.FormatInt(b, 10), UpTo: b})
	}
	return append(out, Bucket{Label: "gt_5000"})
}()

// LatencyBucket returns the label of the bucket ms falls into.
func LatencyBucket(ms int64) string {
	for _, b := range LatencyBuckets[:len(LatencyBuckets)-1] {
		if ms <= b.UpTo {
			return b.Label
		}
	}
	return LatencyBuckets[len(LatencyBuckets)-1].Label
}

// Names returns the closed list: name → allowed buckets (latency names list the
// latency bucket labels).
func Names() map[string][]string {
	out := make(map[string][]string, len(names))
	for n, bs := range names {
		if bs == nil {
			for _, b := range LatencyBuckets {
				bs = append(bs, b.Label)
			}
		}
		out[n] = append([]string(nil), bs...)
	}
	return out
}

// Allowed reports whether (name, bucket) is in the closed list.
func Allowed(name, bucket string) bool {
	bs, ok := names[name]
	if !ok {
		return false
	}
	if bs == nil {
		for _, b := range LatencyBuckets {
			if b.Label == bucket {
				return true
			}
		}
		return false
	}
	for _, b := range bs {
		if b == bucket {
			return true
		}
	}
	return false
}

func isLatency(name string) bool {
	bs, ok := names[name]
	return ok && bs == nil
}
