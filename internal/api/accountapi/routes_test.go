package accountapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/accountapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
)

func harness(t *testing.T) *apitest.Harness {
	t.Helper()
	return apitest.Service(t, "account", accountapi.Register)
}

func advance(t *testing.T, h *apitest.Harness, d time.Duration) {
	t.Helper()
	if err := h.App.Hooks().AdvanceClock(d); err != nil {
		t.Fatal(err)
	}
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("error body %s: %v", b, err)
	}
	return e.Error
}

func TestAnonymousGetsUniformNotFound(t *testing.T) {
	h := harness(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/me"}, {http.MethodGet, "/api/keys"}, {http.MethodGet, "/api/me/recovery"},
		{http.MethodPost, "/api/me/recovery/cancel"},
	} {
		rec := h.Do(c.method, c.path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d", c.method, c.path, rec.Code)
		}
	}
}

func TestMeSettingsAndKeys(t *testing.T) {
	h := harness(t)
	h.Login("alice")
	rec := h.Do(http.MethodGet, "/api/me", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"handle":"alice"`) {
		t.Fatalf("me %d %s", rec.Code, rec.Body)
	}
	for body, code := range map[string]string{
		`{"translate_to":"de"}`:              "translate_to_not_in_languages",
		`{"rank_preset":"Top"}`:              "preset_unavailable",
		`{"ui_prefs":{"format":"explorer"}}`: "ui_prefs_invalid",
		`{"display_name":"Alice ✓"}`:         "display_name_invalid",
		`{"languages":["en","not a tag"]}`:   "lang_invalid",
		`{"ui_locale":"--"}`:                 "locale_invalid",
	} {
		var in map[string]any
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			t.Fatal(err)
		}
		rec := h.Do(http.MethodPatch, "/api/me/settings", in)
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec.Body.Bytes()) != code {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec = h.Do(http.MethodPatch, "/api/me/settings", map[string]any{"bogus": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	rec = h.Do(http.MethodPatch, "/api/me/settings", map[string]any{
		"languages": []string{"en", "de"}, "translate_to": "de", "rank_preset": "latest",
		"ui_prefs": map[string]any{"format": "compact", "a11y": map[string]any{"text_scale": "large"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch %d %s", rec.Code, rec.Body)
	}
	var sv struct {
		TranslateTo string         `json:"translate_to"`
		RankPreset  string         `json:"rank_preset"`
		UIPrefs     map[string]any `json:"ui_prefs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sv); err != nil || sv.TranslateTo != "de" || sv.RankPreset != "latest" || sv.UIPrefs["format"] != "compact" {
		t.Fatalf("settings %s %v", rec.Body, err)
	}

	rec = h.Do(http.MethodGet, "/api/keys", nil)
	var keys accountapi.KeysView
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &keys) != nil || len(keys.Server) != 1 || len(keys.Server[0].Fingerprint) != 64 {
		t.Fatalf("keys %d %s", rec.Code, rec.Body)
	}
	rec = h.Do(http.MethodGet, "/api/me/recovery", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("recovery %d %s", rec.Code, rec.Body)
	}
	if rec := h.Do(http.MethodPost, "/api/me/recovery/cancel", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel %d", rec.Code)
	}
}

// Acceptance (HTTP): export/delete without fresh auth ⇒ step_up_required.
func TestExportAndDeleteOverHTTP(t *testing.T) {
	h := harness(t)
	h.Login("alice")
	rec := h.Do(http.MethodPost, "/api/me/export", map[string]any{})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("export %d %v", rec.Code, rec.Header())
	}
	z := rec.Body.Bytes()
	if _, err := zip.NewReader(bytes.NewReader(z), int64(len(z))); err != nil {
		t.Fatal(err)
	}
	rep, err := records.VerifyExport(bytes.NewReader(z), int64(len(z)), nil)
	if err != nil || len(rep.Failures) != 0 {
		t.Fatalf("verify %+v %v", rep, err)
	}

	advance(t, h, 6*time.Minute)
	rec = h.Do(http.MethodPost, "/api/me/export", map[string]any{})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec.Body.Bytes()) != "step_up_required" {
		t.Fatalf("stale export %d %s", rec.Code, rec.Body)
	}
	rec = h.Do(http.MethodPost, "/api/me/delete", map[string]any{"confirm_handle": "alice"})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec.Body.Bytes()) != "step_up_required" {
		t.Fatalf("stale delete %d %s", rec.Code, rec.Body)
	}

	h.Login("alice") // a fresh session
	rec = h.Do(http.MethodPost, "/api/me/delete", map[string]any{"confirm_handle": "bob"})
	if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec.Body.Bytes()) != "confirm_mismatch" {
		t.Fatalf("mismatch %d %s", rec.Code, rec.Body)
	}
	rec = h.Do(http.MethodPost, "/api/me/delete", map[string]any{"confirm_handle": "alice"})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("delete %d %v", rec.Code, rec.Header())
	}
	// Every session is gone: the API no longer knows the caller.
	if rec := h.Do(http.MethodGet, "/api/me", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("me after delete %d", rec.Code)
	}
	if err := h.App.DrainJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModRecoveryRoutes(t *testing.T) {
	h := harness(t)
	h.Login("alice")
	// Members do not see the moderator surface.
	rec := h.Do(http.MethodPost, "/api/mod/recovery", map[string]any{"subject": "AAAA"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("member request %d", rec.Code)
	}
	for _, p := range []string{"/api/mod/recovery/AAAA/approve"} {
		if rec := h.Do(http.MethodPost, p, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("member %s %d", p, rec.Code)
		}
	}
	if rec := h.Do(http.MethodGet, "/api/mod/recovery/AAAA/link", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("member link %d", rec.Code)
	}
}
