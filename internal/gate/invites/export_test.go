package invites

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Redeem runs the RegisterFinish transaction of guest session sess with the
// signup ceremony's assertion.
func (s *Service) Redeem(ctx context.Context, sess [32]byte, assertion []byte) (string, core.UserID, error) {
	return s.redeem(ctx, sess, assertion)
}

// SignupToken computes a signup token for E_v under a handshake secret.
func SignupToken(secret, ev []byte) string { return signupToken(secret, ev) }
