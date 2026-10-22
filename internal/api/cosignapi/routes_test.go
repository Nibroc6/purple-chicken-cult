package cosignapi_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/cosignapi"
)

func harness(t *testing.T) *apitest.Harness {
	t.Helper()
	return apitest.Service(t, "cosign", cosignapi.Register)
}

// withPasskey signs in as handle and gives the account one passkey.
func withPasskey(t *testing.T, h *apitest.Harness, handle string) {
	t.Helper()
	h.Login(handle)
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: b[1:33], -3: b[33:65]})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, ok := h.App.Users.ByHandle(ctx, handle)
	if !ok {
		t.Fatal("no user")
	}
	if _, err := h.App.Deps().DB.Q().Exec(ctx, `INSERT INTO webauthn_credentials (id, user_id, public_key, alg, sign_count,
		aaguid, transports, backup_eligible, backup_state, name, created_at)
		VALUES ($1, $2, $3, -7, 0, $4, '{}', false, false, 'test', now())`,
		[]byte("cred-"+handle), int64(id), cose, make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
}

func TestBeginIntent(t *testing.T) {
	h := harness(t)
	if rec := h.Do(http.MethodPost, "/api/cosign", map[string]any{"purpose": "cosign_check"}); rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	withPasskey(t, h, "alice")
	rec := h.Do(http.MethodPost, "/api/cosign", map[string]any{"purpose": "cosign_check"})
	if rec.Code != http.StatusOK {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Intent  string          `json:"intent"`
		Record  json.RawMessage `json:"record"`
		Options json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Intent == "" ||
		!strings.Contains(string(out.Record), `"cosign":{`) || !strings.Contains(string(out.Options), `"challenge"`) {
		t.Fatalf("intent %s %v", rec.Body, err)
	}
	for _, body := range []map[string]any{{"purpose": "nope"}, {"purpose": ""}, {"purpose": "friend_request", "target": "x"}} {
		if rec := h.Do(http.MethodPost, "/api/cosign", body); rec.Code != http.StatusNotFound {
			t.Errorf("%v: %d", body, rec.Code)
		}
	}
	if rec := h.Do(http.MethodPost, "/api/cosign", map[string]any{"purpose": "cosign_check", "bogus": 1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
}
