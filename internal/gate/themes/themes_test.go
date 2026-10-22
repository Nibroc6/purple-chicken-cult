package themes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

type imgEnv struct {
	t *testing.T
	a *wire.App
	w *wiretest.TestWorld
}

func newImgEnv(t *testing.T) *imgEnv {
	t.Helper()
	a := wiretest.NewApp(t)
	dev, err := scan.NewDev("")
	if err != nil {
		t.Fatal(err)
	}
	a.Media.UseDrivers(media.Drivers{Scanner: dev, Blobs: blob.NewMem("media"), Evidence: blob.NewMem("evidence"), Purger: &blob.Recorder{}})
	e := &imgEnv{t: t, a: a, w: wiretest.World(t, a)}
	e.w.User("alice")
	e.w.User("bob")
	return e
}

func (e *imgEnv) as(h string) context.Context {
	return core.WithViewer(context.Background(), core.Viewer{ID: e.w.ID(h), Kind: core.SessUser, AuthAt: e.a.Deps().Clock.Now()})
}

func (e *imgEnv) upload(h string, seed uint8) gate.Ref {
	e.t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.NRGBA{R: uint8(x) ^ seed, G: uint8(y), B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	mv, err := e.a.Media.Upload(e.as(h), bytes.NewReader(buf.Bytes()), int64(buf.Len()), "image/png", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return mv.Ref
}

func (e *imgEnv) live() int {
	e.t.Helper()
	var n int
	if err := e.a.Deps().DB.Q().QueryRow(context.Background(),
		`SELECT count(*) FROM media WHERE deleted_at IS NULL AND attached_at IS NOT NULL`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestThemeImages(t *testing.T) {
	e := newImgEnv(t)
	ts := e.a.Themes
	ctx := e.as("alice")
	heart, logo, unused := e.upload("alice", 1), e.upload("alice", 2), e.upload("alice", 3)
	css := "[data-part=like] { background-image: image(heart) }\n.brand { background-image: image(logo) }"
	v, err := ts.Set(ctx, themes.Theme{Tokens: json.RawMessage(good), CSS: css, Images: []themes.ImageIn{
		{Name: "heart", Ref: heart}, {Name: "logo", Ref: logo}, {Name: "spare", Ref: unused},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Images) != 2 || v.Images[0].Name != "heart" || v.Images[1].Name != "logo" || v.Images[0].W != 32 {
		t.Fatalf("images = %+v (the unused upload stays unattached)", v.Images)
	}
	if e.live() != 2 {
		t.Fatalf("attached = %d, want 2", e.live())
	}

	// Saving again with the current refs keeps them; dropping logo releases it.
	v, err = ts.Set(ctx, themes.Theme{Tokens: json.RawMessage(good), CSS: "[data-part=like] { background-image: image(heart) }",
		Images: []themes.ImageIn{{Name: "heart", Ref: v.Images[0].Ref}, {Name: "logo", Ref: v.Images[1].Ref}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Images) != 1 || v.Images[0].Name != "heart" || e.live() != 1 {
		t.Fatalf("images = %+v, attached = %d", v.Images, e.live())
	}

	// Another user's upload, or a ref minted for someone else, is the uniform not-found.
	bobs := e.upload("bob", 4)
	if _, err := ts.Set(ctx, themes.Theme{Tokens: json.RawMessage(good), CSS: "[data-part=like] { background-image: image(x) }",
		Images: []themes.ImageIn{{Name: "x", Ref: bobs}}}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("bob's upload: %v", err)
	}
	// A ref minted for alice does not open for bob.
	if _, err := ts.Set(e.as("bob"), themes.Theme{Tokens: json.RawMessage(good), CSS: "[data-part=like] { background-image: image(x) }",
		Images: []themes.ImageIn{{Name: "x", Ref: v.Images[0].Ref}}}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("alice's ref as bob: %v", err)
	}
	if got, _ := ts.Get(ctx); got == nil || len(got.Images) != 1 {
		t.Fatalf("a refused save changed the theme: %+v", got)
	}

	// Duplicate and malformed names.
	for _, ims := range [][]themes.ImageIn{
		{{Name: "a", Ref: heart}, {Name: "a", Ref: logo}},
		{{Name: "Bad Name", Ref: heart}},
	} {
		if _, err := ts.Set(ctx, themes.Theme{Tokens: json.RawMessage(good), Images: ims}); problem(t, err).Field != "images" {
			t.Errorf("images %+v: %v", ims, err)
		}
	}

	// Clearing the theme releases its images.
	if err := ts.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if e.live() != 0 {
		t.Fatalf("attached after clear = %d", e.live())
	}
}

func TestThemeImagesNeedMedia(t *testing.T) {
	a := wiretest.NewApp(t)
	a.Deps().ThemeMedia = nil // a build without theme media
	w := wiretest.World(t, a)
	w.User("alice")
	ctx := core.WithViewer(context.Background(), core.Viewer{ID: w.ID("alice"), Kind: core.SessUser, AuthAt: a.Deps().Clock.Now()})
	_, err := a.Themes.Set(ctx, themes.Theme{Tokens: json.RawMessage(good), CSS: "[data-part=like] { background-image: image(x) }",
		Images: []themes.ImageIn{{Name: "x", Ref: "nope"}}})
	var d *gate.DisabledError
	if !errors.As(err, &d) {
		t.Fatalf("err = %v, want feature_disabled", err)
	}
	// Theme CSS without images needs no media.
	v, err := a.Themes.Set(ctx, themes.Theme{Tokens: json.RawMessage(good), CSS: ".post { border-radius: 0 }"})
	if err != nil || v.CSS != ".post { border-radius: 0 }" || len(v.Images) != 0 {
		t.Fatalf("v = %+v, err = %v", v, err)
	}
}
