package safefetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// recorder replaces DNS and the network: names resolve from a table and every
// dial is recorded and refused, so no test leaves the machine.
type recorder struct {
	mu     sync.Mutex
	names  map[string][]string
	calls  map[string]int
	dialed []string
}

func (r *recorder) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	answers, ok := r.names[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	i := min(r.calls[host], len(answers)-1)
	r.calls[host]++
	return []netip.Addr{netip.MustParseAddr(answers[i])}, nil
}

func (r *recorder) dial(_ context.Context, _, addr string) (net.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dialed = append(r.dialed, addr)
	return nil, errors.New("test: network disabled")
}

func (r *recorder) dials() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dialed...)
}

func offline(names map[string][]string) (*Client, *recorder) {
	r := &recorder{names: names}
	c := newForTest(false)
	c.resolve, c.dial = r.resolve, r.dial
	return c, r
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestSSRFTable: every address class the plan names is refused without a
// single connection attempt.
func TestSSRFTable(t *testing.T) {
	c, r := offline(map[string][]string{
		"private.example":   {"10.1.2.3"},
		"cgnat.example":     {"100.64.0.1"},
		"meta.example":      {"169.254.169.254"},
		"ula.example":       {"fd00::1"},
		"mapped.example":    {"::ffff:127.0.0.1"},
		"nat64.example":     {"64:ff9b::7f00:1"},
		"sixtofour.example": {"2002:7f00:1::"},
		"teredo.example":    {"2001::1"},
		"linklocal.example": {"fe80::1"},
		"bench.example":     {"198.18.0.1"},
	})
	for _, raw := range []string{
		"http://127.0.0.1/", "http://127.1.2.3/", "http://[::1]/", "http://10.0.0.1/", "http://10.255.255.255/",
		"http://172.16.0.1/", "http://172.31.255.255/", "http://192.168.1.1/", "http://100.64.0.1/",
		"http://169.254.169.254/latest/meta-data/", "http://[fd00::1]/", "http://0.0.0.0/", "http://0.1.2.3/",
		"http://198.18.0.1/", "http://198.19.255.1/", "http://240.0.0.1/", "http://255.255.255.255/",
		"http://224.0.0.1/", "http://[::ffff:127.0.0.1]/", "http://[::ffff:7f00:1]/", "http://[64:ff9b::7f00:1]/",
		"http://[2002:7f00:1::]/", "http://[fe80::1]/", "http://[fe80::1%25eth0]/", "http://[::]/", "http://[ff02::1]/",
		"http://[::127.0.0.1]/", "http://[2001::1]/", "http://[fec0::1]/", "http://192.0.0.8/",
		// decimal, octal, hex and short IPv4 forms
		"http://2130706433/", "http://017700000001/", "http://0177.0.0.1/", "http://0x7f.0.0.1/", "http://0x7f000001/",
		"http://127.1/", "http://10.1/",
		// names
		"http://localhost/", "http://LOCALHOST./", "http://foo.localhost/", "http://metadata.google.internal/",
		"http://metadata/", "http://instance-data/", "http://printer.local/", "http://intranet/",
		"http://private.example/", "http://cgnat.example/", "http://meta.example/", "http://ula.example/",
		"http://mapped.example/", "http://nat64.example/", "http://sixtofour.example/", "http://teredo.example/",
		"http://linklocal.example/", "http://bench.example/",
		// ports, userinfo, schemes
		"http://example.com:8080/", "https://example.com:8443/", "http://example.com:443/", "https://example.com:80/",
		"http://user@example.com/", "http://user:pass@example.com/", "file:///etc/passwd", "gopher://example.com/",
		"ftp://example.com/", "javascript:alert(1)",
	} {
		_, err := c.GetHTML(context.Background(), mustURL(t, raw))
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v, want blocked", raw, err)
		}
	}
	if d := r.dials(); len(d) != 0 {
		t.Fatalf("dialed %v", d)
	}
}

// TestDNSRebinding: the address that was checked is the address dialed; a
// later answer pointing inside is refused before any dial.
func TestDNSRebinding(t *testing.T) {
	c, r := offline(map[string][]string{"rebind.example": {"93.184.216.34", "127.0.0.1"}})
	_, _ = c.GetHTML(context.Background(), mustURL(t, "http://rebind.example/"))
	_, err := c.GetHTML(context.Background(), mustURL(t, "http://rebind.example/"))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("second fetch: %v", err)
	}
	if d := r.dials(); len(d) != 1 || d[0] != "93.184.216.34:80" {
		t.Fatalf("dialed %v", d)
	}
	// The socket check catches an address that slipped past resolution.
	if err := c.checkSocket("tcp", "127.0.0.1:80", nil); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if err := c.checkSocket("tcp", "[::ffff:10.0.0.1]:443", nil); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if err := c.checkSocket("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatal(err)
	}
}

