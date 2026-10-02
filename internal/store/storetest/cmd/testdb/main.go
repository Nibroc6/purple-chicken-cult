// Command testdb manages per-run databases for scripts (e2e, db-reset):
//
//	testdb template-name     print the template name for the current migrations
//	testdb create PREFIX     clone a fresh database from the migrated template; print its URL
//	testdb drop NAME         drop a database created by `create` (refuses other names)
//
// It uses PCC_TEST_DATABASE_URL (default: the local pcc_test database).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/store"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "testdb:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	base := storetest.URL()
	if len(args) == 0 {
		return fmt.Errorf("usage: testdb template-name | create PREFIX | drop NAME")
	}
	switch args[0] {
	case "template-name":
		h, err := store.MigrationsHash()
		if err != nil {
			return err
		}
		fmt.Println("pcc_tpl_" + h)
		return nil
	case "create":
		prefix := "pcc_e2e_"
		if len(args) > 1 {
			prefix = args[1]
		}
		if !strings.HasPrefix(prefix, "pcc_e2e_") && !strings.HasPrefix(prefix, "pcc_t_") {
			return fmt.Errorf("prefix must start with pcc_e2e_ or pcc_t_")
		}
		var b [6]byte
		_, _ = rand.Read(b[:])
		name := prefix + hex.EncodeToString(b[:])
		if err := storetest.CreateFromTemplate(ctx, base, name); err != nil {
			return err
		}
		u, err := storetest.URLFor(base, name)
		if err != nil {
			return err
		}
		fmt.Println(u)
		return nil
	case "drop":
		if len(args) < 2 || (!strings.HasPrefix(args[1], "pcc_e2e_") && !strings.HasPrefix(args[1], "pcc_t_")) {
			return fmt.Errorf("drop needs a pcc_e2e_* or pcc_t_* name")
		}
		c, err := pgx.Connect(ctx, base)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, err = c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{args[1]}.Sanitize())
		return err
	}
	return fmt.Errorf("unknown command %q", args[0])
}
