// Package logx holds the logging policy (ARCHITECTURE §2.3, plan archtest A11):
// no user IDs, edges, chains or IPs in logs. Attribute keys come from a closed
// allowlist; user IDs appear only as UID(id), an HMAC under a daily salt that is
// derived from the master key and never stored. Request logs carry the route
// pattern, never the URL path.
package logx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

// Keys is the allowlist of slog attribute keys usable in internal/gate and internal/api.
var Keys = map[string]bool{
	"req_id": true, "route": true, "status": true, "dur_ms": true, "code": true, "job": true,
	"uid_h": true, "count": true, "err": true, "method": true, "kind": true, "feature": true,
	"name": true, "bucket": true, "attempt": true, "tier": true, "ms": true, "n": true,
	"env": true, "addr": true, "version": true, "phase": true, "reason": true, "preset": true,
	"planner": true, "lambda": true, "shift_min": true, "credit_min": true, "penalty_min": true,
}

// Allowed reports whether k may be used as a log attribute key.
func Allowed(k string) bool { return Keys[k] }

var saltFn atomic.Value // func(day string) []byte

// SetSalt installs the daily salt source (wire: HKDF(MasterKey, "pcc/log-salt/v1/<day>")).
func SetSalt(fn func(day string) []byte) { saltFn.Store(fn) }

// UID returns the attribute uid_h = hex(HMAC(daily salt, id)[:8]).
func UID(id uint32) slog.Attr {
	fn, _ := saltFn.Load().(func(string) []byte)
	if fn == nil {
		return slog.String("uid_h", "unsalted")
	}
	m := hmac.New(sha256.New, fn(time.Now().UTC().Format("2006-01-02")))
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], id)
	m.Write(b[:])
	return slog.String("uid_h", hex.EncodeToString(m.Sum(nil)[:8]))
}

// Route returns the chi route pattern of r (never the raw path).
func Route(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}

// New returns a JSON logger at level writing to w (os.Stderr when nil).
func New(level slog.Level, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
