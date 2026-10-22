package modapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

type p1QueueOut struct {
	Items []gate.ModItem `json:"items"`
}

// The Phase 1 moderator routes: claim, confirm with and without a body,
// csam-report and quarantine, validated against the fragment; members get
// the uniform 404 everywhere.
func TestP1Routes(t *testing.T) {
	f := newModFixture(t)
	ctx := context.Background()
	d := f.h.App.Deps()
	f.h.Login("carol")
	f.do(t, http.MethodPost, "/api/reports", report("person", f.person("carol", "bob"), "bot"), http.StatusAccepted)
	for _, p := range []string{"/api/mod/items/garbage/claim", "/api/mod/items/garbage/confirm"} {
		f.do(t, http.MethodPost, p, nil, http.StatusNotFound)
	}
	f.do(t, http.MethodPost, "/api/mod/quarantine", map[string]string{"subject_item": "garbage"}, http.StatusNotFound)
	f.do(t, http.MethodPost, "/api/mod/items/garbage/csam-report", map[string]string{"report_ref": "CT-1"}, http.StatusNotFound)

	f.h.Login("mo1")
	q := decode[p1QueueOut](t, f.do(t, http.MethodGet, "/api/mod/queue", nil, http.StatusOK))
	if len(q.Items) != 1 || q.Items[0].Source != "report" || q.Items[0].Claimed {
		t.Fatalf("queue %+v", q)
	}
	id := string(q.Items[0].ID)
	f.do(t, http.MethodPost, "/api/mod/items/"+id+"/claim", nil, http.StatusNoContent)
	q = decode[p1QueueOut](t, f.do(t, http.MethodGet, "/api/mod/queue", nil, http.StatusOK))
	if !q.Items[0].ClaimedByMe || q.Items[0].Summary["subtree_confirmed"] != 0 || q.Items[0].Summary["quarantine_k"] != 3 {
		t.Fatalf("claimed %+v", q.Items[0])
	}
	// Not a csam item; below the quarantine threshold.
	f.do(t, http.MethodPost, "/api/mod/items/"+id+"/csam-report", map[string]string{"report_ref": "CT-1"}, http.StatusUnprocessableEntity)
	b := f.do(t, http.MethodPost, "/api/mod/quarantine", map[string]string{"subject_item": id}, http.StatusConflict)
	if !strings.Contains(string(b), "quarantine_threshold") {
		t.Fatalf("%s", b)
	}
	f.do(t, http.MethodPost, "/api/mod/quarantine", map[string]any{"subject_item": id, "extra": 1}, http.StatusBadRequest)
	f.do(t, http.MethodPost, "/api/mod/items/"+id+"/resolve", map[string]string{"action": "suspend_user", "reason": "bot"}, http.StatusNoContent)

	f.h.Login("mo2")
	q = decode[p1QueueOut](t, f.do(t, http.MethodGet, "/api/mod/queue", nil, http.StatusOK))
	id = string(q.Items[0].ID)
	if q.Items[0].Pending == nil || q.Items[0].Pending.Action != "suspend_user" || q.Items[0].Pending.Mine || !q.Items[0].Claimed {
		t.Fatalf("pending %+v", q.Items[0])
	}
	// A confirmation body must be well formed; an empty one is the P0 path.
	rec := f.h.Raw(http.MethodPost, "/api/mod/items/"+id+"/confirm", map[string]any{"bogus": true}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus body %d", rec.Code)
	}
	f.do(t, http.MethodPost, "/api/mod/items/"+id+"/confirm", map[string]any{}, http.StatusNoContent)
	var status int
	if err := d.DB.Q().QueryRow(ctx, "SELECT status FROM users WHERE id = $1", int64(f.w.ID("bob"))).Scan(&status); err != nil || status != 2 {
		t.Fatalf("bob %d %v", status, err)
	}
	q = decode[p1QueueOut](t, f.do(t, http.MethodGet, "/api/mod/queue", nil, http.StatusOK))
	if len(q.Items) != 0 {
		t.Fatalf("queue after %+v", q)
	}
}
