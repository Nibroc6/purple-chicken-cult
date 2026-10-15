// Command pcc is the single binary: the server and every operator CLI subcommand
// (plan §4.1). Stdlib flag, one FlagSet per subcommand.
//
// Owner: SCAFFOLD. Work packages implement the called service methods.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `usage: pcc <command> [flags]

commands:
  serve                                   API + SSE + static SPA + job workers
  migrate                                 apply database migrations
  genesis --handle H [--name N]           create a genesis user; prints a one-time enrollment URL
  mod grant --handle H (--by ADMIN | --bootstrap)
                                          grant moderator (first admin: --bootstrap)
  keys rotate                             new server signing key (prints its fingerprint)
  keys fingerprints                       print "key_id  sha256(pubkey)" lines to publish
  export-verify FILE.zip [--fingerprints F]
                                          verify an export offline
  audit-edges                             check every edge against its signed Friendship record
  metrics [--days 14]                     print aggregate product metrics
  dev seed --users N [--model ba|ws] [--seed S]
                                          synthetic data (PCC_ENV=dev|test only)

configuration: PCC_* environment variables (PCC_ENV is required: dev, test or prod).
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pcc:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("missing command")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return serve(ctx, rest)
	case "migrate":
		return migrateCmd(ctx, rest)
	case "genesis":
		return genesisCmd(ctx, rest)
	case "mod":
		return modCmd(ctx, rest)
	case "keys":
		return keysCmd(ctx, rest)
	case "export-verify":
		return exportVerifyCmd(ctx, rest)
	case "audit-edges":
		return auditEdgesCmd(ctx, rest)
	case "metrics":
		return metricsCmd(ctx, rest)
	case "dev":
		return devCmd(ctx, rest)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}
