// Package account owns the signed-in user's own surface: Me, settings, export,
// deletion and manual recovery (ARCHITECTURE §10.2, §10.3, §10.7, §2.3).
//
// Owner: P0-W11 → P1-W10. Scaffold stub: Me works (read-only); everything else
// returns gate.ErrNotImplemented after the viewer / freshness checks.
package account

import (
	"context"
	"io"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the account module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers account.delete / recovery.release in W11.
func (s *Service) Init(context.Context) error { return nil }

// Me returns the signed-in user.
func (s *Service) Me(ctx context.Context) (gate.MeView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.MeView{}, err
	}
	m, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.MeView{}, err
	}
	label, err := s.d.Users.InviterLabel(ctx, v.ID)
	if err != nil {
		return gate.MeView{}, err
	}
	return gate.MeView{
		Ref: s.d.Refs.User(v.ID, v.ID), Handle: m.Handle, DisplayName: m.DisplayName, Role: m.Role.String(), Inviter: label,
		Settings: gate.SettingsView{UILocale: m.Locale, Languages: m.Languages, RankPreset: m.Preset.String(),
			NameInChains: m.NameInChains, Suggestible: m.Suggestible, NamedInLikes: m.NamedInLikes,
			SlipperyDefault: m.SlipperyDefault, UIPrefs: []byte("{}")},
	}, nil
}

// UpdateSettings validates and applies a settings patch.
func (s *Service) UpdateSettings(ctx context.Context, p gate.SettingsPatch) (gate.SettingsView, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return gate.SettingsView{}, err
	}
	return gate.SettingsView{}, gate.ErrNotImplemented
}

// Export writes the viewer's export zip (RequireFresh).
func (s *Service) Export(ctx context.Context, w io.Writer) error {
	if _, err := core.RequireFresh(ctx, s.d.Clock.Now()); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// Delete deletes the viewer's account (RequireFresh; confirmHandle must match).
func (s *Service) Delete(ctx context.Context, confirmHandle string) error {
	if _, err := core.RequireFresh(ctx, s.d.Clock.Now()); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// RecoveryRequest opens a manual recovery (moderator 1).
func (s *Service) RecoveryRequest(ctx context.Context, subject gate.Ref) (gate.Ref, error) {
	if _, err := core.RequireModerator(ctx); err != nil {
		return "", err
	}
	return "", gate.ErrNotImplemented
}

// RecoveryApprove approves a recovery (moderator 2 ≠ 1, RequireFresh).
func (s *Service) RecoveryApprove(ctx context.Context, id gate.Ref) error {
	if _, err := core.RequireModerator(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// RecoveryLink returns the enrollment URL after the 24 h hold (moderator 2 only).
func (s *Service) RecoveryLink(ctx context.Context, id gate.Ref) (string, error) {
	if _, err := core.RequireModerator(ctx); err != nil {
		return "", err
	}
	return "", gate.ErrNotImplemented
}

// RecoveryCancel cancels a pending recovery of the viewer's own account.
func (s *Service) RecoveryCancel(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// RecoveryPending returns the release time of a pending recovery (banner), or nil.
func (s *Service) RecoveryPending(ctx context.Context) (*time.Time, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return nil, err
	}
	return nil, nil
}
