package posts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

// withMedia gives the app in-memory media stores and the dev scanner.
func (e *env) withMedia() {
	e.t.Helper()
	dev, err := scan.NewDev("")
	if err != nil {
		e.t.Fatal(err)
	}
	e.a.Media.UseDrivers(media.Drivers{Scanner: dev, Blobs: blob.NewMem("media"), Evidence: blob.NewMem("evidence"),
		Purger: &blob.Recorder{}})
}

func pngOf(seed uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	for y := range 8 {
		for x := range 12 {
			img.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 30), B: seed, A: 255}) //nolint:gosec // small
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

var seed uint8

func (e *env) upload(h, alt string) gate.MediaView {
	e.t.Helper()
	seed++
	img := pngOf(seed)
	mv, err := e.a.Media.Upload(e.as(h), bytes.NewReader(img), int64(len(img)), "image/png", alt)
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	return mv
}

func (e *env) mediaID(h string, r gate.Ref) uuid.UUID {
	e.t.Helper()
	id, ok := e.d.Refs.OpenMedia(e.id(h), r)
	if !ok {
		e.t.Fatal("media ref does not open")
	}
	return id
}

// recordMedia reads the media ids of a post's current signed Post record.
func (e *env) recordMedia(id core.PostID) []string {
	e.t.Helper()
	var body []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT r.body FROM signed_records r JOIN posts p ON p.record_hash = r.hash
		WHERE p.id = $1`, id).Scan(&body); err != nil {
		e.t.Fatal(err)
	}
	var rec struct {
		Media []string `json:"media"`
	}
	if err := json.Unmarshal(body, &rec); err != nil || rec.Media == nil {
		e.t.Fatalf("record %s: %v", body, err)
	}
	return rec.Media
}

// Acceptance: up to 4 owned, ready media attach in order; the Post record lists
// their ids; every viewer of the post gets refs of their own, and the URL
// follows the post's visibility.
func TestMediaAttach(t *testing.T) {
	e := newEnv(t)
	e.withMedia()
	m1, m2 := e.upload("alice", "first"), e.upload("alice", "")
	pv, err := e.p.Create(e.as("alice"), posts.Draft{Body: "two pictures", Lang: "en", Media: []gate.Ref{m2.Ref, m1.Ref}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Media) != 2 || pv.Media[0].Ref != m2.Ref || pv.Media[1].Ref != m1.Ref || pv.Media[1].Alt != "first" ||
		pv.Media[0].W != 12 || pv.Media[0].H != 8 {
		t.Fatalf("media %+v", pv.Media)
	}
	id := e.pid("alice", pv)
	id1, id2 := e.mediaID("alice", m1.Ref), e.mediaID("alice", m2.Ref)
	if got := e.recordMedia(id); len(got) != 2 || got[0] != id2.String() || got[1] != id1.String() {
		t.Fatalf("record media %v", got)
	}
	if e.count("SELECT count(*) FROM post_media WHERE post_id = $1", id) != 2 {
		t.Fatal("post_media rows")
	}
	// A post without media records an empty list.
	plain := e.post("alice", "no pictures")
	if got := e.recordMedia(e.pid("alice", plain)); len(got) != 0 || len(plain.Media) != 0 {
		t.Fatalf("plain: %v %+v", got, plain.Media)
	}

	// bob (a friend) sees the media under refs of his own, and gets a URL.
	got, err := e.p.Get(e.as("bob"), e.ref("bob", id))
	if err != nil || len(got.Media) != 2 {
		t.Fatalf("bob: %+v %v", got, err)
	}
	if got.Media[0].Ref == m2.Ref || e.mediaID("bob", got.Media[0].Ref) != id2 {
		t.Fatal("bob's media ref is not his own")
	}
	if _, _, err := e.a.Media.URL(e.as("bob"), got.Media[0].Ref, "display"); err != nil {
		t.Fatalf("bob url: %v", err)
	}
	// erin (another component) cannot see the post, so she gets no URL.
	if _, _, err := e.a.Media.URL(e.as("erin"), e.d.Refs.Media(e.id("erin"), id1), "display"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("erin url: %v", err)
	}

	// Edits keep the media in the new record.
	if _, err := e.p.Edit(e.as("alice"), pv.Ref, "two pictures, edited", ""); err != nil {
		t.Fatal(err)
	}
	if got := e.recordMedia(id); len(got) != 2 || got[0] != id2.String() {
		t.Fatalf("edited record media %v", got)
	}
	// A ready preview's image renders through Media.Views too (gate/previews
	// writes image_media_id; any ready media will do here).
	img := e.upload("alice", "site image")
	e.exec(`INSERT INTO link_previews (post_id, ord, url_canon, site_host, title, status, ready_at, image_media_id)
		VALUES ($1, 0, 'https://example.com/', 'example.com', 'Example', 1, $2, $3)`, id, e.d.Clock.Now(),
		e.mediaID("alice", img.Ref))
	got, err = e.p.Get(e.as("bob"), e.ref("bob", id))
	if err != nil || len(got.Previews) != 1 || got.Previews[0].Image == nil || got.Previews[0].Image.Alt != "site image" ||
		e.mediaID("bob", got.Previews[0].Image.Ref) != e.mediaID("alice", img.Ref) {
		t.Fatalf("preview image: %+v %v", got.Previews, err)
	}

	// Once the post is deleted, its media URL is the uniform not-found.
	bobRef := got.Media[0].Ref
	if err := e.p.Delete(e.as("alice"), pv.Ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.a.Media.URL(e.as("bob"), bobRef, "display"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("deleted post url: %v", err)
	}

	// A thread part carries its own media.
	m3 := e.upload("alice", "")
	parts, err := e.p.CreateThread(e.as("alice"), []posts.Draft{{Body: "one", Lang: "en"}, {Body: "two", Lang: "en",
		Media: []gate.Ref{m3.Ref}}})
	if err != nil || len(parts[0].Media) != 0 || len(parts[1].Media) != 1 {
		t.Fatalf("thread: %+v %v", parts, err)
	}
}

// Acceptance: another user's media ref cannot be attached; neither can media
// already attached, more than four, or anything once media is off.
func TestMediaAttachRefused(t *testing.T) {
	e := newEnv(t)
	e.withMedia()
	mine, theirs := e.upload("alice", ""), e.upload("bob", "")
	before := e.count("SELECT count(*) FROM posts")
	for name, refs := range map[string][]gate.Ref{
		"bob's ref":          {theirs.Ref},
		"alice's ref to bob": {e.d.Refs.Media(e.id("alice"), e.mediaID("bob", theirs.Ref))},
		"garbage":            {"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"with a good one":    {mine.Ref, theirs.Ref},
		"duplicate":          {mine.Ref, mine.Ref},
	} {
		// The ref is resolved before the empty body is a Problem.
		_, err := e.p.Create(e.as("alice"), posts.Draft{Body: "", Lang: "en", Media: refs})
		if name == "duplicate" || name == "alice's ref to bob" {
			_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Media: refs})
		}
		if !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := e.count("SELECT count(*) FROM posts"); n != before {
		t.Fatalf("%d posts written", n-before)
	}
	if e.count("SELECT count(*) FROM media WHERE attached_at IS NOT NULL") != 0 {
		t.Fatal("a refused post attached media")
	}

	if _, err := e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Media: []gate.Ref{mine.Ref}}); err != nil {
		t.Fatal(err)
	}
	_, err := e.p.Create(e.as("alice"), posts.Draft{Body: "again", Lang: "en", Media: []gate.Ref{mine.Ref}})
	wantNotFound(t, err)

	five := make([]gate.Ref, 5)
	for i := range five {
		five[i] = e.upload("alice", "").Ref
	}
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Media: five})
	wantProblem(t, err, "too_many_media")
	if _, err := e.p.Create(e.as("alice"), posts.Draft{Body: "four", Lang: "en", Media: five[:4]}); err != nil {
		t.Fatal(err)
	}
}

func TestMediaDisabled(t *testing.T) {
	a := wiretest.NewApp(t, wiretest.WithEnv("PCC_SCANNER", "none"))
	w := wiretest.World(t, a)
	alice := w.User("alice")
	ctx := core.WithViewer(context.Background(), core.Viewer{ID: alice, Kind: core.SessUser, AuthAt: a.Deps().Clock.Now()})
	_, err := a.Posts.Create(ctx, posts.Draft{Body: "x", Lang: "en", Media: []gate.Ref{a.Deps().Refs.Media(alice, uuid.New())}})
	var de *gate.DisabledError
	if !errors.As(err, &de) || de.Feature != "media" {
		t.Fatalf("disabled: %v", err)
	}
	if n := countRows(t, a, "SELECT count(*) FROM posts"); n != 0 {
		t.Fatalf("%d posts", n)
	}
}

func countRows(t *testing.T, a *wire.App, sql string) int {
	t.Helper()
	var n int
	if err := a.Deps().DB.Q().QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Acceptance: a friend_plus post is hidden from plain friends (and the author's
// friends of friends); friend+ friends see it.
func TestFriendPlusAudience(t *testing.T) {
	e := newEnv(t)
	e.w.User("gina")
	e.w.FriendsPlus("alice", "gina")
	pv, err := e.p.Create(e.as("alice"), posts.Draft{Body: "close friends", Lang: "en", Audience: "friend_plus"})
	if err != nil || pv.Audience != "friend_plus" {
		t.Fatalf("create: %+v %v", pv, err)
	}
	id := e.pid("alice", pv)
	if got, err := e.p.Get(e.as("gina"), e.ref("gina", id)); err != nil || got.Audience != "friend_plus" {
		t.Fatalf("gina: %+v %v", got, err)
	}
	for _, h := range []string{"bob", "carol"} {
		_, err := e.p.Get(e.as(h), e.ref(h, id))
		wantNotFound(t, err)
	}
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Audience: "friends_plus"})
	wantProblem(t, err, "audience_invalid")
}