// TestMixedAnswersBlocked: one private address in the answer blocks the host.
func TestMixedAnswersBlocked(t *testing.T) {
	r := &recorder{}
	c := newForTest(false)
	c.dial = r.dial
	c.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, nil
	}
	if _, err := c.GetHTML(context.Background(), mustURL(t, "http://mixed.example/")); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if len(r.dials()) != 0 {
		t.Fatal(r.dials())
	}
}

// loopback serves h on 127.0.0.1 for a client that may reach loopback.
func loopback(t *testing.T, h http.HandlerFunc) (*Client, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return newForTest(true), srv.URL
}

func TestFetchHTML(t *testing.T) {
	var ua, accept atomic.Value
	c, base := loopback(t, func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		accept.Store(r.Header.Get("Accept"))
		if _, err := r.Cookie("x"); err == nil {
			t.Error("a cookie was sent")
		}
		http.SetCookie(w, &http.Cookie{Name: "x", Value: "1"})
		w.Header().Set("Content-Type", "text/html; charset=ISO-8859-1")
		_, _ = fmt.Fprint(w, "<title>ok</title>")
	})
	res, err := c.GetHTML(context.Background(), mustURL(t, base+"/a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "<title>ok</title>" || res.ContentType != "text/html" || res.Charset != "ISO-8859-1" || res.Truncated {
		t.Fatalf("%+v", res)
	}
	if ua.Load() != UserAgent || accept.Load() != "text/html" {
		t.Fatal(ua.Load(), accept.Load())
	}
	// No cookie jar: the second request carries no cookie.
	if _, err := c.GetHTML(context.Background(), mustURL(t, base+"/b")); err != nil {
		t.Fatal(err)
	}
}

func TestRedirects(t *testing.T) {
	var hops atomic.Int32
	c, base := loopback(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/private":
			http.Redirect(w, r, "http://10.0.0.1/", http.StatusFound)
		case "/meta":
			http.Redirect(w, r, "http://169.254.169.254/latest/", http.StatusMovedPermanently)
		case "/userinfo":
			http.Redirect(w, r, "http://u:p@example.com/", http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/port":
			http.Redirect(w, r, "http://example.com:8080/", http.StatusTemporaryRedirect)
		case "/final":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "done")
		default: // /loop/n
			n := hops.Add(1)
			if strings.HasPrefix(r.URL.Path, "/three/") && n >= 3 {
				http.Redirect(w, r, "/final", http.StatusFound)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/%s/%d", strings.Split(r.URL.Path, "/")[1], n), http.StatusFound)
		}
	})
	for _, p := range []string{"/private", "/meta", "/userinfo", "/file", "/port"} {
		if _, err := c.GetHTML(context.Background(), mustURL(t, base+p)); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := c.GetHTML(context.Background(), mustURL(t, base+"/loop/0")); !errors.Is(err, ErrRedirects) {
		t.Fatalf("loop: %v", err)
	}
	// Exactly three redirects are fine.
	hops.Store(0)
	res, err := c.GetHTML(context.Background(), mustURL(t, base+"/three/0"))
	if err != nil {
		t.Fatalf("three: %v", err)
	}
	if res.FinalURL.Path != "/final" {
		t.Fatal(res.FinalURL)
	}
}

// TestGzipBombCutOff: the cap is on the decompressed stream.
func TestGzipBombCutOff(t *testing.T) {
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	_, _ = zw.Write([]byte("<html><head><title>bomb</title></head><body>"))
	zero := make([]byte, 1<<20)
	for range 10 {
		_, _ = zw.Write(zero)
	}
	_ = zw.Close()
	if gz.Len() > 12<<10 {
		t.Fatalf("bomb is %d bytes compressed", gz.Len())
	}
	c, base := loopback(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Error("no gzip asked")
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	})
	res, err := c.GetHTML(context.Background(), mustURL(t, base+"/"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Body) != MaxHTML || !res.Truncated || !bytes.HasPrefix(res.Body, []byte("<html><head><title>bomb")) {
		t.Fatalf("%d bytes, truncated=%v", len(res.Body), res.Truncated)
	}
	// Images are all-or-nothing.
	img, base2 := loopback(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	})
	if _, err := img.GetImage(context.Background(), mustURL(t, base2+"/"), 64<<10); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
}

