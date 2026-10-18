package mediaapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/mediaapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/router"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/media"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
)

// Images are generated patterns; the CSAM match is a synthetic digest.

func pngOf(w, h int, seed uint8) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x) ^ seed, G: uint8(y), B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

type fixture struct {
	h            *apitest.Harness
	dev          *scan.Dev
	blobs        *blob.Mem
	cookie, csrf string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "media", mediaapi.Register)
	dev, _ := scan.NewDev("")
	f := &fixture{h: h, dev: dev, blobs: blob.NewMem("media")}
	h.App.Media.UseDrivers(media.Drivers{Scanner: dev, Blobs: f.blobs, Evidence: blob.NewMem("evidence"), Purger: &blob.Recorder{}})
	return f
}

func (f *fixture) login(t *testing.T, handle string) {
	t.Helper()
	f.h.Login(handle)
	f.cookie, f.csrf = wiretest.Login(t, f.h.App, handle)
}

// upload POSTs a multipart body (alt after the file) as the signed-in user.
func (f *fixture) upload(t *testing.T, img []byte, alt string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.png")
	_, _ = fw.Write(img)
	_ = mw.WriteField("alt", alt)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", f.h.Origin)
	if f.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: f.cookie}) //nolint:gosec // test request
		req.Header.Set("X-PCC-CSRF", f.csrf)
	}
	rec := httptest.NewRecorder()
	f.h.Handler.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error   string `json:"error"`
		Feature string `json:"feature"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}

func TestRoutesAnonymousNotFound(t *testing.T) {
	f := newFixture(t)
	if rec := f.upload(t, pngOf(4, 4, 1), ""); rec.Code != http.StatusNotFound || rec.Body.String() != gate.NotFoundJSON {
		t.Fatalf("upload: %d %q", rec.Code, rec.Body)
	}
	if rec := f.h.Do(http.MethodGet, "/api/media/garbage/url?variant=thumb", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("url: %d", rec.Code)
	}
}

// Upload → signed URL → the dev media origin serves it with the media-origin
// headers; unsigned, tampered or expired URLs are 403.
func TestUploadURLServe(t *testing.T) {
	f := newFixture(t)
	f.login(t, "alice")
	rec := f.upload(t, pngOf(40, 30, 2), "a test pattern")
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload %d %s", rec.Code, rec.Body)
	}
	var mv gate.MediaView
	if err := json.Unmarshal(rec.Body.Bytes(), &mv); err != nil || mv.W != 40 || mv.H != 30 || mv.Alt != "a test pattern" {
		t.Fatalf("view %s", rec.Body)
	}
	rec = f.h.Do(http.MethodGet, "/api/media/"+string(mv.Ref)+"/url?variant=display", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("url %d %s", rec.Code, rec.Body)
	}
	var out mediaapi.URLOut
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	u, err := url.Parse(out.URL)
	if err != nil || !strings.HasPrefix(u.Path, "/m/") {
		t.Fatalf("signed url %q", out.URL)
	}
	get := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		f.h.Handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, target, nil))
		return r
	}
	ok := get(u.RequestURI())
	if ok.Code != http.StatusOK || ok.Header().Get("Content-Type") != "image/webp" ||
		ok.Header().Get("X-Content-Type-Options") != "nosniff" ||
		ok.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" ||
		ok.Header().Get("Content-Disposition") != "inline" || ok.Body.Len() == 0 {
		t.Fatalf("serve %d %v", ok.Code, ok.Header())
	}
	if r := get(u.Path); r.Code != http.StatusForbidden {
		t.Fatalf("unsigned: %d", r.Code)
	}
	q := u.Query()
	q.Set("v", "AAAAAAAA")
	if r := get(u.Path + "?" + q.Encode()); r.Code != http.StatusForbidden {
		t.Fatalf("other viewer tag: %d", r.Code)
	}
	if err := f.h.App.Hooks().AdvanceClock(2 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if r := get(u.RequestURI()); r.Code != http.StatusForbidden {
		t.Fatalf("expired: %d", r.Code)
	}
	// Bad variant, someone else's media.
	if rec := f.h.Do(http.MethodGet, "/api/media/"+string(mv.Ref)+"/url?variant=huge", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("variant %d", rec.Code)
	}
	f.login(t, "bob")
	if rec := f.h.Do(http.MethodGet, "/api/media/"+string(mv.Ref)+"/url?variant=thumb", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob: %d", rec.Code)
	}
}

func TestUploadProblems(t *testing.T) {
	f := newFixture(t)
	f.login(t, "alice")
	if rec := f.upload(t, []byte("not an image"), ""); rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "media_type" {
		t.Fatalf("garbage %d %s", rec.Code, rec.Body)
	}
	big := make([]byte, media.MaxUploadBytes+128<<10)
	if rec := f.upload(t, big, ""); rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "media_too_large" {
		t.Fatalf("too large %d %s", rec.Code, rec.Body)
	}
	bad := pngOf(20, 20, 9)
	f.dev.Block(bad)
	if rec := f.upload(t, bad, ""); rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "media_rejected" {
		t.Fatalf("match %d %s", rec.Code, rec.Body)
	}
	// The account is held: the next clean upload is rejected the same way.
	if rec := f.upload(t, pngOf(8, 8, 1), ""); errCode(t, rec) != "media_rejected" {
		t.Fatalf("held %d %s", rec.Code, rec.Body)
	}
	if len(f.blobs.Keys()) != 0 {
		t.Fatal("nothing reached the media store")
	}
}

// Without a usable scanner media is feature_disabled (fail closed).
func TestDisabled(t *testing.T) {
	a := wiretest.NewApp(t, wiretest.WithEnv("PCC_SCANNER", "none"))
	cfg := a.Config()
	d := api.FromApp(a, cfg, slog.New(slog.DiscardHandler), fstest.MapFS{"index.html": {Data: []byte(apitest.TestIndexHTML)}})
	f := &fixture{h: &apitest.Harness{T: t, App: a, Handler: router.NewWith(d, mediaapi.Register), Origin: cfg.Origin}}
	f.cookie, f.csrf = wiretest.Login(t, a, "alice")
	rec := f.upload(t, pngOf(4, 4, 1), "")
	if rec.Code != http.StatusConflict || rec.Body.String() != `{"error":"feature_disabled","feature":"media"}`+"\n" {
		t.Fatalf("disabled %d %q", rec.Code, rec.Body)
	}
}
