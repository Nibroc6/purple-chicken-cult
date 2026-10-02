package refs

import (
	"bytes"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

func codec(t testing.TB) *Codec {
	c, err := New(map[byte][]byte{1: key(7)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := codec(t)
	r := c.Seal(42, KindPost, []byte("0123456789abcdef"))
	got, ok := c.Open(42, KindPost, r)
	if !ok || string(got) != "0123456789abcdef" {
		t.Fatalf("round trip failed: %v %q", ok, got)
	}
	if strings.ContainsAny(r, "+/=") {
		t.Fatalf("ref must be base64url without padding: %q", r)
	}
}

func TestDeterministicPerViewer(t *testing.T) {
	c := codec(t)
	a1 := c.Seal(1, KindUser, []byte{0, 0, 0, 9})
	a2 := c.Seal(1, KindUser, []byte{0, 0, 0, 9})
	b := c.Seal(2, KindUser, []byte{0, 0, 0, 9})
	if a1 != a2 {
		t.Fatal("refs must be deterministic")
	}
	if a1 == b {
		t.Fatal("two viewers must get different refs")
	}
}

func TestRejects(t *testing.T) {
	c := codec(t)
	r := c.Seal(1, KindUser, []byte{1, 2, 3, 4})
	cases := map[string]func() bool{
		"other viewer": func() bool { _, ok := c.Open(2, KindUser, r); return ok },
		"other kind":   func() bool { _, ok := c.Open(1, KindPost, r); return ok },
		"viewer zero":  func() bool { _, ok := c.Open(0, KindUser, r); return ok },
		"garbage":      func() bool { _, ok := c.Open(1, KindUser, "not-a-ref"); return ok },
		"padded":       func() bool { _, ok := c.Open(1, KindUser, r+"="); return ok },
		"empty":        func() bool { _, ok := c.Open(1, KindUser, ""); return ok },
		"huge":         func() bool { _, ok := c.Open(1, KindUser, strings.Repeat("A", 4096)); return ok },
		"flipped": func() bool {
			b := []byte(r)
			if b[5] == 'A' {
				b[5] = 'B'
			} else {
				b[5] = 'A'
			}
			_, ok := c.Open(1, KindUser, string(b))
			return ok
		},
	}
	for name, f := range cases {
		if f() {
			t.Errorf("%s: must not open", name)
		}
	}
}

func TestRotation(t *testing.T) {
	old, _ := New(map[byte][]byte{1: key(1)}, 1)
	r := old.Seal(5, KindPost, []byte("x"))
	both, _ := New(map[byte][]byte{1: key(1), 2: key(2)}, 2)
	if _, ok := both.Open(5, KindPost, r); !ok {
		t.Fatal("old version must still open while its key is configured")
	}
	if nr := both.Seal(5, KindPost, []byte("x")); nr == r {
		t.Fatal("new version must produce a different ref")
	}
	retired, _ := New(map[byte][]byte{2: key(2)}, 2)
	if _, ok := retired.Open(5, KindPost, r); ok {
		t.Fatal("retired version must not open")
	}
}

func TestSealViewerZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	codec(t).Seal(0, KindUser, []byte{1})
}

func TestNewValidates(t *testing.T) {
	if _, err := New(map[byte][]byte{1: key(1)}, 2); err == nil {
		t.Fatal("missing current version must fail")
	}
	if _, err := New(map[byte][]byte{1: {1, 2, 3}}, 1); err == nil {
		t.Fatal("short key must fail")
	}
}

func TestPropertyRoundTrip(t *testing.T) {
	c := codec(t)
	rapid.Check(t, func(t *rapid.T) {
		v := rapid.Uint64Min(1).Draw(t, "viewer")
		k := Kind(rapid.ByteRange(1, 8).Draw(t, "kind"))
		target := rapid.SliceOfN(rapid.Byte(), 1, MaxTarget).Draw(t, "target")
		r := c.Seal(v, k, target)
		got, ok := c.Open(v, k, r)
		if !ok || !bytes.Equal(got, target) {
			t.Fatalf("round trip failed")
		}
		if _, ok := c.Open(v+1, k, r); ok && v+1 != 0 {
			t.Fatalf("opened under another viewer")
		}
	})
}
