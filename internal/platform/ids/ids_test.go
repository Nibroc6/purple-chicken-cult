package ids

import "testing"

func TestNamedDeterministic(t *testing.T) {
	a, b := Named("p1"), Named("p1")
	if a != b {
		t.Fatal("Named must be deterministic")
	}
	if Named("p1") == Named("p2") {
		t.Fatal("different labels must differ")
	}
	if a.Version() != 8 {
		t.Fatalf("want version 8, got %d", a.Version())
	}
}

func TestSeq(t *testing.T) {
	var s Seq
	a, b := s.New(), s.New()
	if a == b {
		t.Fatal("Seq must not repeat")
	}
}
