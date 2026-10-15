// Package config loads and validates process configuration from PCC_* variables.
//
// Dev-only behavior is gated by an ALLOWLIST (Env ∈ {dev, test} via DevLike), never
// by "Env != prod". PCC_ENV is required: unset or unknown values refuse to start.
// Phase 1 integrations (media, push, DBSC) are fail-closed feature switches computed
// from the configuration (MediaEnabled, PushEnabled, DBSCEnabled); a disabled
// feature is not a startup error, but unknown enum values and weak keys are.
package config

import (
	"bytes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is the deployment environment.
type Env string

// Environments. There is NO default.
const (
	EnvDev  Env = "dev"
	EnvTest Env = "test"
	EnvProd Env = "prod"
)

// Blob drivers.
const (
	BlobNone = ""
	BlobFS   = "fs"
	BlobS3   = "s3"
)

// Scanner drivers. There is deliberately no "cloudflare" driver: Cloudflare's CSAM
// tool scans at the CDN after storage and can never be the §6 pre-storage scanner.
const (
	ScannerPhotoDNA = "photodna"
	ScannerDev      = "dev"
	ScannerNone     = "none"
)

// BlobConfig configures an object store (PCC_BLOB_* or PCC_EVIDENCE_*).
type BlobConfig struct {
	Driver      string // "", "fs", "s3"
	FSDir       string
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3UseSSL    bool
}

// Configured reports whether the store has everything its driver needs.
func (b BlobConfig) Configured() bool {
	switch b.Driver {
	case BlobFS:
		return b.FSDir != ""
	case BlobS3:
		return b.S3Endpoint != "" && b.S3Bucket != "" && b.S3AccessKey != "" && b.S3SecretKey != ""
	}
	return false
}

// ScannerConfig configures the pre-storage CSAM scanner.
type ScannerConfig struct {
	Driver      string // photodna | dev | none
	PhotoDNAKey string
	PhotoDNAURL string
}

// PushConfig configures Web Push (VAPID).
type PushConfig struct {
	VAPIDPublic  string
	VAPIDPrivate string
	VAPIDSubject string
}

// Config is the process configuration.
type Config struct {
	Env             Env
	HTTPAddr        string
	Origin          string
	RPID            string
	TrustedProxies  []netip.Prefix
	BehindCDN       bool
	DatabaseURL     string
	AutoMigrate     bool
	MasterKey       []byte
	MasterKeyRandom bool // dev/test only: no PCC_MASTER_KEY was set, a random one is in use
	ServerKeyFile   string
	StaticDir       string // PCC_STATIC_DIR: serve the SPA from this directory instead of the embedded web/dist
	MediaBaseURL    string // PCC_MEDIA_BASE_URL (P1): signed media URLs point here; dev = <Origin>/m
	LazyBFS         bool   // PCC_LAZY_BFS (P1)

	// Phase 1.
	KEK          []byte
	Blob         BlobConfig
	AllowFSBlob  bool
	EvidenceBlob BlobConfig
	Scanner      ScannerConfig
	Push         PushConfig
	DBSC         bool

	MinAge       int
	TOSVersion   int
	QuarantineK  int
	PreviewDelay time.Duration
	FeedWindow   time.Duration
	TestHooks    bool
	LogLevel     slog.Level
}

// Getenv is the lookup used by Load (replaceable in tests).
type Getenv func(string) string

// Load reads PCC_* variables from the process environment and validates them.
func Load() (*Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads configuration through get and validates it.
func LoadFrom(get Getenv) (*Config, error) {
	c := &Config{}
	switch env := Env(strings.TrimSpace(get("PCC_ENV"))); env {
	case EnvDev, EnvTest, EnvProd:
		c.Env = env
	case "":
		return nil, errors.New("config: PCC_ENV is required (dev, test or prod)")
	default:
		return nil, fmt.Errorf("config: unknown PCC_ENV %q", env)
	}
	dev := c.DevLike()

	c.HTTPAddr = str(get, "PCC_HTTP_ADDR", map[bool]string{true: "127.0.0.1:8080", false: ":8080"}[dev])
	defOrigin := ""
	switch c.Env {
	case EnvDev:
		defOrigin = "http://localhost:5173"
	case EnvTest:
		defOrigin = "http://localhost:8080"
	}
	c.Origin = strings.TrimRight(str(get, "PCC_ORIGIN", defOrigin), "/")
	if c.Origin == "" {
		return nil, errors.New("config: PCC_ORIGIN is required")
	}
	ou, err := url.Parse(c.Origin)
	if err != nil || ou.Host == "" || (ou.Scheme != "http" && ou.Scheme != "https") || ou.Path != "" {
		return nil, fmt.Errorf("config: PCC_ORIGIN %q must be scheme://host[:port]", c.Origin)
	}
	c.RPID = str(get, "PCC_RP_ID", ou.Hostname())

	if v := get("PCC_TRUSTED_PROXIES"); v != "" {
		for _, s := range strings.Split(v, ",") {
			p, err := netip.ParsePrefix(strings.TrimSpace(s))
			if err != nil {
				return nil, fmt.Errorf("config: PCC_TRUSTED_PROXIES: %w", err)
			}
			c.TrustedProxies = append(c.TrustedProxies, p.Masked())
		}
	}
	if c.BehindCDN, err = boolean(get, "PCC_BEHIND_CDN", false); err != nil {
		return nil, err
	}

	defDB := ""
	switch c.Env {
	case EnvDev:
		defDB = "postgres://pcc:pcc@localhost:54329/pcc?sslmode=disable" //nolint:gosec // dev-only default (local container)
	case EnvTest:
		defDB = "postgres://pcc:pcc@localhost:54329/pcc_test?sslmode=disable" //nolint:gosec // test-only default (local container)
	}
	c.DatabaseURL = str(get, "PCC_DATABASE_URL", defDB)
	if c.AutoMigrate, err = boolean(get, "PCC_AUTO_MIGRATE", dev); err != nil {
		return nil, err
	}

	if v := get("PCC_MASTER_KEY"); v != "" {
		if c.MasterKey, err = hexKey("PCC_MASTER_KEY", v); err != nil {
			return nil, err
		}
	} else if dev {
		c.MasterKey = make([]byte, 32)
		if _, err := rand.Read(c.MasterKey); err != nil {
			return nil, err
		}
		c.MasterKeyRandom = true
	}
	c.ServerKeyFile = str(get, "PCC_SERVER_KEY_FILE", map[bool]string{true: "./var/keys/server.key", false: ""}[dev])
	c.StaticDir = get("PCC_STATIC_DIR")
	c.MediaBaseURL = strings.TrimRight(str(get, "PCC_MEDIA_BASE_URL", map[bool]string{true: c.Origin + "/m", false: ""}[dev]), "/")
	if c.LazyBFS, err = boolean(get, "PCC_LAZY_BFS", false); err != nil {
		return nil, err
	}

	// ---- Phase 1 ----
	if v := get("PCC_KEK"); v != "" {
		if c.KEK, err = hexKey("PCC_KEK", v); err != nil {
			return nil, err
		}
	}
	if c.Blob, err = loadBlob(get, "PCC_BLOB_", "PCC_S3_", dev, "./var/blobs"); err != nil {
		return nil, err
	}
	if c.EvidenceBlob, err = loadBlob(get, "PCC_EVIDENCE_", "PCC_EVIDENCE_S3_", dev, "./var/evidence"); err != nil {
		return nil, err
	}
	if c.AllowFSBlob, err = boolean(get, "PCC_ALLOW_FS_BLOB", false); err != nil {
		return nil, err
	}
	c.Scanner.Driver = str(get, "PCC_SCANNER", map[bool]string{true: ScannerDev, false: ScannerNone}[dev])
	switch c.Scanner.Driver {
	case ScannerPhotoDNA, ScannerDev, ScannerNone:
	default:
		return nil, fmt.Errorf("config: unknown PCC_SCANNER %q (photodna, dev or none)", c.Scanner.Driver)
	}
	c.Scanner.PhotoDNAKey = get("PCC_PHOTODNA_KEY")
	c.Scanner.PhotoDNAURL = get("PCC_PHOTODNA_URL")
	c.Push = PushConfig{VAPIDPublic: get("PCC_VAPID_PUBLIC"), VAPIDPrivate: get("PCC_VAPID_PRIVATE"), VAPIDSubject: get("PCC_VAPID_SUBJECT")}
	if c.DBSC, err = boolean(get, "PCC_DBSC", false); err != nil {
		return nil, err
	}

	if c.MinAge, err = integer(get, "PCC_MIN_AGE", 16); err != nil {
		return nil, err
	}
	if c.TOSVersion, err = integer(get, "PCC_TOS_VERSION", 1); err != nil {
		return nil, err
	}
	if c.QuarantineK, err = integer(get, "PCC_QUARANTINE_K", 3); err != nil {
		return nil, err
	}
	if c.PreviewDelay, err = duration(get, "PCC_PREVIEW_DELAY", 30*time.Second); err != nil {
		return nil, err
	}
	if c.FeedWindow, err = duration(get, "PCC_FEED_WINDOW", 168*time.Hour); err != nil {
		return nil, err
	}
	if c.TestHooks, err = boolean(get, "PCC_TEST_HOOKS", false); err != nil {
		return nil, err
	}
	switch strings.ToLower(str(get, "PCC_LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		return nil, errors.New("config: PCC_LOG_LEVEL must be debug, info, warn or error")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func loadBlob(get Getenv, prefix, s3prefix string, dev bool, devDir string) (BlobConfig, error) {
	b := BlobConfig{
		Driver:      str(get, prefix+"DRIVER", map[bool]string{true: BlobFS, false: BlobNone}[dev]),
		FSDir:       get(prefix + "FS_DIR"),
		S3Endpoint:  get(s3prefix + "ENDPOINT"),
		S3Bucket:    get(s3prefix + "BUCKET"),
		S3AccessKey: get(s3prefix + "ACCESS_KEY"),
		S3SecretKey: get(s3prefix + "SECRET_KEY"),
		S3Region:    str(get, s3prefix+"REGION", "auto"),
		S3UseSSL:    get(s3prefix+"INSECURE") != "1",
	}
	switch b.Driver {
	case BlobNone, BlobS3:
	case BlobFS:
		if b.FSDir == "" && dev {
			b.FSDir = devDir
		}
	default:
		return b, fmt.Errorf("config: unknown %sDRIVER %q (fs or s3)", prefix, b.Driver)
	}
	return b, nil
}

// DevLike reports Env ∈ {dev, test}: the only predicate dev-only paths may use.
func (c *Config) DevLike() bool { return c.Env == EnvDev || c.Env == EnvTest }

// Prod reports Env == prod.
func (c *Config) Prod() bool { return c.Env == EnvProd }

// Validate checks cross-field rules (see package doc and plan §4.2).
func (c *Config) Validate() error {
	switch c.Env {
	case EnvDev, EnvTest, EnvProd:
	default:
		return fmt.Errorf("config: invalid env %q", c.Env)
	}
	if err := checkKey("MasterKey", c.MasterKey); err != nil {
		return err
	}
	if c.KEK != nil {
		if err := checkKey("KEK", c.KEK); err != nil {
			return err
		}
		if bytes.Equal(c.KEK, c.MasterKey) {
			return errors.New("config: PCC_KEK must differ from PCC_MASTER_KEY")
		}
	}
	ou, err := url.Parse(c.Origin)
	if err != nil || ou.Hostname() == "" {
		return errors.New("config: invalid origin")
	}
	host := ou.Hostname()
	if c.RPID == "" || (host != c.RPID && !strings.HasSuffix(host, "."+c.RPID)) {
		return fmt.Errorf("config: PCC_RP_ID %q must be a registrable suffix of the origin host %q", c.RPID, host)
	}
	if c.Env == EnvDev && !loopbackAddr(c.HTTPAddr) {
		return fmt.Errorf("config: in dev PCC_HTTP_ADDR must be a loopback address, got %q", c.HTTPAddr)
	}
	if c.TestHooks && c.Env != EnvTest {
		return errors.New("config: PCC_TEST_HOOKS is only allowed with PCC_ENV=test")
	}
	if c.DatabaseURL == "" {
		return errors.New("config: PCC_DATABASE_URL is required")
	}
	if c.Prod() {
		if ou.Scheme != "https" {
			return errors.New("config: prod requires an https PCC_ORIGIN")
		}
		if c.MasterKeyRandom {
			return errors.New("config: prod requires PCC_MASTER_KEY")
		}
		if c.ServerKeyFile == "" {
			return errors.New("config: prod requires PCC_SERVER_KEY_FILE (never generated)")
		}
		if c.BehindCDN && len(c.TrustedProxies) == 0 {
			return errors.New("config: PCC_BEHIND_CDN=1 requires PCC_TRUSTED_PROXIES")
		}
		if c.KEK == nil {
			return errors.New("config: prod requires PCC_KEK (Phase 1 per-user keys)")
		}
	}
	if c.MinAge < 13 || c.MinAge > 99 || c.TOSVersion < 1 || c.QuarantineK < 1 {
		return errors.New("config: PCC_MIN_AGE, PCC_TOS_VERSION or PCC_QUARANTINE_K out of range")
	}
	if c.PreviewDelay <= 0 || c.FeedWindow <= 0 {
		return errors.New("config: durations must be positive")
	}
	return nil
}

// MediaEnabled is the fail-closed media switch (D20).
//
//	prod: Scanner == photodna with its key and URL, Blob configured (s3, or fs with
//	      PCC_ALLOW_FS_BLOB), EvidenceBlob configured and a media-URL key derivable.
//	dev/test: a dev or configured photodna scanner plus configured blob + evidence stores.
//	Anything else ⇒ false; uploads then answer feature_disabled.
func (c *Config) MediaEnabled() bool {
	if len(c.MasterKey) != 32 {
		return false
	}
	photodna := c.Scanner.Driver == ScannerPhotoDNA && c.Scanner.PhotoDNAKey != "" && c.Scanner.PhotoDNAURL != ""
	if c.Prod() {
		if !photodna || !c.EvidenceBlob.Configured() || !c.Blob.Configured() {
			return false
		}
		if c.Blob.Driver == BlobFS && !c.AllowFSBlob {
			return false
		}
		if c.EvidenceBlob.Driver == BlobFS && !c.AllowFSBlob {
			return false
		}
		return c.MediaBaseURL != ""
	}
	if c.DevLike() {
		return (photodna || c.Scanner.Driver == ScannerDev) && c.Blob.Configured() && c.EvidenceBlob.Configured()
	}
	return false
}

// PushEnabled reports whether VAPID keys are present.
func (c *Config) PushEnabled() bool {
	return c.Push.VAPIDPublic != "" && c.Push.VAPIDPrivate != "" && c.Push.VAPIDSubject != ""
}

// DBSCEnabled reports PCC_DBSC=1 (any env). When false, sessions use the short-lived
// re-verify fallback (§17.2(2)).
func (c *Config) DBSCEnabled() bool { return c.DBSC }

// Key derives a symmetric application key: HKDF-SHA256(MasterKey, label).
// Labels in use: "pcc/ref/v1" (64 B), "pcc/csrf/v1", "pcc/cursor/v1",
// "pcc/media-url/v1", "pcc/pseudonym/v1", "pcc/log-salt/v1/<day>",
// "pcc/metrics-salt/v1/<day>". Distinct labels give independent keys.
func (c *Config) Key(label string, n int) []byte {
	k, err := hkdf.Key(sha256.New, c.MasterKey, nil, label, n)
	if err != nil {
		panic("config: hkdf: " + err.Error())
	}
	return k
}

// ---- helpers ----

func checkKey(name string, k []byte) error {
	if len(k) != 32 {
		return fmt.Errorf("config: %s must be 32 bytes", name)
	}
	if bytes.Equal(k, make([]byte, 32)) {
		return fmt.Errorf("config: %s must not be all zero", name)
	}
	return nil
}

func hexKey(name, v string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(v))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("config: %s must be 64 hex characters (32 bytes)", name)
	}
	return b, nil
}

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func str(get Getenv, k, def string) string {
	if v := strings.TrimSpace(get(k)); v != "" {
		return v
	}
	return def
}

func boolean(get Getenv, k string, def bool) (bool, error) {
	switch strings.TrimSpace(get(k)) {
	case "":
		return def, nil
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return false, fmt.Errorf("config: %s must be 0 or 1", k)
}

func integer(get Getenv, k string, def int) (int, error) {
	v := strings.TrimSpace(get(k))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer", k)
	}
	return n, nil
}

func duration(get Getenv, k string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(get(k))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a Go duration", k)
	}
	return d, nil
}
