// Package limits enforces per-account read budgets and human-pace write limits
// (ARCHITECTURE §17.2(3), §17.4). It implements core.Limiter and apix.Charger
// (ChargeRequest): every request is charged in apix.Route before the handler and
// before any ref is resolved (D19).
//
// Owner: P0-W12 → P1-W07. Scaffold stub: unlimited (coreref.PassLimiter).
package limits

import (
	"context"
	"net/netip"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Read classes (plan §5.1).
const (
	ReadFeedPage  core.ReadClass = "feed_page"
	ReadTreeView  core.ReadClass = "tree_view"
	ReadGraph     core.ReadClass = "graph_read"
	ReadPost      core.ReadClass = "post_read"
	ReadMediaFull core.ReadClass = "media_full" // P1
)

// Write kinds (plan §5.1).
const (
	WritePost      core.WriteKind = "post"
	WriteLike      core.WriteKind = "like"
	WriteFriendReq core.WriteKind = "friend_request"
	WriteReport    core.WriteKind = "report"
	WriteReference core.WriteKind = "reference"
	WriteInvite    core.WriteKind = "invite_handshake"
	WriteSocial    core.WriteKind = "social"
	WriteAccount   core.WriteKind = "account"
	WriteSave      core.WriteKind = "save"  // P1
	WriteMedia     core.WriteKind = "media" // P1
)

// Service is the limits module.
type Service struct {
	d   *core.Deps
	ref coreref.PassLimiter
}

var _ core.Limiter = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers limits.purge in W12.
func (s *Service) Init(context.Context) error { return nil }

// ChargeRequest charges one request of class c to the caller (viewer from ctx;
// unauthenticated ⇒ ChargeAnon by client prefix). Exhaustion ⇒ gate.ErrRateLimited.
func (s *Service) ChargeRequest(ctx context.Context, c gate.Class) error {
	v, ok := core.ViewerFrom(ctx)
	switch c.Kind {
	case gate.ClassFree:
		return nil
	case gate.ClassRead:
		if !ok {
			return nil // unauthenticated reads fail later with the uniform 404
		}
		return s.ChargeRead(ctx, v, core.ReadClass(c.Name))
	case gate.ClassWrite:
		if !ok {
			return nil
		}
		return s.ChargeWrite(ctx, v, core.WriteKind(c.Name), 1)
	case gate.ClassAnon:
		return s.ChargeAnon(netip.Prefix{}, c.Name)
	}
	return nil
}

// ChargeRead charges a read.
func (s *Service) ChargeRead(ctx context.Context, v core.Viewer, c core.ReadClass) error {
	return s.ref.ChargeRead(ctx, v, c)
}

// ChargeWrite charges n writes.
func (s *Service) ChargeWrite(ctx context.Context, v core.Viewer, k core.WriteKind, n int) error {
	return s.ref.ChargeWrite(ctx, v, k, n)
}

// ChargeAnon charges the unauthenticated surface (memory only).
func (s *Service) ChargeAnon(ip netip.Prefix, bucket string) error {
	return s.ref.ChargeAnon(ip, bucket)
}
