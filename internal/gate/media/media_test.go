package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/imageproc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

// Every image in these tests is generated in code; CSAM matches are synthetic
// (the dev scanner's digest list), never real imagery.

// ---- generated images ----

func pattern(w, h int, seed uint8) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x) ^ seed, G: uint8(y) + seed, B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	return img
}

func pngOf(w, h int, seed uint8) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, pattern(w, h, seed))
	return buf.Bytes()
}

var gpsMarker = []byte{0x13, 0x57, 0x9B, 0xDF, 0x24, 0x68, 0xAC, 0xE0}

// gpsJPEG is a JPEG whose APP1 Exif segment holds a GPS IFD with gpsMarker.
func gpsJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, pattern(w, h, 7), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0}
	ifd0 := make([]byte, 2+12+4)
	le.PutUint16(ifd0[0:], 1)
	le.PutUint16(ifd0[2:], 0x8825)
	le.PutUint16(ifd0[4:], 4)
	le.PutUint32(ifd0[6:], 1)
	le.PutUint32(ifd0[10:], uint32(8+len(ifd0))) //nolint:gosec // small
	tiff = append(tiff, ifd0...)
	gps := make([]byte, 2+12+4)
	le.PutUint16(gps[0:], 1)
	le.PutUint16(gps[2:], 0x0002)
	le.PutUint16(gps[4:], 5)
	le.PutUint32(gps[6:], 1)
	le.PutUint32(gps[10:], uint32(8+len(ifd0)+len(gps))) //nolint:gosec // small
	tiff = append(tiff, gps...)
	tiff = append(tiff, gpsMarker...)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(app1)+2)) //nolint:gosec // small
	seg = append(seg, app1...)
	j := jb.Bytes()
	out := append([]byte{}, j[:2]...)
	out = append(out, seg...)
	return append(out, j[2:]...)
}

// bombPNG is a valid PNG header claiming w×h pixels.
func bombPNG(w, h uint32) []byte {
	b := pngOf(1, 1, 0)
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}

// ---- drivers ----

// countingScanner wraps a scanner, counting calls; err fails every scan; block
// waits for the context.
type countingScanner struct {
	inner scan.Scanner
	mu    sync.Mutex
	calls int
	err   error
	block bool
}

func (c *countingScanner) Name() string { return "dev" }

func (c *countingScanner) Scan(ctx context.Context, img []byte, mime string) (scan.Verdict, error) {
	c.mu.Lock()
	c.calls++
	err, block := c.err, c.block
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return scan.Verdict{}, ctx.Err()
	}
	if err != nil {
		return scan.Verdict{}, err
	}
	return c.inner.Scan(ctx, img, mime)
}

type chargeLimiter struct {
	coreref.PassLimiter
	mu    sync.Mutex
	reads []core.ReadClass
}

func (l *chargeLimiter) ChargeRead(_ context.Context, _ core.Viewer, c core.ReadClass) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reads = append(l.reads, c)
	return nil
}

