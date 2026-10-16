// Package conformance holds the horizon / noninterference suite of plan §7
// (ARCHITECTURE §2.2, §13): it builds pairs of worlds that differ only outside
// a viewer's horizon H(V), replays the viewer's calls through the gate services
// in both and requires identical normalized outputs, and checks the additional
// properties of plan §7 (chain oracle, component isolation, oracle uniformity,
// foreign tokens, freshness, inviter labels). The perf-tagged sim_test.go runs
// the bot-isolation simulation on the synthetic 10k graph.
//
// Everything lives in _test.go files; this package has no production code.
//
// Owner: INT-0 → INT-1.
package conformance
