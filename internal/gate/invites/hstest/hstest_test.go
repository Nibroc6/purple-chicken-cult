package hstest

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
)

// TestKnownAnswers pins the protocol arithmetic. web/src/features/invites/
// handshake.test.ts checks the browser code against the same vectors.
func TestKnownAnswers(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	hs := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	ei := append([]byte{4}, bytes.Repeat([]byte{2}, 64)...)
	ev := append([]byte{4}, bytes.Repeat([]byte{3}, 64)...)
	k := Kmac(bytes.Repeat([]byte{5}, 32), hs)
	u, j := Bits(k, Transcript(hs, ei, ev, 7, 2))
	for _, c := range []struct{ got, want string }{
		{Frame(key, hs, ei, 7), "pcc2.ABEiM0RVZneImaq7zN3u_w.BAICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI.7.8KF1C2IPRjHzFw"},
		{hex.EncodeToString(k), "3c19db04145a9ab3acef340b07b6332351bddb6c8e5927db77701ed5679912f0"},
		{hex.EncodeToString(u), "382cd90a"},
		{hex.EncodeToString(j), "d8eb15"},
		{DualFrame(k, hs, 9), "pcc2d.ABEiM0RVZneImaq7zN3u_w.9.DGeVNv_QXHUySQ"},
	} {
		if c.got != c.want {
			t.Errorf("got %s, want %s", c.got, c.want)
		}
	}
}

func TestDevicesAgree(t *testing.T) {
	ini, err := NewInitiator("AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE")
	if err != nil {
		t.Fatal(err)
	}
	ini.HS = uuid.New()
	c := NewClaimer()
	c.Claim(ini.Frame(3))
	if c.N != 3 || c.HS != ini.HS {
		t.Fatalf("claimer parsed %v %d", c.HS, c.N)
	}
	k, err := ini.Kmac(c.EV())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k, c.Kmac()) {
		t.Fatal("the devices derived different keys")
	}
}
