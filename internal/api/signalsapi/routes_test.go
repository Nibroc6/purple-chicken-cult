package signalsapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/signalsapi"
)

func TestSignalsReport(t *testing.T) {
	h := apitest.Service(t, "signals", signalsapi.Register)
	if rec := h.Do(http.MethodPost, "/api/signals", map[string]any{"webdriver": true}); rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	h.Login("alice")
	rec := h.Do(http.MethodPost, "/api/signals", map[string]any{"webdriver": true, "cdp": false,
		"inputs_per_min": []int{3, 0, 12}, "visible_ms": 180000})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []map[string]any{
		{"inputs_per_min": []int{-1}},
		{"visible_ms": -5},
		{"mouse": []int{1, 2}}, // no pointer data, ever
	} {
		if rec := h.Raw(http.MethodPost, "/api/signals", bad, nil); rec.Code != http.StatusBadRequest ||
			!strings.Contains(rec.Body.String(), "bad_request") {
			t.Fatalf("%v: %d %s", bad, rec.Code, rec.Body)
		}
	}
}

// Machine-speed chain expansion over two minutes from a browser reporting a
// DevTools-protocol hint steps the session up: every request then answers
// step_up_required, the session says so, and nothing else changes.
func TestSignalStepUpOverHTTP(t *testing.T) {
	h := apitest.App(t)
	h.Login("alice")
	if rec := h.Do(http.MethodPost, "/api/signals", map[string]any{"cdp": true}); rec.Code != http.StatusNoContent {
		t.Fatalf("report: %d", rec.Code)
	}
	for m := range 2 {
		if m > 0 {
			if err := h.App.Hooks().AdvanceClock(60e9); err != nil {
				t.Fatal(err)
			}
		}
		for range 30 {
			h.Raw(http.MethodGet, "/api/friends", nil, nil)
		}
	}
	rec := h.Raw(http.MethodGet, "/api/friends", nil, nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "step_up_required") {
		t.Fatalf("after the pattern: %d %s", rec.Code, rec.Body)
	}
	if rec := h.Do(http.MethodGet, "/api/session", nil); !strings.Contains(rec.Body.String(), `"step_up":true`) {
		t.Fatalf("session %s", rec.Body)
	}
}
