package cosignapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/cosignapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/cosign"
)

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

// The intent itself (200) is covered end to end in gate/cosign; here: the
// route's uniform failures, validated against the fragment.
func TestBeginIntent(t *testing.T) {
	h := apitest.Service(t, "cosign", cosignapi.Register)
	in := map[string]any{"purpose": cosign.PurposeBlock}
	if rec := h.Do(http.MethodPost, "/api/cosign", in); rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}
	h.Login("alice")
	if rec := h.Do(http.MethodPost, "/api/cosign", in); rec.Code != http.StatusNotFound {
		t.Fatalf("no drafter: %d %s", rec.Code, rec.Body)
	}
	rec := h.Raw(http.MethodPost, "/api/cosign", map[string]any{"purpose": "nope"}, nil)
	if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "bad_request" {
		t.Fatalf("unknown purpose: %d %s", rec.Code, rec.Body)
	}
}