type env struct {
	t        *testing.T
	a        *wire.App
	d        *core.Deps
	m        *media.Service
	w        *wiretest.TestWorld
	dev      *scan.Dev
	sc       *countingScanner
	blobs    *blob.Mem
	evidence *blob.Mem
	purger   *blob.Recorder
	queue    *coreref.MemModQueue
	limits   *chargeLimiter
	ctx      context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	dev, err := scan.NewDev("")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, a: a, d: d, m: a.Media, w: wiretest.World(t, a), dev: dev, sc: &countingScanner{inner: dev},
		blobs: blob.NewMem("media"), evidence: blob.NewMem("evidence"), purger: &blob.Recorder{},
		queue: &coreref.MemModQueue{}, limits: &chargeLimiter{}, ctx: context.Background()}
	d.ModQueue, d.Limits = e.queue, e.limits
	e.m.UseDrivers(media.Drivers{Scanner: e.sc, Blobs: e.blobs, Evidence: e.evidence, Purger: e.purger})
	for _, h := range []string{"alice", "bob", "carol", "dave"} {
		e.w.User(h)
	}
	e.w.Friends("alice", "bob")
	return e
}

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.w.ID(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

func (e *env) upload(h string, img []byte, alt string) (gate.MediaView, error) {
	return e.m.Upload(e.as(h), bytes.NewReader(img), int64(len(img)), "image/png", alt)
}

func (e *env) mustUpload(h string, img []byte) gate.MediaView {
	e.t.Helper()
	mv, err := e.upload(h, img, "")
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	return mv
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) id(h string, r gate.Ref) uuid.UUID {
	e.t.Helper()
	id, ok := e.d.Refs.OpenMedia(e.w.ID(h), r)
	if !ok {
		e.t.Fatal("ref does not open")
	}
	return id
}

// attach attaches h's media to a new post of h (Attach + the post_media row P1-W12 writes).
func (e *env) attach(h, label string, refs ...gate.Ref) core.PostID {
	e.t.Helper()
	p := e.w.Post(h, label, "with a picture")
	ids, err := e.m.Attach(e.ctx, e.d.DB.Q(), e.w.ID(h), refs)
	if err != nil {
		e.t.Fatalf("attach: %v", err)
	}
	for i, id := range ids {
		if _, err := e.d.DB.Q().Exec(e.ctx, `INSERT INTO post_media (post_id, ord, media_id) VALUES ($1, $2, $3)`, p, i, id); err != nil {
			e.t.Fatal(err)
		}
	}
	return p
}

func code(err error) string {
	var p *gate.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// ---- upload ----

func TestUploadVariantsStripMetadata(t *testing.T) {
	e := newEnv(t)
	img := gpsJPEG(t, 1500, 1000)
	mv, err := e.upload("alice", img, "  a\tgradient\x00 ")
	if err != nil {
		t.Fatal(err)
	}
	if mv.W != 1500 || mv.H != 1000 || mv.Alt != "a gradient" || mv.Ref == "" {
		t.Fatalf("view %+v", mv)
	}
	id := e.id("alice", mv.Ref)
	rows, err := e.d.DB.Q().Query(e.ctx, `SELECT variant, blob_key, mime, width, height, bytes FROM media_variants WHERE media_id = $1 ORDER BY variant`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[int][2]int{media.VariantThumb: {320, 213}, media.VariantDisplay: {1080, 720}, media.VariantOriginal: {1500, 1000}}
	n := 0
	for rows.Next() {
		var (
			v, w, h, size int
			key, mime     string
		)
		if err := rows.Scan(&v, &key, &mime, &w, &h, &size); err != nil {
			t.Fatal(err)
		}
		n++
		if got := [2]int{w, h}; got != want[v] || mime != "image/webp" {
			t.Errorf("variant %d: %v %s", v, got, mime)
		}
		data, ok := e.blobs.Bytes(key)
		if !ok || len(data) != size {
			t.Fatalf("variant %d blob missing", v)
		}
		if bytes.Contains(data, gpsMarker) || bytes.Contains(data, []byte("Exif")) {
			t.Errorf("variant %d keeps EXIF GPS", v)
		}
		for _, c := range imageproc.WebPChunks(data) {
			if c == "EXIF" || c == "XMP " {
				t.Errorf("variant %d has %s", v, c)
			}
		}
		// Blob keys are random, never a content hash.
		sum := sha256.Sum256(data)
		if len(key) != blob.KeyLen || strings.Contains(strings.ToLower(base32.StdEncoding.EncodeToString(sum[:])), key[:10]) {
			t.Errorf("variant %d key %q", v, key)
		}
	}
	if n != 3 {
		t.Fatalf("%d variants", n)
	}
	if e.evidence.Keys() != nil && len(e.evidence.Keys()) != 0 {
		t.Fatal("a clean upload writes no evidence")
	}
	// The same bytes again: new media, new keys.
	mv2 := e.mustUpload("alice", img)
	if e.id("alice", mv2.Ref) == id || len(e.blobs.Keys()) != 6 {
		t.Fatalf("second upload must get fresh keys: %d blobs", len(e.blobs.Keys()))
	}
}

func TestUploadInputProblems(t *testing.T) {
	e := newEnv(t)
	if _, err := e.upload("alice", []byte("not an image"), ""); code(err) != "media_type" {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := e.upload("alice", pngOf(4, 4, 1), strings.Repeat("x", 1001)); code(err) != "alt_too_long" {
		t.Fatalf("alt: %v", err)
	}
	big := make([]byte, media.MaxUploadBytes+1)
	if _, err := e.m.Upload(e.as("alice"), bytes.NewReader(big), -1, "", ""); code(err) != "media_too_large" {
		t.Fatalf("too large: %v", err)
	}
	if _, err := e.m.Upload(e.ctx, bytes.NewReader(pngOf(4, 4, 1)), 10, "", ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous: %v", err)
	}
	if e.sc.calls != 0 || len(e.blobs.Keys()) != 0 {
		t.Fatal("refused input must never reach the scanner or a store")
	}
}

// A 50,000×50,000 PNG header is refused before decoding and before the scan.
func TestBombRefusedBeforeDecode(t *testing.T) {
	e := newEnv(t)
	if _, err := e.upload("alice", bombPNG(50_000, 50_000), ""); code(err) != "media_too_large" {
		t.Fatalf("bomb: %v", err)
	}
	if e.sc.calls != 0 || e.count(`SELECT count(*) FROM media`) != 0 {
		t.Fatal("bomb reached the scanner or the database")
	}
}

// A scanner error or timeout rejects the upload and stores nothing anywhere.
func TestScannerFailureStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.sc.err = errors.New("photodna down")
	if _, err := e.upload("alice", pngOf(32, 32, 2), ""); code(err) != "media_rejected" {
		t.Fatalf("scanner error: %v", err)
	}
	e.sc.err, e.sc.block = nil, true
	ctx, cancel := context.WithTimeout(e.as("alice"), 50*time.Millisecond)
	defer cancel()
	img := pngOf(32, 32, 3)
	if _, err := e.m.Upload(ctx, bytes.NewReader(img), int64(len(img)), "", ""); code(err) != "media_rejected" {
		t.Fatalf("timeout: %v", err)
	}
	if len(e.blobs.Keys())+len(e.evidence.Keys()) != 0 || e.count(`SELECT count(*) FROM media`) != 0 ||
		e.count(`SELECT count(*) FROM csam_holds`) != 0 || len(e.queue.Items) != 0 {
		t.Fatal("a failed scan must store nothing anywhere")
	}
	// Not a hold: the next upload works once the scanner is back.
	e.sc.block = false
	e.mustUpload("alice", pngOf(32, 32, 4))
}

// ---- CSAM match path ----

func TestMatchPreservesHoldsAndQueues(t *testing.T) {
	e := newEnv(t)
	bad := pngOf(40, 40, 9)
	e.dev.Block(bad)
	if _, err := e.upload("bob", bad, "x"); code(err) != "media_rejected" {
		t.Fatalf("match: %v", err)
	}
	if len(e.blobs.Keys()) != 0 {
		t.Fatal("a matched image must never reach the media store")
	}
	evk := e.evidence.Keys()
	if len(evk) != 1 {
		t.Fatalf("evidence objects %v", evk)
	}
	if got, _ := e.evidence.Bytes(evk[0]); !bytes.Equal(got, bad) {
		t.Fatal("evidence must be the exact uploaded bytes")
	}
	var (
		mid          uuid.UUID
		status       int
		held, retain time.Time
		key          string
	)
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT m.id, m.status, h.held_at, h.retain_until, h.evidence_key FROM media m
		JOIN csam_holds h ON h.media_id = m.id`).Scan(&mid, &status, &held, &retain, &key); err != nil {
		t.Fatal(err)
	}
	if status != 3 || key != evk[0] || !retain.Equal(held.Add(media.Retention)) {
		t.Fatalf("hold row status %d key %q retain %v", status, key, retain.Sub(held))
	}
	if e.count(`SELECT count(*) FROM media_variants`) != 0 {
		t.Fatal("held media has no variants")
	}
	if len(e.queue.Items) != 1 {
		t.Fatalf("queue %+v", e.queue.Items)
	}
	it := e.queue.Items[0]
	if it.Source != core.ModSrcCSAM || it.Priority != 0 || it.SubjectUser != e.w.ID("bob") || it.Media != mid ||
		it.Summary["csam_match"] != 1 || it.Summary["upload"] != 1 {
		t.Fatalf("queue item %+v", it)
	}
	// Moderators see metadata only.
	h, err := e.m.Hold(e.ctx, mid)
	if err != nil || !h.AccountHeld || h.SubjectUser != e.w.ID("bob") || h.MatchInfo["driver"] != "dev" || h.MatchInfo["source"] != "upload" {
		t.Fatalf("hold %+v %v", h, err)
	}
	// Account hold: bob's next (clean) upload is rejected too; alice is unaffected.
	if _, err := e.upload("bob", pngOf(16, 16, 1), ""); code(err) != "media_rejected" {
		t.Fatalf("held account upload: %v", err)
	}
	e.mustUpload("alice", pngOf(16, 16, 1))
	// The csam_report action: validated, once, preservation extended.
	tx := e.d.DB.Q()
	if err := e.m.Report(e.ctx, tx, mid, ""); code(err) != "bad_request" {
		t.Fatalf("empty ref: %v", err)
	}
	if err := e.m.Report(e.ctx, tx, uuid.New(), "CT-1"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("unknown hold: %v", err)
	}
	_ = e.a.Hooks().AdvanceClock(48 * time.Hour)
	if err := e.m.Report(e.ctx, tx, mid, "CT-123456"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Report(e.ctx, tx, mid, "CT-2"); code(err) != "csam_already_reported" {
		t.Fatalf("second report: %v", err)
	}
	h, _ = e.m.Hold(e.ctx, mid)
	if h.ReportRef != "CT-123456" || h.ReportedAt == nil || !h.RetainUntil.Equal(h.ReportedAt.Add(media.Retention)) {
		t.Fatalf("reported hold %+v", h)
	}
	// A reviewed false positive lifts the upload hold; the evidence stays.
	if err := e.m.ReleaseAccount(e.ctx, tx, mid); err != nil {
		t.Fatal(err)
	}
	e.mustUpload("bob", pngOf(16, 16, 2))
	if len(e.evidence.Keys()) != 1 {
		t.Fatal("evidence must stay after a release")
	}
	if err := e.m.ReleaseAccount(e.ctx, tx, uuid.New()); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
}

// An evidence-store failure still holds the account and queues the item.
func TestMatchWithoutEvidenceStoreStillHolds(t *testing.T) {
	e := newEnv(t)
	bad := pngOf(20, 20, 5)
	e.dev.Block(bad)
	e.evidence.FailPut = true
	if _, err := e.upload("bob", bad, ""); code(err) != "media_rejected" {
		t.Fatal(err)
	}
	if len(e.queue.Items) != 1 || e.count(`SELECT count(*) FROM csam_holds WHERE match_info->>'evidence' = 'unavailable'`) != 1 {
		t.Fatal("hold and queue item must exist")
	}
	if _, err := e.upload("bob", pngOf(8, 8, 1), ""); code(err) != "media_rejected" {
		t.Fatal("account must be held")
	}
}

// Preview images take the same path: hold + priority-0 item + source URL,
// the linking post's author held; never dropped silently.
func TestIngestPreviewMatch(t *testing.T) {
	e := newEnv(t)
	p := e.w.Post("carol", "linkpost", "look https://example.com/a")
	bad := pngOf(64, 48, 11)
	e.dev.Block(bad)
	if _, err := e.m.Ingest(e.ctx, bad, p, "https://example.com/a"); code(err) != "media_rejected" {
		t.Fatalf("ingest match: %v", err)
	}
	if len(e.queue.Items) != 1 {
		t.Fatal("queue item")
	}
	it := e.queue.Items[0]
	if it.SubjectPost != p || it.SubjectUser != e.w.ID("carol") || it.Priority != 0 || it.Summary["preview"] != 1 {
		t.Fatalf("item %+v", it)
	}
	h, err := e.m.Hold(e.ctx, it.Media)
	if err != nil || h.SourceURL != "https://example.com/a" || h.SourcePost != p || !h.AccountHeld {
		t.Fatalf("hold %+v %v", h, err)
	}
	if len(e.blobs.Keys()) != 0 || len(e.evidence.Keys()) != 1 {
		t.Fatal("stores")
	}
	if _, err := e.upload("carol", pngOf(8, 8, 1), ""); code(err) != "media_rejected" {
		t.Fatal("the linking post's author is held")
	}
	// A clean preview image is stored and renders for a viewer of the post.
	id, err := e.m.Ingest(e.ctx, pngOf(64, 48, 12), p, "https://example.com/b")
	if err != nil || id == uuid.Nil {
		t.Fatal(err)
	}
	sr, _ := e.d.Vis.SearchFor(e.ctx, e.w.ID("carol"))
	vs, err := e.m.Views(e.ctx, sr, []uuid.UUID{id, uuid.New()})
	if err != nil || len(vs) != 1 || vs[0].W != 64 {
		t.Fatalf("views %+v %v", vs, err)
	}
	if _, _, err := e.m.URL(e.as("carol"), vs[0].Ref, "display"); err != nil {
		t.Fatalf("preview URL for the author: %v", err)
	}
}

// ---- fail-closed configuration ----

func prodConfig(t *testing.T, extra map[string]string) *config.Config {
	t.Helper()
	m := map[string]string{
		"PCC_ENV": "prod", "PCC_ORIGIN": "https://pcc.example", "PCC_DATABASE_URL": "postgres://x/y",
		"PCC_MASTER_KEY": strings.Repeat("11", 32), "PCC_KEK": strings.Repeat("22", 32),
		"PCC_SERVER_KEY_FILE": "/etc/pcc/server.key", "PCC_MEDIA_BASE_URL": "https://media.pcc.example",
		"PCC_BLOB_DRIVER": "s3", "PCC_S3_ENDPOINT": "r2.example", "PCC_S3_BUCKET": "b", "PCC_S3_ACCESS_KEY": "a", "PCC_S3_SECRET_KEY": "s",
		"PCC_EVIDENCE_DRIVER": "s3", "PCC_EVIDENCE_S3_ENDPOINT": "r2.example", "PCC_EVIDENCE_S3_BUCKET": "e",
		"PCC_EVIDENCE_S3_ACCESS_KEY": "a", "PCC_EVIDENCE_S3_SECRET_KEY": "s",
	}
	for k, v := range extra {
		m[k] = v
	}
	c, err := config.LoadFrom(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Prod with PCC_SCANNER=dev or none, or without an evidence store ⇒ upload
// feature_disabled (even with working drivers injected).
func TestProdFailClosed(t *testing.T) {
	e := newEnv(t)
	for name, extra := range map[string]map[string]string{
		"scanner dev":  {"PCC_SCANNER": "dev"},
		"scanner none": {"PCC_SCANNER": "none"},
		"no evidence": {"PCC_SCANNER": "photodna", "PCC_PHOTODNA_KEY": "k", "PCC_PHOTODNA_URL": "https://p.example",
			"PCC_EVIDENCE_DRIVER": ""},
	} {
		d := *e.d
		d.Cfg = prodConfig(t, extra)
		s := media.New(&d)
		s.UseDrivers(media.Drivers{Scanner: e.sc, Blobs: e.blobs, Evidence: e.evidence, Purger: e.purger})
		img := pngOf(8, 8, 1)
		if _, err := s.Upload(e.as("alice"), bytes.NewReader(img), int64(len(img)), "", ""); !errors.Is(err, gate.ErrDisabled) {
			t.Errorf("%s: upload %v", name, err)
		}
		var de *gate.DisabledError
		if _, _, err := s.URL(e.as("alice"), "x", "thumb"); !errors.As(err, &de) || de.Feature != "media" {
			t.Errorf("%s: url %v", name, err)
		}
		if _, err := s.Ingest(e.ctx, img, uuid.New(), "https://x"); !errors.Is(err, gate.ErrDisabled) {
			t.Errorf("%s: ingest %v", name, err)
		}
		if _, err := s.Attach(e.ctx, e.d.DB.Q(), e.w.ID("alice"), []gate.Ref{"x"}); !errors.Is(err, gate.ErrDisabled) {
			t.Errorf("%s: attach %v", name, err)
		}
	}
	if e.sc.calls != 0 || len(e.blobs.Keys()) != 0 {
		t.Fatal("disabled media must touch nothing")
	}
	// The fully configured prod case is enabled (PhotoDNA + both stores + URL key).
	ok := prodConfig(t, map[string]string{"PCC_SCANNER": "photodna", "PCC_PHOTODNA_KEY": "k", "PCC_PHOTODNA_URL": "https://p.example"})
	if !ok.MediaEnabled() {
		t.Fatal("complete prod config must enable media")
	}
}

// ---- attach ----

func TestAttach(t *testing.T) {
	e := newEnv(t)
	a1 := e.mustUpload("alice", pngOf(8, 8, 1))
	b1 := e.mustUpload("bob", pngOf(8, 8, 2))
	q := e.d.DB.Q()
	alice := e.w.ID("alice")
	if _, err := e.m.Attach(e.ctx, q, alice, nil); err != nil {
		t.Fatal(err)
	}
	// bob's media (bob's ref, and alice can't even hold a ref of hers for it).
	if _, err := e.m.Attach(e.ctx, q, alice, []gate.Ref{b1.Ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("other's media: %v", err)
	}
	if _, err := e.m.Attach(e.ctx, q, alice, []gate.Ref{a1.Ref, a1.Ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("duplicate: %v", err)
	}
	five := []gate.Ref{a1.Ref, a1.Ref, a1.Ref, a1.Ref, a1.Ref}
	if _, err := e.m.Attach(e.ctx, q, alice, five); code(err) != "bad_request" {
		t.Fatalf("five: %v", err)
	}
	// All or none: a good ref with a bad one attaches nothing.
	if _, err := e.m.Attach(e.ctx, q, alice, []gate.Ref{a1.Ref, "garbage"}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
	ids, err := e.m.Attach(e.ctx, q, alice, []gate.Ref{a1.Ref})
	if err != nil || len(ids) != 1 || ids[0] != e.id("alice", a1.Ref) {
		t.Fatalf("attach %v %v", ids, err)
	}
	if _, err := e.m.Attach(e.ctx, q, alice, []gate.Ref{a1.Ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("second attach: %v", err)
	}
}

// ---- URLs and the dev media origin ----

func parse(t *testing.T, raw string) (string, url.Values) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path[strings.LastIndexByte(u.Path, '/')+1:], u.Query()
}

func TestURLAccess(t *testing.T) {
	e := newEnv(t)
	mv := e.mustUpload("alice", pngOf(30, 20, 3))
	raw, exp, err := e.m.URL(e.as("alice"), mv.Ref, "display")
	if err != nil {
		t.Fatal(err)
	}
	now := e.d.Clock.Now()
	if exp.After(now.Add(media.URLTTL)) || exp.Before(now.Add(media.URLTTL-media.URLStep)) {
		t.Fatalf("expiry %v", exp.Sub(now))
	}
	if !strings.HasPrefix(raw, e.d.Cfg.MediaBaseURL+"/") {
		t.Fatalf("url %s", raw)
	}
	// bob has no ref of his own until the media is in a post he can see.
	bobRef := e.d.Refs.Media(e.w.ID("bob"), e.id("alice", mv.Ref))
	if _, _, err := e.m.URL(e.as("bob"), bobRef, "display"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("unattached for bob: %v", err)
	}
	if _, _, err := e.m.URL(e.as("bob"), mv.Ref, "display"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("alice's ref for bob: %v", err)
	}
	e.attach("alice", "pic", mv.Ref)
	if _, _, err := e.m.URL(e.as("bob"), bobRef, "thumb"); err != nil {
		t.Fatalf("friend: %v", err)
	}
	daveRef := e.d.Refs.Media(e.w.ID("dave"), e.id("alice", mv.Ref))
	if _, _, err := e.m.URL(e.as("dave"), daveRef, "thumb"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("stranger: %v", err)
	}
	if _, _, err := e.m.URL(e.as("bob"), bobRef, "huge"); code(err) != "bad_request" {
		t.Fatalf("variant: %v", err)
	}
	// original charges media_full (before the ref is resolved: garbage too).
	e.limits.reads = nil
	if _, _, err := e.m.URL(e.as("bob"), bobRef, "original"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.m.URL(e.as("bob"), "garbage", "original"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
	if _, _, err := e.m.URL(e.as("bob"), bobRef, "display"); err != nil {
		t.Fatal(err)
	}
	if len(e.limits.reads) != 2 || e.limits.reads[0] != "media_full" || e.limits.reads[1] != "media_full" {
		t.Fatalf("charges %v", e.limits.reads)
	}
	// After an unfriend, bob's next URL request is not found.
	e.d.Graph.RemoveEdge(e.w.ID("alice"), e.w.ID("bob"))
	if _, _, err := e.m.URL(e.as("bob"), bobRef, "thumb"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("after unfriend: %v", err)
	}
}

func TestServeSignedURLs(t *testing.T) {
	e := newEnv(t)
	mv := e.mustUpload("alice", pngOf(30, 20, 4))
	raw, _, err := e.m.URL(e.as("alice"), mv.Ref, "thumb")
	if err != nil {
		t.Fatal(err)
	}
	key, q := parse(t, raw)
	b, err := e.m.Serve(e.ctx, key, q)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(b.Body)
	_ = b.Body.Close()
	stored, _ := e.blobs.Bytes(key)
	if !bytes.Equal(data, stored) || b.MIME != "image/webp" || b.MaxAge <= 0 || b.MaxAge > 3600 {
		t.Fatalf("served %d bytes %s max-age %d", len(data), b.MIME, b.MaxAge)
	}
	forbidden := func(name string, k string, q url.Values) {
		t.Helper()
		if _, err := e.m.Serve(e.ctx, k, q); !errors.Is(err, media.ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	forbidden("unsigned", key, url.Values{})
	other := url.Values{"e": q["e"], "s": q["s"]}
	sg := blob.NewSigner(e.d.Keys.MediaURL)
	tag := sg.ViewerTag(uint64(e.w.ID("bob")))
	other.Set("v", base64URL(tag[:]))
	forbidden("other viewer tag", key, other)
	bad := url.Values{"e": q["e"], "v": q["v"], "s": {strings.Repeat("A", 43)}}
	forbidden("bad signature", key, bad)
	// A URL is not valid for another key.
	mv2 := e.mustUpload("alice", pngOf(30, 20, 5))
	raw2, _, _ := e.m.URL(e.as("alice"), mv2.Ref, "thumb")
	key2, _ := parse(t, raw2)
	forbidden("other key", key2, q)
	// Expired after the hour.
	if err := e.a.Hooks().AdvanceClock(media.URLTTL + time.Minute); err != nil {
		t.Fatal(err)
	}
	forbidden("expired", key, q)
	// A fresh URL for deleted media is signed but not found.
	raw3, _, _ := e.m.URL(e.as("alice"), mv2.Ref, "thumb")
	k3, q3 := parse(t, raw3)
	if _, err := e.d.DB.Q().Exec(e.ctx, `UPDATE media SET deleted_at = now() WHERE id = $1`, e.id("alice", mv2.Ref)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Serve(e.ctx, k3, q3); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// ---- deletion ----

func TestDeletionPurges(t *testing.T) {
	e := newEnv(t)
	mv := e.mustUpload("alice", pngOf(30, 20, 6))
	p := e.attach("alice", "pic", mv.Ref)
	orphan := e.mustUpload("bob", pngOf(30, 20, 7))
	bad := pngOf(30, 20, 8)
	e.dev.Block(bad)
	_, _ = e.upload("carol", bad, "")
	if len(e.blobs.Keys()) != 6 {
		t.Fatalf("blobs %d", len(e.blobs.Keys()))
	}
	// Post deletion (the Bus event enqueues media.process).
	e.d.Bus.Publish(core.PostDeleted{ID: p, Author: e.w.ID("alice")})
	if err := e.d.Jobs.Drain(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.blobs.Keys()) != 3 || len(e.purger.URLs()) != 3 {
		t.Fatalf("after post delete: %d blobs, purged %v", len(e.blobs.Keys()), e.purger.URLs())
	}
	for _, u := range e.purger.URLs() {
		if !strings.HasPrefix(u, e.d.Cfg.MediaBaseURL+"/") || strings.Contains(u, "?") {
			t.Fatalf("purge URL %s", u)
		}
	}
	// The unattached upload is swept after OrphanTTL.
	if err := e.m.Process(e.ctx, "", 0); err != nil {
		t.Fatal(err)
	}
	if len(e.blobs.Keys()) != 3 {
		t.Fatal("a fresh upload must not be swept")
	}
	if err := e.a.Hooks().AdvanceClock(media.OrphanTTL + time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Process(e.ctx, "", 0); err != nil {
		t.Fatal(err)
	}
	if len(e.blobs.Keys()) != 0 {
		t.Fatal("orphan not swept")
	}
	if _, _, err := e.m.URL(e.as("bob"), orphan.Ref, "thumb"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("swept media must be gone")
	}
	// Held media and its evidence survive account deletion and sweeps;
	// evidence is deleted only once reported and past retention.
	e.d.Bus.Publish(core.AccountDeleted{ID: e.w.ID("carol")})
	_ = e.d.Jobs.Drain(e.ctx)
	if e.count(`SELECT count(*) FROM media WHERE status = 3 AND deleted_at IS NULL`) != 1 || len(e.evidence.Keys()) != 1 {
		t.Fatal("held media must survive")
	}
	if err := e.a.Hooks().AdvanceClock(media.Retention + time.Hour); err != nil {
		t.Fatal(err)
	}
	_ = e.m.Process(e.ctx, "", 0)
	if len(e.evidence.Keys()) != 1 {
		t.Fatal("unreported evidence is never deleted automatically")
	}
	var mid uuid.UUID
	_ = e.d.DB.Q().QueryRow(e.ctx, `SELECT media_id FROM csam_holds`).Scan(&mid)
	if err := e.m.Report(e.ctx, e.d.DB.Q(), mid, "CT-9"); err != nil {
		t.Fatal(err)
	}
	_ = e.m.Process(e.ctx, "", 0)
	if len(e.evidence.Keys()) != 1 {
		t.Fatal("reported evidence stays a year from the report")
	}
	_ = e.a.Hooks().AdvanceClock(media.Retention + time.Hour)
	if err := e.m.Process(e.ctx, "", 0); err != nil {
		t.Fatal(err)
	}
	if len(e.evidence.Keys()) != 0 {
		t.Fatal("evidence past retention must be deleted")
	}
	if _, err := e.m.Hold(e.ctx, mid); err != nil {
		t.Fatal("the hold metadata stays")
	}
}

func TestViewsSkipsUnready(t *testing.T) {
	e := newEnv(t)
	mv := e.mustUpload("alice", pngOf(30, 20, 9))
	id := e.id("alice", mv.Ref)
	sr, _ := e.d.Vis.SearchFor(e.ctx, e.w.ID("bob"))
	vs, err := e.m.Views(e.ctx, sr, []uuid.UUID{id})
	if err != nil || len(vs) != 1 || vs[0].Ref != e.d.Refs.Media(e.w.ID("bob"), id) {
		t.Fatalf("views %+v %v", vs, err)
	}
	_, _ = e.d.DB.Q().Exec(e.ctx, `UPDATE media SET deleted_at = now() WHERE id = $1`, id)
	if vs, _ := e.m.Views(e.ctx, sr, []uuid.UUID{id}); len(vs) != 0 {
		t.Fatal("deleted media must not render")
	}
}

// ---- draft uploads: alt text after upload, discard ----

func TestSetAltAndDiscard(t *testing.T) {
	e := newEnv(t)
	mv := e.mustUpload("alice", pngOf(30, 20, 5))
	got, err := e.m.SetAlt(e.as("alice"), mv.Ref, "  a red\nsquare ")
	if err != nil || got.Alt != "a red square" || got.Ref != mv.Ref || got.W != 30 || got.H != 20 {
		t.Fatalf("set alt: %+v %v", got, err)
	}
	if e.count(`SELECT count(*) FROM media WHERE alt = 'a red square'`) != 1 {
		t.Fatal("alt not stored")
	}
	if _, err := e.m.SetAlt(e.as("alice"), mv.Ref, strings.Repeat("x", media.MaxAltRunes+1)); err == nil {
		t.Fatal("alt over the limit must be refused")
	}
	// Another user cannot touch alice's upload, with her ref or their own.
	bobRef := e.d.Refs.Media(e.w.ID("bob"), e.id("alice", mv.Ref))
	for _, r := range []gate.Ref{mv.Ref, bobRef} {
		if _, err := e.m.SetAlt(e.as("bob"), r, "x"); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("bob set alt: %v", err)
		}
		if err := e.m.Discard(e.as("bob"), r); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("bob discard: %v", err)
		}
	}
	// Once attached, the upload is the post's: no alt change, no discard.
	e.attach("alice", "pic", mv.Ref)
	if _, err := e.m.SetAlt(e.as("alice"), mv.Ref, "x"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("attached set alt: %v", err)
	}
	if err := e.m.Discard(e.as("alice"), mv.Ref); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("attached discard: %v", err)
	}
	// A draft upload is discarded and its blobs purged by the queued run.
	d := e.mustUpload("alice", pngOf(25, 25, 6))
	before := len(e.blobs.Keys())
	if err := e.m.Discard(e.as("alice"), d.Ref); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Discard(e.as("alice"), d.Ref); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("second discard: %v", err)
	}
	if err := e.d.Jobs.Drain(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.blobs.Keys()) != before-3 {
		t.Fatalf("blobs %d, want %d", len(e.blobs.Keys()), before-3)
	}
	if _, err := e.m.Attach(e.ctx, e.d.DB.Q(), e.w.ID("alice"), []gate.Ref{d.Ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("a discarded upload must not attach: %v", err)
	}
}