func TestTypesAndStatus(t *testing.T) {
	c, base := loopback(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
		case "/xhtml":
			w.Header().Set("Content-Type", "application/xhtml+xml")
		case "/none":
			w.Header()["Content-Type"] = nil
		case "/br":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "br")
		case "/404":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
		}
		_, _ = w.Write([]byte("{}"))
	})
	for p, want := range map[string]error{"/json": ErrType, "/xhtml": ErrType, "/none": ErrType, "/br": ErrType, "/404": ErrStatus} {
		if _, err := c.GetHTML(context.Background(), mustURL(t, base+p)); !errors.Is(err, want) {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// TestImageTypes: GetImage takes raster images only (never SVG or HTML), with
// the same SSRF rules as GetHTML (a private address is refused before any
// request).
func TestImageTypes(t *testing.T) {
	var accept atomic.Value
	c, base := loopback(t, func(w http.ResponseWriter, r *http.Request) {
		accept.Store(r.Header.Get("Accept"))
		w.Header().Set("Content-Type", strings.TrimPrefix(r.URL.Path, "/t/"))
		_, _ = w.Write([]byte("img"))
	})
	for mt, ok := range map[string]bool{
		"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
		"image/svg+xml": false, "image/x-icon": false, "image/tiff": false, "text/html": false, "application/octet-stream": false,
	} {
		res, err := c.GetImage(context.Background(), mustURL(t, base+"/t/"+mt), 1<<10)
		if ok && (err != nil || string(res.Body) != "img" || res.ContentType != mt) {
			t.Errorf("%s: %v", mt, err)
		}
		if !ok && !errors.Is(err, ErrType) {
			t.Errorf("%s: %v, want ErrType", mt, err)
		}
	}
	if a, _ := accept.Load().(string); a != "image/*" {
		t.Errorf("Accept %q", a)
	}
	if _, err := New().GetImage(context.Background(), mustURL(t, "http://10.0.0.1/a.png"), 1<<10); !errors.Is(err, ErrBlocked) {
		t.Errorf("private address: %v", err)
	}
}

func TestDeadline(t *testing.T) {
	c, base := loopback(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select { // a slow-drip body
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	c.timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := c.GetHTML(context.Background(), mustURL(t, base+"/"))
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	if Timeout != 3*time.Second {
		t.Fatal("the production deadline is 3 s")
	}
}

// TestProxyEnvIgnored: HTTP_PROXY / HTTPS_PROXY never route a fetch.
func TestProxyEnvIgnored(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	c, base := loopback(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("direct"))
	})
	res, err := c.GetHTML(context.Background(), mustURL(t, base+"/"))
	if err != nil || string(res.Body) != "direct" || proxied.Load() != 0 {
		t.Fatalf("%v %v %d", res, err, proxied.Load())
	}
	// An offline client with the proxy set still refuses private targets
	// itself instead of handing them to the proxy.
	off, r := offline(nil)
	if _, err := off.GetHTML(context.Background(), mustURL(t, "http://10.0.0.1/")); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if proxied.Load() != 0 || len(r.dials()) != 0 {
		t.Fatal("proxy or network used")
	}
}

// TestProductionClientRefusesLoopback: New and ForConfig outside test hooks
// never reach a local server.
func TestProductionClientRefusesLoopback(t *testing.T) {
	var hit atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Add(1) }))
	defer srv.Close()
	port := srv.URL[strings.LastIndexByte(srv.URL, ':')+1:]
	for _, c := range []*Client{New(), ForConfig(&config.Config{Env: config.EnvProd}), ForConfig(&config.Config{Env: config.EnvTest}),
		ForConfig(&config.Config{Env: config.EnvDev, TestHooks: true}), ForConfig(nil)} {
		for _, raw := range []string{srv.URL, "http://127.0.0.1/", "http://[::1]:" + port + "/"} {
			if _, err := c.GetHTML(context.Background(), mustURL(t, raw)); !errors.Is(err, ErrBlocked) {
				t.Errorf("%s: %v", raw, err)
			}
		}
	}
	if hit.Load() != 0 {
		t.Fatal("the local server was reached")
	}
	if !ForConfig(&config.Config{Env: config.EnvTest, TestHooks: true}).allowLoopback {
		t.Fatal("test hooks allow loopback for e2e")
	}
	// Even the loopback-allowed client keeps the rest of the policy.
	lc := newForTest(true)
	for _, raw := range []string{"http://10.0.0.1/", "http://169.254.169.254/", "http://localhost/", "http://example.com:8080/"} {
		if _, err := lc.GetHTML(context.Background(), mustURL(t, raw)); !errors.Is(err, ErrBlocked) {
			t.Errorf("loopback client %s: %v", raw, err)
		}
	}
}

func TestPublicAddressesAllowed(t *testing.T) {
	c := newForTest(false)
	for _, s := range []string{"93.184.216.34", "1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "172.32.0.1", "100.128.0.1", "11.0.0.1"} {
		if c.blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s blocked", s)
		}
	}
	for _, h := range []string{"example.com", "a.b.example", "1.example", "x1"} {
		if numericHost(h) {
			t.Errorf("%s numeric", h)
		}
	}
}
