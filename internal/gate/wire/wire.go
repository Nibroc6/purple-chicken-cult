// Package wire constructs every gate service and late-binds core.Deps (plan §4.6).
// It is the only package (with conformance) that imports every domain package.
//
// Order: core.Deps base → New() every service with *core.Deps → fill the
// interface fields of Deps → each service's Init (jobs, Bus subscriptions, caches,
// Graph.Load, PostIndex.Load) → return.
//
// Owner: SCAFFOLD (contract steward during phases). SCAFFOLD-1 added the P1 fields.
package wire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/account"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/cosign"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/devseed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/engage"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/feed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/graph"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/limits"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/metrics"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/moderation"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/notify"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/postindex"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/previews"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/signals/automation"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/signals/behavior"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/tree"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/users"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/views"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/visibility"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/refs"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/jobq"
)

// App holds every gate service.
type App struct {
	Auth       *auth.Service
	Users      *users.Service
	Graph      *graph.Graph
	Visibility *visibility.Service
	Views      *views.Service
	Friends    *friends.Service
	Invites    *invites.Service
	Posts      *posts.Service
	PostIndex  *postindex.Index
	Engage     *engage.Service
	Feed       *feed.Service
	Tree       *tree.Service
	Previews   *previews.Service
	Notify     *notify.Service
	Metrics    *metrics.Service
	Records    *records.Service
	Account    *account.Service
	Limits     *limits.Service
	Moderation *moderation.Service
	DevSeed    *devseed.Service

	// Phase 1 (SCAFFOLD-1).
	Cosign     *cosign.Service
	Media      *media.Service
	Themes     *themes.Service
	Behavior   *behavior.Service
	Automation *automation.Service

	d     *core.Deps
	hooks *Hooks
	fake  *clock.Fake
}

// Options adjust Build (tests).
type Options struct {
	Clock     clock.Clock // default: clock.System (clock.Fake when test hooks are on)
	IDs       ids.Gen     // default: ids.V7
	ServerKey *sigs.Key   // default: PCC_SERVER_KEY_FILE
}

// Option adjusts Options.
type Option func(*Options)

// WithClock sets the clock.
func WithClock(c clock.Clock) Option { return func(o *Options) { o.Clock = c } }

// WithIDs sets the ID generator.
func WithIDs(g ids.Gen) Option { return func(o *Options) { o.IDs = g } }

// WithServerKey sets the server signing key.
func WithServerKey(k *sigs.Key) Option { return func(o *Options) { o.ServerKey = k } }

// DerivedKeys derives every symmetric application key from the master key.
func DerivedKeys(cfg *config.Config) core.DerivedKeys {
	return core.DerivedKeys{
		CSRF:        cfg.Key("pcc/csrf/v1", 32),
		Cursor:      cfg.Key("pcc/cursor/v1", 32),
		Pseudonym:   cfg.Key("pcc/pseudonym/v1", 32),
		MediaURL:    cfg.Key("pcc/media-url/v1", 32),
		LogSalt:     func(day string) []byte { return cfg.Key("pcc/log-salt/v1/"+day, 32) },
		MetricsSalt: func(day string) []byte { return cfg.Key("pcc/metrics-salt/v1/"+day, 32) },
	}
}

