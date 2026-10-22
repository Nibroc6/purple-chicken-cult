// Package api holds the HTTP layer's dependency container. Handlers live in
// internal/api/<module>api and talk ONLY to gate services (ARCHITECTURE §13):
// they never import internal/store, pgx, platform/refs or platform/sigs, and they
// can never name a core type (archtest A2, A5).
//
// Owner: SCAFFOLD (contract steward during phases).
package api

import (
	"io/fs"
	"log/slog"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/account"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/cosign"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/dm"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/engage"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/feed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/limits"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/moderation"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/notify"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/signals/automation"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/tree"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Deps are the concrete services handlers may call.
type Deps struct {
	Cfg    *config.Config
	Log    *slog.Logger
	Static fs.FS       // the SPA (web/dist or PCC_STATIC_DIR)
	Hooks  *wire.Hooks // non-nil only when PCC_ENV=test and PCC_TEST_HOOKS=1

	Auth       *auth.Service
	Friends    *friends.Service
	Invites    *invites.Service
	Posts      *posts.Service
	Engage     *engage.Service
	Feed       *feed.Service
	Tree       *tree.Service
	Notify     *notify.Service
	Records    *records.Service
	Account    *account.Service
	Limits     *limits.Service
	Moderation *moderation.Service

	// Phase 1 (SCAFFOLD-1).
	Media      *media.Service
	Automation *automation.Service
	Cosign     *cosign.Service
	Themes     *themes.Service

	// Direct messages (contract request dm-1).
	DM *dm.Service
}

// FromApp builds Deps from a wired App (cmd/pcc serve, apitest).
func FromApp(a *wire.App, cfg *config.Config, log *slog.Logger, static fs.FS) *Deps {
	return &Deps{
		Cfg: cfg, Log: log, Static: static, Hooks: a.Hooks(),
		Auth: a.Auth, Friends: a.Friends, Invites: a.Invites, Posts: a.Posts, Engage: a.Engage, Feed: a.Feed,
		Tree: a.Tree, Notify: a.Notify, Records: a.Records, Account: a.Account, Limits: a.Limits,
		Moderation: a.Moderation, Media: a.Media, Automation: a.Automation, Cosign: a.Cosign, Themes: a.Themes, DM: a.DM,
	}
}
