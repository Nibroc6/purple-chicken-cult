// Package httpx holds HTTP plumbing with no domain knowledge: request IDs, panic
// recovery, trusted-proxy client IPs, body limits, session cookies and the access
// log. JSON helpers live in internal/api/apix.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
)

type ctxKey int

const (
	keyReqID ctxKey = iota
	keyIP
)

// Cookie names: "__Host-" requires Secure, so dev over http://localhost uses pcc_dev.
const (
	CookieProd = "__Host-pcc"
	CookieDev  = "pcc_dev"
)

// CookieName returns the session cookie name for the transport.
func CookieName(secure bool) string {
	if secure {
		return CookieProd
	}
	return CookieDev
}

// RequestID assigns a random request ID (ctx + X-Request-ID response header).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [8]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyReqID, id)))
	})
}

// ReqID returns the request ID from ctx.
func ReqID(ctx context.Context) string {
	s, _ := ctx.Value(keyReqID).(string)
	return s
}

// Recover turns panics into a fixed 500 body and logs the stack.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as documented by net/http
					panic(v)
				}
				slog.Error("panic", "req_id", ReqID(r.Context()), "route", logx.Route(r), "err", v, "stack", string(debug.Stack()))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RealIP stores the client address in ctx. CF-Connecting-IP and X-Forwarded-For are
// honored ONLY when the TCP peer is inside trusted; otherwise RemoteAddr is used.
// For X-Forwarded-For the right-most hop that is not itself trusted wins.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerAddr(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip) {
				if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
					ip = cf.Unmap()
				} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					hops := strings.Split(xff, ",")
					for i := len(hops) - 1; i >= 0; i-- {
						a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
						if err != nil {
							break
						}
						ip = a.Unmap()
						if !isTrusted(ip) {
							break
						}
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyIP, ip)))
		})
	}
}

func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// ClientIP returns the address computed by RealIP (or the TCP peer when RealIP
// did not run).
func ClientIP(r *http.Request) netip.Addr {
	if a, ok := r.Context().Value(keyIP).(netip.Addr); ok {
		return a
	}
	return peerAddr(r.RemoteAddr)
}

// ClientIPFrom returns the address stored by RealIP in ctx.
func ClientIPFrom(ctx context.Context) (netip.Addr, bool) {
	a, ok := ctx.Value(keyIP).(netip.Addr)
	return a, ok && a.IsValid()
}

// ClientPrefix returns the /24 (IPv4) or /56 (IPv6) prefix used by anonymous limits.
func ClientPrefix(a netip.Addr) netip.Prefix {
	bits := 56
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return netip.Prefix{}
	}
	return p
}

// MaxBytes caps the request body at n bytes.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// SetSessionCookie sets the session cookie: always HttpOnly; SameSite=Strict;
// Path=/, and Secure (with the __Host- name) when secure.
func SetSessionCookie(w http.ResponseWriter, token string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the origin is https (dev uses http://localhost)
		Name: CookieName(secure), Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the origin is https (dev uses http://localhost)
		Name: CookieName(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush supports streaming responses (SSE).
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs route pattern, status and duration only: no path, query or IP.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		slog.Info("http", "req_id", ReqID(r.Context()), "method", r.Method, "route", logx.Route(r),
			"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
	})
}
