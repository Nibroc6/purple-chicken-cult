package authapi_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad/padtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc/dbsctest"
)

var (
	regChallenge = regexp.MustCompile(`challenge="([A-Za-z0-9_-]+)"`)
	refChallenge = regexp.MustCompile(`^"([A-Za-z0-9_-]+)";id="([^"]+)"$`)
)

func b64d(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// register binds b's session to dev over HTTP (as the browser does: no CSRF header).
func (b *browser) register(dev *dbsctest.Device) {
	b.s.t.Helper()
	rec := b.do(http.MethodGet, "/api/session", nil, nil)
	m := regChallenge.FindStringSubmatch(rec.Header().Get(dbsc.HeaderRegistration))
	if m == nil {
		b.s.t.Fatalf("no registration offer: %v", rec.Header())
	}
	jwt := dev.Registration(b64d(b.s.t, m[1]), b.s.h.Origin+dbsc.RegisterPath)
	rec = b.do(http.MethodPost, "/api/auth/dbsc/register", nil,
		map[string]string{dbsc.HeaderResponse: string(jwt), "X-PCC-CSRF": ""})
	want(b.s.t, rec, http.StatusOK, "")
	if b.bound == "" {
		b.s.t.Fatal("no bound cookie")
	}
}

// refresh renews b's bound cookie with dev and returns the proof it sent.
func (b *browser) refresh(dev *dbsctest.Device) (string, int) {
	b.s.t.Helper()
	rec := b.do(http.MethodPost, "/api/auth/dbsc/refresh", nil, map[string]string{"X-PCC-CSRF": ""})
	if rec.Code != http.StatusUnauthorized {
		return "", rec.Code
	}
	m := refChallenge.FindStringSubmatch(rec.Header().Get(dbsc.HeaderChallenge))
	if m == nil {
		b.s.t.Fatalf("challenge header %q", rec.Header().Get(dbsc.HeaderChallenge))
	}
	jwt := string(dev.Refresh(b64d(b.s.t, m[1]), b.s.h.Origin+dbsc.RefreshPath, m[2]))
	rec = b.do(http.MethodPost, "/api/auth/dbsc/refresh", nil, map[string]string{dbsc.HeaderResponse: jwt, "X-PCC-CSRF": ""})
	return jwt, rec.Code
}

func TestDBSCOverHTTP(t *testing.T) {
	s := newSuite(t)
	s.h.App.Config().DBSC = true
	a := authtest.New(s.h.Origin)
	b := s.enroll("alice", a)
	dev := dbsctest.NewES256()
	b.register(dev)
	want(t, b.do(http.MethodGet, "/api/auth/passkeys", nil, nil), http.StatusOK, "")
	if h := b.do(http.MethodGet, "/api/session", nil, nil).Header().Get(dbsc.HeaderRegistration); h != "" {
		t.Fatalf("bound session offered %q", h)
	}
	// The long-lived cookie replayed from another browser: the uniform 404.
	thief := &browser{s: s, cookie: b.cookie, csrf: b.csrf}
	wantUniform404(t, thief.do(http.MethodGet, "/api/auth/passkeys", nil, nil), "stolen long-lived cookie")
	if sv := thief.session(); sv.Authenticated {
		t.Fatal("stolen cookie authenticated")
	}
	// Past 10 minutes the bound cookie fails until the device refreshes.
	s.advance(dbsc.BoundTTL + time.Second)
	wantUniform404(t, b.do(http.MethodGet, "/api/auth/passkeys", nil, nil), "expired bound cookie")
	if _, code := thief.refresh(dbsctest.NewES256()); code != http.StatusNotFound {
		t.Fatalf("thief refresh: %d", code)
	}
	jwt, code := b.refresh(dev)
	if code != http.StatusOK {
		t.Fatalf("refresh: %d", code)
	}
	want(t, b.do(http.MethodGet, "/api/auth/passkeys", nil, nil), http.StatusOK, "")
	// A replayed refresh proof fails.
	wantUniform404(t, b.do(http.MethodPost, "/api/auth/dbsc/refresh", nil,
		map[string]string{dbsc.HeaderResponse: jwt, "X-PCC-CSRF": ""}), "replayed refresh")
	// The CSRF exemption is the DBSC endpoints' only: logout still needs it,
	// and a foreign Origin is refused there too.
	wantUniform404(t, b.do(http.MethodPost, "/api/auth/logout", nil, map[string]string{"X-PCC-CSRF": ""}), "logout without csrf")
	wantUniform404(t, b.do(http.MethodPost, "/api/auth/dbsc/refresh", nil,
		map[string]string{"X-PCC-CSRF": "", "Origin": "https://evil.example"}), "foreign origin")
	// Logout clears both cookies.
	want(t, b.do(http.MethodPost, "/api/auth/logout", nil, nil), http.StatusNoContent, "")
	if b.cookie != "" || b.bound != "" {
		t.Fatalf("cookies after logout %q %q", b.cookie, b.bound)
	}

	// An RSA-only TPM registers too.
	c, rec := s.login(a)
	want(t, rec, http.StatusOK, "")
	c.register(dbsctest.NewRS256())
	want(t, c.do(http.MethodGet, "/api/auth/passkeys", nil, nil), http.StatusOK, "")
}

func TestDBSCOffAnswersNotFound(t *testing.T) {
	s := newSuite(t)
	b := s.enroll("alice", authtest.New(s.h.Origin))
	if h := b.do(http.MethodGet, "/api/session", nil, nil).Header().Get(dbsc.HeaderRegistration); h != "" {
		t.Fatalf("offer with DBSC off: %q", h)
	}
	wantUniform404(t, b.do(http.MethodPost, "/api/auth/dbsc/register", nil, map[string]string{"X-PCC-CSRF": ""}), "register")
	wantUniform404(t, b.do(http.MethodPost, "/api/auth/dbsc/refresh", nil, map[string]string{"X-PCC-CSRF": ""}), "refresh")
}

// The padded DELETE /auth/passkeys/{id}: a valid ref (409 last_passkey) and a
// garbage one (404) land on the same grid point (server-side histogram and
// client-side durations).
func TestPaddedPasskeyRemovalSameBucket(t *testing.T) {
	s := newSuite(t)
	a := authtest.New(s.h.Origin)
	b := s.enroll("alice", a)
	id := b.passkeys().Items[0].ID
	pad.ResetStats()
	charged := func() int {
		var n int
		if err := s.h.App.Deps().DB.Q().QueryRow(context.Background(),
			"SELECT coalesce(sum(count), 0) FROM rate_counters WHERE bucket = 'w:account'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var found, missing []time.Duration
	var foundCharge, missingCharge int
	for i := range 6 {
		if i%2 == 0 {
			body := b.cosign("passkey_remove", id)
			before := charged()
			start := time.Now()
			want(t, b.do(http.MethodDelete, "/api/auth/passkeys/"+id, body, nil), http.StatusConflict, "last_passkey")
			found = append(found, time.Since(start))
			foundCharge += charged() - before
		} else {
			before := charged()
			start := time.Now()
			wantUniform404(t, b.do(http.MethodDelete, "/api/auth/passkeys/garbage-ref", nil, nil), "garbage ref")
			missing = append(missing, time.Since(start))
			missingCharge += charged() - before
		}
	}
	if err := padtest.SameHistogram(pad.Stats(), "DELETE /auth/passkeys/{id}", 409, 404); err != nil {
		t.Fatal(err)
	}
	if err := padtest.SameBucket(pad.Default, found, missing); err != nil {
		t.Fatal(err)
	}
	// Charging stays uniform under padding (fidelity #7).
	if foundCharge != 3 || missingCharge != 3 {
		t.Fatalf("charges: valid ref %d, garbage %d", foundCharge, missingCharge)
	}
}
