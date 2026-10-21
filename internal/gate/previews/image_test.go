package previews_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

// imgEnv is env with the real media pipeline on in-memory stores and the dev
// scanner (a blocklist the test fills).
type imgEnv struct {
	*env
	dev   *scan.Dev
	blobs *blob.Mem
	queue *coreref.MemModQueue
}

func newImgEnv(t *testing.T) *imgEnv {
	t.Helper()
	e := newEnv(t)
	if !e.d.Cfg.MediaEnabled() {
		t.Fatal("the test config must enable media")
	}
	dev, err := scan.NewDev("")
	if err != nil {
		t.Fatal(err)
	}
	ie := &imgEnv{env: e, dev: dev, blobs: blob.NewMem("media"), queue: &coreref.MemModQueue{}}
	e.d.ModQueue = ie.queue
	e.a.Media.UseDrivers(media.Drivers{Scanner: dev, Blobs: ie.blobs, Evidence: blob.NewMem("evidence"), Purger: &blob.Recorder{}})
	return ie
}

func pngOf(w, h int, seed uint8) []byte {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{R: uint8(x) + seed, G: uint8(y) * seed, B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, m)
	return b.Bytes()
}

// imageOf returns link_previews.image_media_id of the post's ord-th preview.
func (e *imgEnv) imageOf(p core.PostID, ord int) uuid.UUID {
	e.t.Helper()
	var id *uuid.UUID
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT image_media_id FROM link_previews WHERE post_id = $1 AND ord = $2`,
		p, ord).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func (e *imgEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

const withImage = `<html><head><title>Pictured</title>
<meta property="og:image" content="/img/cover.png?utm_source=og"></head><body></body></html>`

// TestPreviewImageIngested: the og:image is fetched by the server (resolved,
// canonical), ingested through the media pipeline for this post, and its
// media id lands in image_media_id; the text preview is unchanged.
func TestPreviewImageIngested(t *testing.T) {
	e := newImgEnv(t)
	e.web.set("https://news.example/a", page{html: withImage})
	e.web.setImage("https://news.example/img/cover.png", pngOf(64, 40, 3))
	p := e.post("alice", "see https://news.example/a")
	e.drain()
	if c := e.web.called(); len(c) != 2 || c[1] != "img https://news.example/img/cover.png" {
		t.Fatalf("fetched %v", c)
	}
	id := e.imageOf(p, 0)
	if id == uuid.Nil {
		t.Fatal("no image_media_id")
	}
	if n := e.count(`SELECT count(*) FROM media WHERE id = $1 AND source_post = $2 AND owner_id IS NULL AND status = 1`, id, p); n != 1 {
		t.Fatal("the media row must be a ready system ingest for this post")
	}
	if n := e.count(`SELECT count(*) FROM media_variants WHERE media_id = $1`, id); n != 3 {
		t.Fatalf("%d variants", n)
	}
	e.clk.Advance(time.Minute)
	if got := e.shown("bob", p); len(got) != 1 || got[0].Title != "Pictured" {
		t.Fatalf("%+v", got)
	}
	if len(e.queue.Items) != 0 {
		t.Fatal("a clean image is not queued")
	}
}

// TestPreviewImagePerPost: a second post with the same link answers from the
// page cache but ingests its own copy of the image (deleting one post never
// takes another's image).
func TestPreviewImagePerPost(t *testing.T) {
	e := newImgEnv(t)
	e.web.set("https://news.example/a", page{html: withImage})
	e.web.setImage("https://news.example/img/cover.png", pngOf(64, 40, 3))
	p1 := e.post("alice", "https://news.example/a")
	e.drain()
	p2 := e.post("bob", "https://news.example/a?utm_medium=x")
	e.drain()
	want := []string{"https://news.example/a", "img https://news.example/img/cover.png", "img https://news.example/img/cover.png"}
	if c := e.web.called(); len(c) != 3 || c[0] != want[0] || c[1] != want[1] || c[2] != want[2] {
		t.Fatalf("fetched %v", c)
	}
	i1, i2 := e.imageOf(p1, 0), e.imageOf(p2, 0)
	if i1 == uuid.Nil || i2 == uuid.Nil || i1 == i2 {
		t.Fatalf("images %v %v", i1, i2)
	}
}

// TestPreviewImageCSAMHeld: a matched preview image is held and queued at
// priority 0 with the source URL and the linking post (never dropped
// silently), the post's author is held, nothing reaches the media store, and
// the preview shows without an image.
func TestPreviewImageCSAMHeld(t *testing.T) {
	e := newImgEnv(t)
	bad := pngOf(32, 32, 9)
	e.dev.Block(bad)
	e.web.set("https://bad.example/", page{html: `<title>Bad</title><meta property="og:image" content="https://cdn.example/x.png">`})
	e.web.setImage("https://cdn.example/x.png", bad)
	p := e.post("carol", "https://bad.example/")
	e.drain()
	if e.imageOf(p, 0) != uuid.Nil {
		t.Fatal("a held image must not be linked")
	}
	if len(e.queue.Items) != 1 {
		t.Fatalf("queue %+v", e.queue.Items)
	}
	it := e.queue.Items[0]
	if it.Source != core.ModSrcCSAM || it.Priority != 0 || it.SubjectPost != p || it.SubjectUser != e.ids["carol"] || it.Media == uuid.Nil {
		t.Fatalf("item %+v", it)
	}
	if n := e.count(`SELECT count(*) FROM csam_holds WHERE media_id = $1 AND source_url = 'https://cdn.example/x.png'
		AND subject_user = $2`, it.Media, int64(e.ids["carol"])); n != 1 {
		t.Fatal("hold with the source URL and the author")
	}
	if n := e.count(`SELECT count(*) FROM media WHERE id = $1 AND status = 3`, it.Media); n != 1 {
		t.Fatal("media row held")
	}
	if len(e.blobs.Keys()) != 0 {
		t.Fatalf("media store: %v", e.blobs.Keys())
	}
	e.clk.Advance(time.Minute)
	if got := e.shown("carol", p); len(got) != 1 || got[0].Title != "Bad" || got[0].Image != nil {
		t.Fatalf("%+v", got)
	}
	// The linking author is held: her uploads are refused.
	_, err := e.a.Media.Upload(e.as("carol"), bytes.NewReader(pngOf(16, 16, 2)), int64(len(pngOf(16, 16, 2))), "image/png", "")
	var pr *gate.Problem
	if !errors.As(err, &pr) || pr.Code != "media_rejected" {
		t.Fatalf("upload by the held author: %v", err)
	}
}

// TestPreviewImageSkipped: no image when the page has none, the image fails,
// is too large, or is not an image Media accepts; the preview still shows.
func TestPreviewImageSkipped(t *testing.T) {
	e := newImgEnv(t)
	e.web.set("https://plain.example/", page{html: `<title>Plain</title>`})
	e.web.set("https://gone.example/", page{html: `<title>Gone</title><meta property="og:image" content="/missing.png">`})
	e.web.set("https://big.example/", page{html: `<title>Big</title><meta property="og:image" content="/big.png">`})
	e.web.set("https://junk.example/", page{html: `<title>Junk</title><meta property="og:image" content="/junk.png">`})
	e.web.setImage("https://big.example/big.png", make([]byte, 5<<20+1))
	e.web.setImage("https://junk.example/junk.png", []byte("not an image"))
	p := e.post("alice", "https://plain.example/ https://gone.example/ https://big.example/")
	q := e.post("alice", "https://junk.example/")
	e.drain()
	for i := range 3 {
		if e.imageOf(p, i) != uuid.Nil {
			t.Fatalf("preview %d has an image", i)
		}
	}
	if e.imageOf(q, 0) != uuid.Nil {
		t.Fatal("junk has an image")
	}
	e.clk.Advance(time.Minute)
	if got := e.shown("bob", p); len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if len(e.queue.Items) != 0 {
		t.Fatal("nothing is queued")
	}
}

// TestPreviewImageMediaDisabled: with media off (fail closed) no image is
// fetched at all; the text preview still works.
func TestPreviewImageMediaDisabled(t *testing.T) {
	e := newEnv(t)
	e.d.Cfg.Scanner.Driver = config.ScannerNone
	if e.d.Cfg.MediaEnabled() {
		t.Skip("media cannot be switched off here")
	}
	e.web.set("https://news.example/a", page{html: withImage})
	e.web.setImage("https://news.example/img/cover.png", pngOf(8, 8, 1))
	p := e.post("alice", "https://news.example/a")
	e.drain()
	if c := e.web.called(); len(c) != 1 {
		t.Fatalf("fetched %v", c)
	}
	var id *uuid.UUID
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT image_media_id FROM link_previews WHERE post_id = $1`, p).Scan(&id); err != nil || id != nil {
		t.Fatalf("%v %v", id, err)
	}
}
