// Package account owns the signed-in user's own surface: Me, settings, export,
// deletion and manual recovery (ARCHITECTURE §10.2, §10.3, §10.7, §2.3). It is
// the writer of the users settings/status/deletion columns, account_stubs,
// recovery_requests and recovery_approvals.
//
// Owner: P0-W11 → P1-W10.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Job kinds registered by the account module (plan §4.3).
const (
	JobDelete  = "account.delete"
	JobRelease = "recovery.release"
)

// ReleaseEvery is how often the recovery release job runs.
const ReleaseEvery = 10 * time.Minute

// Service is the account module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers the account.delete and recovery.release jobs.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs == nil {
		return nil
	}
	s.d.Jobs.Handle(JobDelete, func(ctx context.Context, payload json.RawMessage) error {
		var p deletePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		return s.runDelete(ctx, core.UserID(p.User))
	})
	s.d.Jobs.Handle(JobRelease, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.releaseDue(ctx)
		return err
	})
	s.d.Jobs.Every(JobRelease, ReleaseEvery)
	return nil
}

// ---- local adapters over core (contract requests p0-w11-1 and p0-w11-2) ----

// displayNames is implemented by gate/users (core.Users.CleanDisplayName once
// docs/contract-requests/p0-w11-1.md lands).
type displayNames interface {
	CleanDisplayName(name string) (string, bool)
}

// recordExporter is implemented by gate/records (core.Signer.ExportRecords once
// docs/contract-requests/p0-w11-1.md lands).
type recordExporter interface {
	ExportRecords(ctx context.Context, q store.Q, user core.UserID) (recordsJSONL, keysJSON []byte, err error)
}

var errNoAdapter = errors.New("account: a dependency lacks a required capability")

// ---- Me ----

// settingsRow is the settings part of a users row.
type settingsRow struct {
	handle, displayName string
	status              int16
	locale              string
	languages           []string
	translateTo         *string
	autotranslate       bool
	preset              int16
	nameInChains        bool
	suggestible         bool
	namedInLikes        bool
	slippery            bool
	uiPrefs             []byte
}

func loadSettings(ctx context.Context, q store.Q, id core.UserID, forUpdate bool) (settingsRow, error) {
	sql := `SELECT handle, display_name, status, ui_locale, languages, translate_to, autotranslate, rank_preset,
		name_in_chains, suggestible, named_in_likes, slippery_default, ui_prefs FROM users WHERE id = $1`
	if forUpdate {
		sql = `SELECT handle, display_name, status, ui_locale, languages, translate_to, autotranslate, rank_preset,
		name_in_chains, suggestible, named_in_likes, slippery_default, ui_prefs FROM users WHERE id = $1 FOR UPDATE`
	}
	var r settingsRow
	err := q.QueryRow(ctx, sql, int64(id)).Scan(&r.handle, &r.displayName, &r.status, &r.locale, &r.languages,
		&r.translateTo, &r.autotranslate, &r.preset, &r.nameInChains, &r.suggestible, &r.namedInLikes, &r.slippery,
		&r.uiPrefs)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, gate.ErrNotFound
	}
	return r, err
}

func (r settingsRow) view() gate.SettingsView {
	v := gate.SettingsView{UILocale: r.locale, Languages: r.languages, Autotranslate: r.autotranslate,
		RankPreset: core.Preset(r.preset).String(), NameInChains: r.nameInChains, Suggestible: r.suggestible,
		NamedInLikes: r.namedInLikes, SlipperyDefault: r.slippery, UIPrefs: json.RawMessage(r.uiPrefs)}
	if v.Languages == nil {
		v.Languages = []string{}
	}
	if len(v.UIPrefs) == 0 {
		v.UIPrefs = json.RawMessage("{}")
	}
	if r.translateTo != nil {
		v.TranslateTo = *r.translateTo
	}
	return v
}

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
	if m.Status == core.StatusDeleted {
		return gate.MeView{}, gate.ErrNotFound
	}
	row, err := loadSettings(ctx, s.d.DB.Q(), v.ID, false)
	if err != nil {
		return gate.MeView{}, err
	}
	label, err := s.d.Users.InviterLabel(ctx, v.ID)
	if err != nil {
		return gate.MeView{}, err
	}
	return gate.MeView{
		Ref: s.d.Refs.User(v.ID, v.ID), Handle: row.handle, DisplayName: row.displayName, Role: m.Role.String(),
		Inviter: label, Settings: row.view(),
	}, nil
}

// Export writes the viewer's export zip (fresh step-up): records.jsonl,
// keys.json, posts.jsonl, credentials.jsonl and manifest.json (§10.2).
func (s *Service) Export(ctx context.Context, w io.Writer) error {
	v, err := core.RequireFresh(ctx, s.d.Clock.Now())
	if err != nil {
		return err
	}
	return s.writeExport(ctx, v.ID, w)
}
