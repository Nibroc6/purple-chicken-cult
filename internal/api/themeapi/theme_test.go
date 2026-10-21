package themeapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/themeapi"
)

func harness(t *testing.T) *apitest.Harness {
	t.Helper()
	return apitest.Service(t, "theme", func(r chi.Router, d *api.Deps) { themeapi.Register(r, d) })
}

func do(t *testing.T, h *apitest.Harness, method, path string, body any, want int) []byte {
	t.Helper()
	rec := h.Do(method, path, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec.Body.Bytes()
}

func colors(integrityText string) map[string]string {
	return map[string]string{
		"bg": "#F7F3EA", "surface": "#fffdf8", "text": "#2b2620", "muted": "#5f574b", "accent": "#8a3b12",
		"border": "#e4dccb", "integrity_bg": "#efe5d0", "integrity_text": integrityText,
	}
}

type out struct {
	Theme *struct {
		Tokens struct {
			Colors map[string]string `json:"colors"`
			Font   string            `json:"font"`
		} `json:"tokens"`
		UpdatedAt string `json:"updated_at"`
	} `json:"theme"`
}

func get(t *testing.T, h *apitest.Harness) out {
	t.Helper()
	var o out
	if err := json.Unmarshal(do(t, h, http.MethodGet, "/api/me/theme", nil, http.StatusOK), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestSetGetClear(t *testing.T) {
	h := harness(t)
	do(t, h, http.MethodGet, "/api/me/theme", nil, http.StatusNotFound) // signed out: the uniform 404
	h.Login("alice")
	if o := get(t, h); o.Theme != nil {
		t.Fatalf("default theme = %+v", o.Theme)
	}
	var put out
	body := do(t, h, http.MethodPut, "/api/me/theme", map[string]any{"tokens": map[string]any{"colors": colors("#4a2a05"), "font": "serif", "radius": 4}}, http.StatusOK)
	if err := json.Unmarshal(body, &put); err != nil || put.Theme == nil {
		t.Fatalf("put = %s (%v)", body, err)
	}
	if put.Theme.Tokens.Colors["bg"] != "#f7f3ea" || put.Theme.Tokens.Font != "serif" {
		t.Fatalf("not normalized: %s", body)
	}
	if o := get(t, h); o.Theme == nil || o.Theme.Tokens.Colors["integrity_text"] != "#4a2a05" || o.Theme.UpdatedAt != put.Theme.UpdatedAt {
		t.Fatalf("get after put = %+v", o.Theme)
	}

	// A later theme replaces it; nobody else sees it.
	do(t, h, http.MethodPut, "/api/me/theme", map[string]any{"tokens": map[string]any{"colors": colors("#000000")}}, http.StatusOK)
	if o := get(t, h); o.Theme.Tokens.Colors["integrity_text"] != "#000000" || o.Theme.Tokens.Font != "" {
		t.Fatalf("replaced theme = %+v", o.Theme)
	}
	h.Login("bob")
	if o := get(t, h); o.Theme != nil {
		t.Fatalf("bob sees %+v", o.Theme)
	}
	h.Login("alice")
	do(t, h, http.MethodDelete, "/api/me/theme", nil, http.StatusNoContent)
	if o := get(t, h); o.Theme != nil {
		t.Fatalf("after delete = %+v", o.Theme)
	}
	do(t, h, http.MethodDelete, "/api/me/theme", nil, http.StatusNoContent) // idempotent
}

func TestRejections(t *testing.T) {
	h := harness(t)
	h.Login("alice")
	for _, c := range []struct {
		name string
		body any
		want int
		code string
	}{
		{"low-contrast tier header", map[string]any{"tokens": map[string]any{"colors": colors("#c9b991")}}, 422, `{"error":"theme_contrast","field":"colors.integrity_text"}`},
		{"font", map[string]any{"tokens": map[string]any{"colors": colors("#4a2a05"), "font": "Papyrus"}}, 422, `{"error":"theme_font","field":"font"}`},
		{"size floor", map[string]any{"tokens": map[string]any{"colors": colors("#4a2a05"), "font_size": 11}}, 422, `{"error":"theme_invalid","field":"font_size"}`},
		{"unknown token", map[string]any{"tokens": map[string]any{"colors": colors("#4a2a05"), "css": "*{}"}}, 422, `{"error":"theme_invalid","field":"tokens"}`},
		{"no tokens", map[string]any{}, 422, `{"error":"theme_invalid","field":"tokens"}`},
		{"unknown body field", map[string]any{"tokens": map[string]any{"colors": colors("#4a2a05")}, "x": 1}, 400, `{"error":"bad_request"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := strings.TrimSpace(string(do(t, h, http.MethodPut, "/api/me/theme", c.body, c.want)))
			if got != c.code {
				t.Fatalf("body = %s, want %s", got, c.code)
			}
		})
	}
	if o := get(t, h); o.Theme != nil {
		t.Fatalf("a rejected theme was stored: %+v", o.Theme)
	}
}
