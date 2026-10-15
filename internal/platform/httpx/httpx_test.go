package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestSessionCookieHeader(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "tok", 3600, true)
	got := w.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-pcc=tok", "Path=/", "Max-Age=3600", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(got, want) {
			t.Errorf("cookie %q lacks %q", got, want)
		}
	}
	w = httptest.NewRecorder()
	SetSessionCookie(w, "tok", 60, false)
	got = w.Header().Get("Set-Cookie")
	if !strings.HasPrefix(got, "pcc_dev=tok") || !strings.Contains(got, "SameSite=Strict") || strings.Contains(got, "Secure") {
		t.Errorf("dev cookie: %q", got)
	}
}

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	var seen netip.Addr
	h := RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = ClientIP(r) }))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.String() != "203.0.113.5" {
		t.Fatalf("untrusted peer must not be able to spoof: %s", seen)
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.1.1:1234"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 5.6.7.8, 10.2.2.2")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.String() != "5.6.7.8" {
		t.Fatalf("right-most untrusted hop expected, got %s", seen)
	}

	r.Header.Set("CF-Connecting-IP", "8.8.4.4")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen.String() != "8.8.4.4" {
		t.Fatalf("CF-Connecting-IP from trusted peer expected, got %s", seen)
	}
}

func TestClientPrefix(t *testing.T) {
	if p := ClientPrefix(netip.MustParseAddr("1.2.3.4")); p.String() != "1.2.3.0/24" {
		t.Fatal(p)
	}
	if p := ClientPrefix(netip.MustParseAddr("2001:db8:1:2:3::1")); p.Bits() != 56 {
		t.Fatal(p)
	}
}
