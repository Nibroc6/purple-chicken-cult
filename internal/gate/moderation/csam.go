package moderation

import (
	"context"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// reportRefRE is the shape of a CyberTipline report reference (the same rule
// core.CSAMHolds.Report enforces).
var reportRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// CSAMReport starts the csam_report action of a CSAM queue item (plan §5.3,
// fidelity #35; deploy/runbooks/csam.md): the moderator who filed the
// CyberTipline report enters its reference, a second moderator confirms it
// within 24 h (Confirm), and only then is it recorded on the hold
// (core.CSAMHolds.Report extends the evidence retention to a year after the
// report). Needs a fresh step-up. Problems: action_invalid (not a csam hold),
// bad_request (report_ref), item_resolved, item_claimed, action_pending,
// csam_already_reported.
func (s *Service) CSAMReport(ctx context.Context, ref gate.Ref, reportRef string) error {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, ref); !ok {
		return gate.ErrNotFound
	}
	if v, err = s.freshModerator(ctx); err != nil {
		return err
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		it, err := s.lockItem(ctx, tx, v, ref)
		if err != nil {
			return err
		}
		if it.source != core.ModSrcCSAM || it.media == uuid.Nil || s.d.CSAM == nil {
			return gate.NewProblem(gate.StatusUnprocessable, "action_invalid", "action")
		}
		if !reportRefRE.MatchString(reportRef) {
			return gate.NewProblem(gate.StatusBadRequest, "bad_request", "report_ref")
		}
		if err := s.startable(ctx, tx, v, it, now); err != nil {
			return err
		}
		h, err := s.d.CSAM.Hold(ctx, it.media)
		if err != nil {
			return err
		}
		if h.ReportedAt != nil {
			return gate.NewProblem(gate.StatusConflict, "csam_already_reported")
		}
		return s.insertAction(ctx, tx, v, it, ActionCSAMReport, string(core.ReasonCSAM), it.subject, actionPending, now, &reportRef)
	})
}
