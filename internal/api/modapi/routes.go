// Package modapi serves /api/reports, /api/blame, /api/mod/* (queue, claim,
// resolve, confirm, quarantine, csam-report, log) and /api/csp-report (plan §5.3).
//
// Owner: P0-W12 → P1-W06.
package modapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/moderation"
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

// ConfirmIn is the optional body of POST /api/mod/items/{id}/confirm: the
// second moderator's co-signature (required for passkey sessions).
type ConfirmIn struct {
	Intent    gate.Ref        `json:"intent,omitempty"`
	Assertion json.RawMessage `json:"assertion,omitempty"`
}

// QuarantineIn is the body of POST /api/mod/quarantine.
type QuarantineIn struct {
	SubjectItem gate.Ref `json:"subject_item"`
	Reason      string   `json:"reason,omitempty"`
}

// CSAMReportIn is the body of POST /api/mod/items/{id}/csam-report.
type CSAMReportIn struct {
	ReportRef string `json:"report_ref"`
}

// ConfirmMax bounds a confirmation (a WebAuthn assertion is a few KiB).
const ConfirmMax = 64 << 10

// decodeOptional decodes an optional JSON body: none (or whitespace) is the
// zero value.
func decodeOptional[T any](r *http.Request, maxBytes int64) (T, error) {
	var zero T
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return zero, gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return zero, nil
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return apix.Decode[T](r, maxBytes)
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
	apix.Route(r, http.MethodPost, "/mod/items/{id}/claim", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		if err := m.Claim(r.Context(), apix.Ref(r, "id")); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/mod/items/{id}/confirm", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := decodeOptional[ConfirmIn](r, ConfirmMax)
		if err != nil {
			return err
		}
		var c *moderation.Cosig
		if in.Intent != "" || len(in.Assertion) > 0 {
			c = &moderation.Cosig{Intent: in.Intent, Assertion: in.Assertion}
		}
		if err := m.Confirm(r.Context(), apix.Ref(r, "id"), c); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/mod/items/{id}/csam-report", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[CSAMReportIn](r, 0)
		if err != nil {
			return err
		}
		if err := m.CSAMReport(r.Context(), apix.Ref(r, "id"), in.ReportRef); err != nil {
			return err
		}
		return apix.NoContent(w)
	})
	apix.Route(r, http.MethodPost, "/mod/quarantine", wSocial, func(w http.ResponseWriter, r *http.Request) error {
		in, err := apix.Decode[QuarantineIn](r, 0)
		if err != nil {
			return err
		}
		if err := m.Quarantine(r.Context(), in.SubjectItem, in.Reason); err != nil {
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
