package conformance_test

import (
	"flag"
	"os"
	"strconv"
	"testing"
)

// defaultChecks is how many worlds each rapid property draws per run unless
// -rapid.checks is given (every check builds two databases). PCC_CONFORMANCE_CHECKS
// raises it for nightly runs.
const defaultChecks = 12

func TestMain(m *testing.M) {
	flag.Parse()
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == "rapid.checks" })
	if !set {
		n := defaultChecks
		if v, err := strconv.Atoi(os.Getenv("PCC_CONFORMANCE_CHECKS")); err == nil && v > 0 {
			n = v
		}
		_ = flag.Set("rapid.checks", strconv.Itoa(n))
	}
	os.Exit(m.Run())
}
