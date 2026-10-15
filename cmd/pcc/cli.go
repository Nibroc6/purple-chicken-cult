package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

func migrateCmd(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("migrate", flag.ContinueOnError)
	if err := fl.Parse(args); err != nil {
		return err
	}
	_, _, db, err := boot(ctx, false)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		return err
	}
	fmt.Println("migrations applied")
	return nil
}

func genesisCmd(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("genesis", flag.ContinueOnError)
	handle := fl.String("handle", "", "handle of the genesis user")
	name := fl.String("name", "", "display name")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *handle == "" {
		return errors.New("--handle is required")
	}
	a, _, closeDB, err := app(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	u, err := a.Auth.SeedGenesis(ctx, *handle, *name)
	if err != nil {
		return err
	}
	fmt.Println(u)
	fmt.Fprintln(os.Stderr, "one-time enrollment URL (valid 24 h); deliver it out of band")
	return nil
}

func modCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "grant" {
		return errors.New("usage: pcc mod grant --handle H (--by ADMIN | --bootstrap)")
	}
	fl := flag.NewFlagSet("mod grant", flag.ContinueOnError)
	handle := fl.String("handle", "", "user to grant")
	by := fl.String("by", "", "existing admin handle")
	bootstrap := fl.Bool("bootstrap", false, "make the first admin (only when no admin exists)")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	if *handle == "" || (*by == "" && !*bootstrap) {
		return errors.New("--handle and one of --by / --bootstrap are required")
	}
	a, _, closeDB, err := app(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	if err := a.Moderation.GrantModerator(ctx, *handle, *by, *bootstrap); err != nil {
		return err
	}
	fmt.Println("granted")
	return nil
}

func keysCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pcc keys rotate|fingerprints")
	}
	a, _, closeDB, err := app(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	switch args[0] {
	case "rotate":
		fp, err := a.Records.RotateServerKey(ctx)
		if err != nil {
			return err
		}
		fmt.Println(fp)
		return nil
	case "fingerprints":
		keys, err := a.Records.Keys(ctx)
		if err != nil {
			return err
		}
		for _, k := range keys {
			fmt.Printf("%s  %s\n", k.KeyID, k.Fingerprint)
		}
		return nil
	}
	return fmt.Errorf("unknown keys subcommand %q", args[0])
}

func exportVerifyCmd(_ context.Context, args []string) error {
	fl := flag.NewFlagSet("export-verify", flag.ContinueOnError)
	fpFile := fl.String("fingerprints", "", "file of published \"key_id  fingerprint\" lines to pin")
	if len(args) == 0 {
		return errors.New("usage: pcc export-verify FILE.zip [--fingerprints F]")
	}
	file := args[0]
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	pinned := map[string]string{}
	if *fpFile != "" {
		f, err := os.Open(*fpFile)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if fields := strings.Fields(sc.Text()); len(fields) == 2 {
				pinned[fields[0]] = fields[1]
			}
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	f, err := os.Open(file) //nolint:gosec // operator-supplied path (CLI)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	rep, err := records.VerifyExport(f, st.Size(), pinned)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	if len(rep.Failures) > 0 {
		return fmt.Errorf("%d records failed verification", len(rep.Failures))
	}
	return nil
}

func auditEdgesCmd(ctx context.Context, args []string) error {
	a, _, closeDB, err := app(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	bad, err := a.Records.AuditEdges(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("edges without a valid Friendship record: %d\n", bad)
	if bad > 0 {
		return errors.New("audit failed")
	}
	return nil
}

func metricsCmd(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("metrics", flag.ContinueOnError)
	days := fl.Int("days", 14, "days to report")
	if err := fl.Parse(args); err != nil {
		return err
	}
	a, _, closeDB, err := app(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	rows, err := a.Metrics.Report(ctx, *days)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Printf("%s  %-32s %-10s %d\n", r.Day, r.Name, r.Bucket, r.Value)
	}
	return nil
}

func devCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "seed" {
		return errors.New("usage: pcc dev seed --users N [--model ba|ws] [--seed S]")
	}
	fl := flag.NewFlagSet("dev seed", flag.ContinueOnError)
	n := fl.Int("users", 1000, "number of synthetic users")
	model := fl.String("model", "ba", "graph model: ba (Barabási–Albert) or ws (Watts–Strogatz)")
	seed := fl.Int64("seed", 1, "random seed")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	a, cfg, closeDB, err := app(ctx)
	if err != nil {
		return err
	}
	defer closeDB()
	if !cfg.DevLike() {
		return errors.New("pcc dev seed refuses to run outside PCC_ENV=dev|test")
	}
	return a.DevSeed.Synthetic(ctx, *n, *model, *seed)
}
