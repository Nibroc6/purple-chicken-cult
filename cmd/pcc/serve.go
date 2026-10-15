package main

import (
	"context"
	"errors"
	"flag"

	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/router"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
	webdist "github.com/Nibroc6/purple-chicken-cult/web"
)

// boot loads the configuration, opens the database and (optionally) migrates.
func boot(ctx context.Context, migrate bool) (*config.Config, *slog.Logger, *store.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	log := logx.New(cfg.LogLevel, nil)
	slog.SetDefault(log)
	if cfg.DatabaseURL == "" {
		return nil, nil, nil, errors.New("PCC_DATABASE_URL is required")
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	if migrate {
		if err := store.Migrate(ctx, db); err != nil {
			db.Close()
			return nil, nil, nil, err
		}
	}
	return cfg, log, db, nil
}

// app boots and wires every service (CLI commands).
func app(ctx context.Context) (*wire.App, *config.Config, func(), error) {
	cfg, log, db, err := boot(ctx, false)
	if err != nil {
		return nil, nil, nil, err
	}
	a, err := wire.Build(ctx, cfg, db, log)
	if err != nil {
		db.Close()
		return nil, nil, nil, err
	}
	return a, cfg, db.Close, nil
}

func serve(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fl.Parse(args); err != nil {
		return err
	}
	cfg, log, db, err := boot(ctx, false)
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.AutoMigrate {
		if err := store.Migrate(ctx, db); err != nil {
			return err
		}
	}
	if cfg.MasterKeyRandom {
		log.Warn("random master key: refs and sessions do not survive a restart", "env", string(cfg.Env))
	}
	if !cfg.MediaEnabled() {
		lvl := slog.LevelInfo
		if cfg.Prod() {
			lvl = slog.LevelError
		}
		log.Log(ctx, lvl, "media disabled (fail closed)", "feature", "media")
	}
	if !cfg.PushEnabled() {
		log.Info("push disabled (no VAPID keys)", "feature", "push")
	}
	a, err := wire.Build(ctx, cfg, db, log)
	if err != nil {
		return err
	}
	if err := a.Start(ctx); err != nil {
		return err
	}
	static := webdist.FS()
	if cfg.StaticDir != "" {
		static = os.DirFS(cfg.StaticDir)
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router.New(api.FromApp(a, cfg, log, static)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.HTTPAddr, "env", string(cfg.Env))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
