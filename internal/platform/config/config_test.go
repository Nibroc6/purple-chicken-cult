package config

import (
	"fmt"
	"strings"
	"testing"
)

const (
	keyA = "1111111111111111111111111111111111111111111111111111111111111111"
	keyB = "2222222222222222222222222222222222222222222222222222222222222222"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func prodBase() map[string]string {
	return map[string]string{
		"PCC_ENV":             "prod",
		"PCC_ORIGIN":          "https://pcc.example",
		"PCC_DATABASE_URL":    "postgres://x/y",
		"PCC_MASTER_KEY":      keyA,
		"PCC_KEK":             keyB,
		"PCC_SERVER_KEY_FILE": "/etc/pcc/server.key",
		"PCC_MEDIA_BASE_URL":  "https://media.pcc.example",
	}
}

func TestEnvRequired(t *testing.T) {
	for _, v := range []string{"", "garbage", "Prod", "development"} {
		if _, err := LoadFrom(env(map[string]string{"PCC_ENV": v})); err == nil {
			t.Errorf("PCC_ENV=%q must fail", v)
		}
	}
}

func TestDevDefaults(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"PCC_ENV": "dev"}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.DevLike() || !c.MasterKeyRandom || c.ServerKeyFile == "" || c.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("unexpected dev defaults: %+v", c)
	}
	if !c.MediaEnabled() {
		t.Fatal("dev defaults (dev scanner + fs stores) must enable media")
	}
	if c.RPID != "localhost" {
		t.Fatalf("RPID: %q", c.RPID)
	}
}

func TestProdValid(t *testing.T) {
	c, err := LoadFrom(env(prodBase()))
	if err != nil {
		t.Fatal(err)
	}
	if c.DevLike() || c.MediaEnabled() || c.PushEnabled() {
		t.Fatal("prod without scanner/stores/VAPID must have every P1 integration disabled")
	}
}

func TestProdRefusals(t *testing.T) {
	cases := map[string]func(m map[string]string){
		"no master key":     func(m map[string]string) { delete(m, "PCC_MASTER_KEY") },
		"zero master key":   func(m map[string]string) { m["PCC_MASTER_KEY"] = strings.Repeat("0", 64) },
		"short master key":  func(m map[string]string) { m["PCC_MASTER_KEY"] = "abcd" },
		"kek equals master": func(m map[string]string) { m["PCC_KEK"] = keyA },
		"no kek":            func(m map[string]string) { delete(m, "PCC_KEK") },
		"http origin":       func(m map[string]string) { m["PCC_ORIGIN"] = "http://pcc.example" },
		"no server key":     func(m map[string]string) { delete(m, "PCC_SERVER_KEY_FILE") },
		"test hooks":        func(m map[string]string) { m["PCC_TEST_HOOKS"] = "1" },
		"cdn w/o proxies":   func(m map[string]string) { m["PCC_BEHIND_CDN"] = "1" },
		"bad rpid":          func(m map[string]string) { m["PCC_RP_ID"] = "evil.example" },
		"cloudflare scan":   func(m map[string]string) { m["PCC_SCANNER"] = "cloudflare" },
		"unknown blob":      func(m map[string]string) { m["PCC_BLOB_DRIVER"] = "gcs" },
		"no db":             func(m map[string]string) { delete(m, "PCC_DATABASE_URL") },
	}
	for name, mut := range cases {
		m := prodBase()
		mut(m)
		if _, err := LoadFrom(env(m)); err == nil {
			t.Errorf("%s: must refuse to start", name)
		}
	}
}

func TestTestHooksOnlyInTest(t *testing.T) {
	if _, err := LoadFrom(env(map[string]string{"PCC_ENV": "dev", "PCC_TEST_HOOKS": "1"})); err == nil {
		t.Fatal("test hooks with Env=dev must fail")
	}
	if _, err := LoadFrom(env(map[string]string{"PCC_ENV": "test", "PCC_TEST_HOOKS": "1"})); err != nil {
		t.Fatal(err)
	}
}

func TestDevLoopback(t *testing.T) {
	if _, err := LoadFrom(env(map[string]string{"PCC_ENV": "dev", "PCC_HTTP_ADDR": "0.0.0.0:8080"})); err == nil {
		t.Fatal("dev must bind loopback only")
	}
}