// Build constructs and initializes every service.
func Build(ctx context.Context, cfg *config.Config, db *store.DB, log *slog.Logger, opts ...Option) (*App, error) {
	if cfg == nil || db == nil {
		return nil, errors.New("wire: config and database are required")
	}
	if log == nil {
		log = slog.Default()
	}
	o := Options{}
	for _, f := range opts {
		f(&o)
	}
	a := &App{}
	testHooks := cfg.Env == config.EnvTest && cfg.TestHooks
	if o.Clock == nil {
		if testHooks {
			a.fake = clock.NewFake(time.Now().UTC())
			o.Clock = a.fake
		} else {
			o.Clock = clock.System{}
		}
	} else if f, ok := o.Clock.(*clock.Fake); ok {
		a.fake = f
	}
	if o.IDs == nil {
		o.IDs = ids.V7{}
	}
	codec, err := refs.New(map[byte][]byte{1: cfg.Key("pcc/ref/v1", refs.KeySize)}, 1)
	if err != nil {
		return nil, fmt.Errorf("wire: refs: %w", err)
	}
	keys := DerivedKeys(cfg)
	logx.SetSalt(keys.LogSalt)

	d := &core.Deps{
		DB: db, Jobs: jobq.New(db, o.Clock, log), Clock: o.Clock, IDs: o.IDs, Refs: core.Refs{C: codec},
		Cfg: cfg, Log: log, Bus: &core.Bus{}, Keys: keys,
	}
	a.d = d

	a.Auth = auth.New(d)
	a.Users = users.New(d)
	a.Graph = graph.New(d)
	a.Visibility = visibility.New(d)
	a.Views = views.New(d)
	a.Friends = friends.New(d)
	a.Invites = invites.New(d)
	a.Posts = posts.New(d)
	a.PostIndex = postindex.New(d)
	a.Engage = engage.New(d)
	a.Feed = feed.New(d)
	a.Tree = tree.New(d)
	a.Previews = previews.New(d)
	a.Notify = notify.New(d)
	a.Metrics = metrics.New(d)
	a.Records = records.New(d)
	a.Account = account.New(d)
	a.Limits = limits.New(d)
	a.Moderation = moderation.New(d)
	a.DevSeed = devseed.New(d)
	a.Cosign = cosign.New(d)
	a.Media = media.New(d)
	a.Themes = themes.New(d)
	a.Behavior = behavior.New(d)
	a.Automation = automation.New(d)
	if o.ServerKey != nil {
		a.Records.SetKey(o.ServerKey)
	}

	d.Users = a.Users
	d.Graph = a.Graph
	d.Edges = a.Friends
	d.Mutes = a.Friends
	d.Posts = a.PostIndex
	d.Vis = a.Visibility
	d.Chains = a.Visibility
	d.Engage = a.Engage
	d.Prefer = a.Engage
	d.Views = a.Views
	d.Signer = a.Records
	d.Passkeys = a.Auth
	d.Invites = a.Invites
	d.Probation = a.Invites
	d.Notify = a.Notify
	d.Limits = a.Limits
	d.Metrics = a.Metrics
	d.Cosign = a.Cosign
	d.Handshakes = a.Invites
	d.Media = a.Media
	d.ModQueue = a.Moderation
	d.StepUp = a.Auth
	d.DisplayNames = a.Users
	d.RecordExporter = a.Records
	d.PostAdmin = a.Posts
	d.Enrollments = a.Auth
	d.Accounts = a.Users
	d.Handles = a.Users
	d.Requests = a.Friends
	d.Mentions = a.PostIndex

	inits := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"records", a.Records.Init}, {"users", a.Users.Init}, {"auth", a.Auth.Init}, {"graph", a.Graph.Init},
		{"postindex", a.PostIndex.Init}, {"visibility", a.Visibility.Init}, {"views", a.Views.Init},
		{"friends", a.Friends.Init}, {"invites", a.Invites.Init}, {"posts", a.Posts.Init}, {"engage", a.Engage.Init},
		{"feed", a.Feed.Init}, {"tree", a.Tree.Init}, {"previews", a.Previews.Init}, {"notify", a.Notify.Init},
		{"metrics", a.Metrics.Init}, {"account", a.Account.Init}, {"limits", a.Limits.Init},
		{"moderation", a.Moderation.Init}, {"devseed", a.DevSeed.Init}, {"cosign", a.Cosign.Init},
		{"media", a.Media.Init}, {"themes", a.Themes.Init}, {"behavior", a.Behavior.Init},
		{"automation", a.Automation.Init},
	}
	for _, in := range inits {
		if err := in.fn(ctx); err != nil {
			return nil, fmt.Errorf("wire: init %s: %w", in.name, err)
		}
	}
	if testHooks {
		a.hooks = &Hooks{a: a}
	}
	return a, nil
}

// Start runs the job workers until ctx ends (the SSE hub and metrics flusher
// are job/Bus driven).
func (a *App) Start(ctx context.Context) error {
	go func() {
		if err := a.d.Jobs.Run(ctx, 2); err != nil && !errors.Is(err, context.Canceled) {
			a.d.Log.Error("jobs stopped", "err", err)
		}
	}()
	return nil
}

// Hooks returns the runtime test hooks, non-nil only when cfg.Env == "test" and
// PCC_TEST_HOOKS=1 (Build then uses a clock.Fake).
func (a *App) Hooks() *Hooks { return a.hooks }

// DrainJobs runs all due jobs synchronously (tests).
func (a *App) DrainJobs(ctx context.Context) error { return a.d.Jobs.Drain(ctx) }

// Deps returns the late-bound dependency container (gate-internal tests and
// wiretest only; handlers can never name core types).
func (a *App) Deps() *core.Deps { return a.d }

// Config returns the configuration the App was built with.
func (a *App) Config() *config.Config { return a.d.Cfg }
