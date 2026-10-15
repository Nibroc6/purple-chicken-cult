package invites

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Redeem runs the RegisterFinish transaction for an already verified credential.
func (s *Service) Redeem(ctx context.Context, sess [32]byte, cred core.Credential) (string, core.UserID, error) {
	return s.redeem(ctx, sess, cred)
}

// SAS computes the short authentication string.
func SAS(secret, session []byte) string { return sas(secret, session) }

// FramePayload builds the frame payload of hs in the window of t.
func FramePayload(secret []byte, hs uuid.UUID, t time.Time) string {
	return framePayload(secret, hs, window(t))
}