// TestMediaFailClosedMatrix: Env × Scanner × Blob × Evidence ⇒ MediaEnabled.
func TestMediaFailClosedMatrix(t *testing.T) {
	scanners := []string{"photodna", "dev", "none", "unscanned", "unscanned+allow"}
	blobs := []string{"none", "fs", "fs+allow", "s3"}
	evidence := []string{"none", "set"}
	for _, e := range []string{"dev", "test", "prod"} {
		for _, sc := range scanners {
			for _, bl := range blobs {
				for _, ev := range evidence {
					m := prodBase()
					m["PCC_ENV"] = e
					if e != "prod" {
						m["PCC_ORIGIN"] = "http://localhost:5173"
					}
					m["PCC_SCANNER"] = strings.TrimSuffix(sc, "+allow")
					if sc == "unscanned+allow" {
						m["PCC_ALLOW_UNSCANNED_MEDIA"] = "1"
					}
					if sc == "photodna" {
						m["PCC_PHOTODNA_KEY"], m["PCC_PHOTODNA_URL"] = "k", "https://photodna.example"
					}
					switch bl {
					case "none":
						m["PCC_BLOB_DRIVER"] = ""
						if e != "prod" {
							m["PCC_BLOB_DRIVER"] = "s3" // s3 without credentials = not configured
						}
					case "fs", "fs+allow":
						m["PCC_BLOB_DRIVER"], m["PCC_BLOB_FS_DIR"] = "fs", "/tmp/blobs"
						if bl == "fs+allow" {
							m["PCC_ALLOW_FS_BLOB"] = "1"
						}
					case "s3":
						m["PCC_BLOB_DRIVER"] = "s3"
						m["PCC_S3_ENDPOINT"], m["PCC_S3_BUCKET"], m["PCC_S3_ACCESS_KEY"], m["PCC_S3_SECRET_KEY"] = "r2.example", "b", "a", "s"
					}
					if ev == "set" {
						m["PCC_EVIDENCE_DRIVER"] = "s3"
						m["PCC_EVIDENCE_S3_ENDPOINT"], m["PCC_EVIDENCE_S3_BUCKET"] = "r2.example", "evidence"
						m["PCC_EVIDENCE_S3_ACCESS_KEY"], m["PCC_EVIDENCE_S3_SECRET_KEY"] = "a", "s"
					} else {
						m["PCC_EVIDENCE_DRIVER"] = "s3" // configured driver without credentials
					}
					name := fmt.Sprintf("%s/%s/%s/%s", e, sc, bl, ev)
					c, err := LoadFrom(env(m))
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					want := false
					blobOK := bl != "none"
					if e == "prod" {
						want = (sc == "photodna" || sc == "unscanned+allow") && ev == "set" && (bl == "s3" || bl == "fs+allow")
					} else {
						want = (sc == "photodna" || sc == "dev" || sc == "unscanned+allow") && blobOK && ev == "set"
					}
					if got := c.MediaEnabled(); got != want {
						t.Errorf("%s: MediaEnabled=%v want %v", name, got, want)
					}
				}
			}
		}
	}
}

// TestServesMedia: the process serves /m/ in dev/test, and in prod only with
// media on and an fs store (s3 is served by the Worker).
func TestServesMedia(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]string)
		want bool
	}{
		{"prod fs media on", func(map[string]string) {}, true},
		{"prod fs not allowed", func(m map[string]string) { m["PCC_ALLOW_FS_BLOB"] = "" }, false},
		{"prod s3", func(m map[string]string) {
			m["PCC_BLOB_DRIVER"] = "s3"
			m["PCC_S3_ENDPOINT"], m["PCC_S3_BUCKET"], m["PCC_S3_ACCESS_KEY"], m["PCC_S3_SECRET_KEY"] = "r2.example", "b", "a", "s"
		}, false},
		{"prod no scanner", func(m map[string]string) { m["PCC_SCANNER"] = "none" }, false},
		{"dev", func(m map[string]string) { m["PCC_ENV"], m["PCC_ORIGIN"] = "dev", "http://localhost:5173" }, true},
	}
	for _, tc := range cases {
		m := prodBase()
		m["PCC_SCANNER"], m["PCC_ALLOW_UNSCANNED_MEDIA"] = "unscanned", "1"
		m["PCC_BLOB_DRIVER"], m["PCC_BLOB_FS_DIR"], m["PCC_ALLOW_FS_BLOB"] = "fs", "/var/lib/pcc/media", "1"
		m["PCC_EVIDENCE_DRIVER"], m["PCC_EVIDENCE_FS_DIR"] = "fs", "/var/lib/pcc/evidence"
		tc.edit(m)
		c, err := LoadFrom(env(m))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := c.ServesMedia(); got != tc.want {
			t.Errorf("%s: ServesMedia=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestPushEnabled(t *testing.T) {
	m := prodBase()
	m["PCC_VAPID_PUBLIC"], m["PCC_VAPID_PRIVATE"], m["PCC_VAPID_SUBJECT"] = "p", "q", "mailto:x@y"
	c, err := LoadFrom(env(m))
	if err != nil || !c.PushEnabled() {
		t.Fatalf("push must be enabled with VAPID keys: %v", err)
	}
}

func TestDerivedKeysDistinct(t *testing.T) {
	c, err := LoadFrom(env(prodBase()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, l := range []string{"pcc/ref/v1", "pcc/csrf/v1", "pcc/cursor/v1", "pcc/media-url/v1", "pcc/pseudonym/v1"} {
		k := fmt.Sprintf("%x", c.Key(l, 32))
		if other, dup := seen[k]; dup {
			t.Fatalf("%s and %s derive the same key", l, other)
		}
		seen[k] = l
	}
	if len(c.Key("pcc/ref/v1", 64)) != 64 {
		t.Fatal("ref key must be 64 bytes")
	}
}

// TestCDNPurge: the purge driver is an enum (unknown ⇒ refuse to start) and
// cloudflare needs its zone and token (contract request p1-w04-1).
func TestCDNPurge(t *testing.T) {
	m := prodBase()
	c, err := LoadFrom(env(m))
	if err != nil || c.CDN.Purge != CDNPurgeNone {
		t.Fatalf("default: %v %+v", err, c)
	}
	m["PCC_CDN_PURGE"] = "fastly"
	if _, err := LoadFrom(env(m)); err == nil {
		t.Fatal("unknown purge driver must refuse to start")
	}
	m["PCC_CDN_PURGE"] = "cloudflare"
	if _, err := LoadFrom(env(m)); err == nil {
		t.Fatal("cloudflare purge without zone and token must refuse to start")
	}
	m["PCC_CDN_ZONE_ID"], m["PCC_CDN_API_TOKEN"] = "z", "t"
	if c, err := LoadFrom(env(m)); err != nil || c.CDN.Purge != CDNPurgeCloudflare {
		t.Fatalf("cloudflare: %v", err)
	}
}
