// Package modapi serves /api/reports, /api/blame, /api/mod/* (queue, resolve,
// confirm, log) and /api/csp-report (plan §5.3).
//
// Owner: P0-W12 → P1-W06.
package modapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

var (
	wReport = apix.Write("report")
	wSocial = apix.Write("social")
	rGraph  = apix.Read("graph_read")
	aCSP    = apix.Anon("csp_report")
)

// Body limits.
const (
	// ReportMax bounds POST /reports (a 1,000-code-point note is ≤ 4 KiB).
	ReportMax = 16 << 10
	// CSPMax bounds one CSP report upload.
	CSPMax = 64 << 10
	// CSPPerUpload is how many violations of one upload are counted.
	CSPPerUpload = 20
)

// ReportTarget is the target of POST /api/reports.
type ReportTarget struct {
	Kind string   `json:"kind"` // post | person | inviter (the invitee's ref) | preview
	Ref  gate.Ref `json:"ref"`
}

// ReportIn is the body of POST /api/reports.
type ReportIn struct {
	Target ReportTarget `json:"target"`
	Reason string       `json:"reason"`
	Note   string       `json:"note,omitempty"`
}

// ResolveIn is the body of POST /api/mod/items/{id}/resolve.
type ResolveIn struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// Register registers the module routes on the /api router.
func Register(r chi.Router, d *api.Deps) {
	m := d.Moderation
	apix.Route(r, http.MethodPost, "/reports", wReport, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ReportIn](r, ReportMax)
		if err != nil {
			return err
		}
		if err := m.Report(r.Context(), in.Target.Kind, in.Target.Ref, in.Reason, in.Note); err != nil {
			return err
		}
		return apix.JSON(w, http.StatusAccepted, nil)
	})
	apix.Route(r, http.MethodGet, "/blame", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		page, err := m.BlameInbox(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodGet, "/mod/queue", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		page, err := m.Queue(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodPost, "/mod/items/{id}/resolve", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[ResolveIn](r, 0)
		if err != nil {
			return err
		}
		if err := m.Resolve(r.Context(), apix.Ref(r, "id"), in.Action, in.Reason); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/mod/items/{id}/confirm", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := m.Confirm(r.Context(), apix.Ref(r, "id")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodGet, "/mod/log", rGraph, func(w http.ResponseWriter, r *http.Request) error {
		page, err := m.Log(r.Context(), r.URL.Query().Get("cursor"))
		if err != nil {
			return err
		}
		return apix.JSON(w, http.StatusOK, page)
	})
	apix.Route(r, http.MethodPost, "/csp-report", aCSP, func(w http.ResponseWriter, r *http.Request) error {
		raw, err := apix.Decode[json.RawMessage](r, CSPMax)
		if err != nil {
			return err
		}
		for _, dir := range Directives(raw) {
			if err := m.CSPReport(r.Context(), dir); err != nil {
				return err
			}
		}
		return apix.NoContent(w)
	})
}

// cspBody holds the directive fields of both report formats: the Reporting API
// (application/reports+json: [{type, body: {effectiveDirective}}]) and the
// legacy report-uri format (application/csp-report: {"csp-report": {...}}).
type cspBody struct {
	EffectiveDirective  string `json:"effectiveDirective"`
	EffectiveDirective2 string `json:"effective-directive"`
	ViolatedDirective   string `json:"violated-directive"`
}

func (b cspBody) directive() string {
	for _, d := range []string{b.EffectiveDirective, b.EffectiveDirective2, b.ViolatedDirective} {
		if d != "" {
			return d
		}
	}
	return ""
}

// Directives extracts the violated directives of a CSP report upload (at most
// CSPPerUpload). Nothing else of the report (URLs, samples, positions) is read.
func Directives(raw json.RawMessage) []string {
	var out []string
	var list []struct {
		Type string  `json:"type"`
		Body cspBody `json:"body"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, rep := range list {
			if rep.Type != "csp-violation" {
				continue
			}
			if d := rep.Body.directive(); d != "" {
				out = append(out, d)
			}
			if len(out) == CSPPerUpload {
				break
			}
		}
		return out
	}
	var legacy struct {
		Report cspBody `json:"csp-report"`
	}
	if err := json.Unmarshal(raw, &legacy); err == nil {
		if d := legacy.Report.directive(); d != "" {
			out = append(out, d)
		}
	}
	return out
}
