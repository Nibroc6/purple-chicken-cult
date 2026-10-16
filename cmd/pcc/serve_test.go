package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/storetest"
)

// prodEnv sets a complete prod configuration (plan §4.2) against a fresh
// migrated database and returns nothing; t.Setenv restores the environment.
func prodEnv(t *testing.T, extra map[string]string) {
	t.Helper()
	base := storetest.URL()
	if base == "" {
		t.Skip("no test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := storetest.Template(ctx, base); err != nil {
		t.Skipf("no test database: %v", err)
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	name := "pcc_t_serve_" + hex.EncodeToString(rnd[:])
	if err := storetest.CreateFromTemplate(ctx, base, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if admin, err := pgx.Connect(ctx, base); err == nil {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = admin.Close(ctx)
		}
	})
	dbURL, err := storetest.URLFor(base, name)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "server.key")
	if _, err := sigs.CreateServerKey(keyFile); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"PCC_ENV":             "prod",
		"PCC_ORIGIN":          "https://pcc.example",
		"PCC_HTTP_ADDR":       "127.0.0.1:0",
		"PCC_DATABASE_URL":    dbURL,
		"PCC_MASTER_KEY":      strings.Repeat("1", 64),
		"PCC_KEK":             strings.Repeat("2", 64),
		"PCC_SERVER_KEY_FILE": keyFile,
		"PCC_MEDIA_BASE_URL":  "https://media.pcc.example",
		"PCC_TEST_HOOKS":      "",
		"PCC_AUTO_MIGRATE":    "",
		"PCC_LOG_LEVEL":       "error",
		"PCC_SCANNER":         "",
		"PCC_PHOTODNA_KEY":    "",
		"PCC_PHOTODNA_URL":    "",
		"PCC_BLOB_DRIVER":     "",
		"PCC_EVIDENCE_DRIVER": "",
		"PCC_ALLOW_FS_BLOB":   "",
		"PCC_VAPID_PUBLIC":    "",
		"PCC_VAPID_PRIVATE":   "",
		"PCC_VAPID_SUBJECT":   "",
	}
	for k, v := range extra {
		env[k] = v
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// The fail-closed matrix at startup (D20, SCAFFOLD-1 acceptance): a prod config
// with a dev or no scanner starts with media disabled; an unknown scanner such as
// "cloudflare" (which scans after storage) refuses to start.
func TestServeFailClosed(t *testing.T) {
	ctx := context.Background()
	evidence := map[string]string{
		"PCC_BLOB_DRIVER": "s3", "PCC_S3_ENDPOINT": "r2.example", "PCC_S3_BUCKET": "media",
		"PCC_S3_ACCESS_KEY": "a", "PCC_S3_SECRET_KEY": "s",
		"PCC_EVIDENCE_DRIVER": "s3", "PCC_EVIDENCE_S3_ENDPOINT": "r2.example", "PCC_EVIDENCE_S3_BUCKET": "evidence",
		"PCC_EVIDENCE_S3_ACCESS_KEY": "a", "PCC_EVIDENCE_S3_SECRET_KEY": "s",
	}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		refuse  bool
		mediaOn bool
	}{
		{name: "dev scanner", env: map[string]string{"PCC_SCANNER": "dev"}},
		{name: "dev scanner with stores", env: merge(evidence, map[string]string{"PCC_SCANNER": "dev"})},
		{name: "no scanner", env: map[string]string{"PCC_SCANNER": "none"}},
		{name: "photodna without evidence store", env: map[string]string{"PCC_SCANNER": "photodna",
			"PCC_PHOTODNA_KEY": "k", "PCC_PHOTODNA_URL": "https://photodna.example", "PCC_BLOB_DRIVER": "s3",
			"PCC_S3_ENDPOINT": "r2.example", "PCC_S3_BUCKET": "media", "PCC_S3_ACCESS_KEY": "a", "PCC_S3_SECRET_KEY": "s"}},
		{name: "photodna with stores", env: merge(evidence, map[string]string{"PCC_SCANNER": "photodna",
			"PCC_PHOTODNA_KEY": "k", "PCC_PHOTODNA_URL": "https://photodna.example"}), mediaOn: true},
		{name: "cloudflare scanner", env: map[string]string{"PCC_SCANNER": "cloudflare"}, refuse: true},
		{name: "no kek", env: map[string]string{"PCC_KEK": ""}, refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prodEnv(t, tc.env)
			srv, a, _, closeDB, err := newServer(ctx)
			if tc.refuse {
				if err == nil {
					closeDB()
					t.Fatal("prod must refuse to start")
				}
				return
			}
			if err != nil {
				t.Fatalf("prod must start: %v", err)
			}
			defer closeDB()
			if got := a.Media.Enabled(); got != tc.mediaOn {
				t.Fatalf("media enabled = %v, want %v", got, tc.mediaOn)
			}
			if a.Config().PushEnabled() {
				t.Fatal("push must be disabled without VAPID keys")
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "https://pcc.example/api/session", nil)
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"authenticated":false`) {
				t.Fatalf("GET /api/session: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
