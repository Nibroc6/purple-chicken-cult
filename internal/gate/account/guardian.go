package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Guardian recovery (§10.3 Phase 1, D10): recovery confirmed by two distinct
// friend+ friends of the account, without moderators.
//
//  1. Guardian A, in person with the recovering person, opens it for that
//     friend (POST /api/recovery/sessions): a recovery handshake
//     (core.Handshakes.OpenRecovery) and an open guardian request with A's
//     approval. There is no handle lookup for the unauthenticated side, so
//     nobody can start a recovery of an account they cannot vouch for.
//  2. The recovering device (a guest session) scans A's code and claims it
//     (POST /api/recovery/claim, core.Handshakes.ClaimRecovery). It gets a
//     recovery ticket: guest sessions last 30 minutes, the recovery a day or
//     more, so the device asks GET /api/recovery/status with the ticket
//     (X-PCC-Recovery) rather than its session. The SAS and the sound run
//     through /api/join/* as for an invitee.
//  3. Once the handshake's outcome passed (Accepted or Verified) the request is
//     vouched: the account's other friend+ friends are told
//     (recovery_approval) and see it in GET /api/recovery/requests.
//  4. A second guardian B (≠ A, ≠ the account, friend+ of the account)
//     approves with a passkey assertion over the RecoveryApproval record
//     (POST /api/recovery/requests/{id}/approve). The 24 h hold starts: every
//     live session of the account shows the banner with Cancel (GET
//     /api/me/recovery) and the account's friends get friend_security.
//  5. After the hold (recovery.release, shared with manual recovery, or the
//     device's next status call) every session of the account ends and the
//     device's status answers a one-time enrollment URL (/enroll#token,
//     purpose recovery: the new passkey starts a new chain, old passkeys can
//     be removed in the same flow).
//
// Anyone else asking about a request is the uniform not-found.

// TicketHeader carries the recovering device's ticket on GET /api/recovery/status.
const TicketHeader = "X-PCC-Recovery"

// Recovery states the recovering device sees.
const (
	StatusClaimed   = "claimed"   // waiting for the in-person handshake to pass
	StatusApproving = "approving" // vouched by guardian A, waiting for a second guardian
	StatusHolding   = "holding"   // approved; released at ReleaseAt unless cancelled
	StatusReleased  = "released"  // enroll at EnrollURL
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

// RecoveryStatus is GET /api/recovery/status.
type RecoveryStatus struct {
	State     string     `json:"state"`
	ReleaseAt *time.Time `json:"release_at,omitempty"`
	EnrollURL string     `json:"enroll_url,omitempty"`
}

// Claimed is the answer of POST /api/recovery/claim: the device's ticket.
type Claimed struct {
	Ticket string `json:"ticket"`
}

// PersonLabel names an account in a guardian's list.
type PersonLabel struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
}

// GuardianRequest is one recovery waiting for the viewer's approval.
type GuardianRequest struct {
	ID        gate.Ref    `json:"id"`
	Account   PersonLabel `json:"account"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// GuardianRequests is GET /api/recovery/requests.
type GuardianRequests struct {
	Items []GuardianRequest `json:"items"`
}

var b64 = base64.RawURLEncoding

// isGuardian reports whether g may vouch for or approve a recovery of account:
// an active friend+ of it.
func (s *Service) isGuardian(ctx context.Context, g, account core.UserID) bool {
	if g == 0 || account == 0 || g == account {
		return false
	}
	if lvl, ok := s.d.Graph.Level(g, account); !ok || lvl != core.LevelFriendPlus {
		return false
	}
	m, err := s.d.Users.Get(ctx, g)
	return err == nil && m.Status == core.StatusActive
}

// RecoveryStart opens a guardian recovery of friend by the viewer (guardian
// A): the in-person handshake the recovering device scans. Opening again
// before the device's handshake passed replaces the handshake. Problem
// recovery_exists when another recovery of the account is under way.
func (s *Service) RecoveryStart(ctx context.Context, friend gate.Ref) (gate.HandshakeView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	account, ok := s.d.Refs.OpenUser(v.ID, friend)
	if !ok || !s.isGuardian(ctx, v.ID, account) || s.d.Handshakes == nil {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var rid uuid.UUID
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := subjectLive(ctx, tx, account); err != nil {
			return err
		}
		var (
			id      uuid.UUID
			kind    int16
			vouched *time.Time
			created time.Time
			state   int16
		)
		err := tx.QueryRow(ctx, `SELECT id, kind, state, created_at, vouched_at FROM recovery_requests
			WHERE user_id = $1 AND state IN (0, 1) FOR UPDATE`, int64(account)).Scan(&id, &kind, &state, &created, &vouched)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case state == stateOpen && now.Sub(created) > OpenFor:
			// Never approved in time: it expires now and a new one starts.
			if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", id, stateExpired); err != nil {
				return err
			}
		default:
			var first int64
			if kind == kindGuardian && state == stateOpen && vouched == nil {
				if err := tx.QueryRow(ctx, `SELECT approver_id FROM recovery_approvals WHERE request_id = $1 AND role = $2
					ORDER BY at, approver_id LIMIT 1`, id, roleGuardian).Scan(&first); err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			if core.UserID(first) != v.ID {
				return gate.NewProblem(gate.StatusConflict, "recovery_exists")
			}
			rid = id
			_, err := tx.Exec(ctx, `UPDATE recovery_requests SET created_at = $2, handshake = NULL, claimed_session = NULL
				WHERE id = $1`, id, now)
			return err
		}
		rid = s.d.IDs.New()
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_requests (id, user_id, kind, state, created_at)
			VALUES ($1, $2, $3, $4, $5)`, rid, int64(account), kindGuardian, stateOpen, now); err != nil {
			if store.IsUniqueViolation(err) {
				return gate.NewProblem(gate.StatusConflict, "recovery_exists")
			}
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO recovery_approvals (request_id, approver_id, role, at, auth_at)
			VALUES ($1, $2, $3, $4, $5)`, rid, int64(v.ID), roleGuardian, now, v.AuthAt)
		return err
	})
	if err != nil {
		return gate.HandshakeView{}, err
	}
	hv, err := s.d.Handshakes.OpenRecovery(ctx, v.ID, account)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	hs, ok := s.d.Refs.OpenHandshake(v.ID, hv.HS)
	if !ok {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if _, err := s.d.DB.Q().Exec(ctx, `UPDATE recovery_requests SET handshake = $2
		WHERE id = $1 AND state = $3 AND claimed_session IS NULL`, rid, hs, stateOpen); err != nil {
		return gate.HandshakeView{}, err
	}
	return hv, nil
}

// RecoveryClaim claims guardian A's recovery code from the recovering
// device's guest session. claim is the JSON claim body ({payload, e_v,
// sig_pub, proof}), passed to core.Handshakes.ClaimRecovery as is. It answers
// the device's recovery ticket.
func (s *Service) RecoveryClaim(ctx context.Context, claim json.RawMessage) (Claimed, error) {
	v, ok := core.ViewerFrom(ctx)
	if !ok || v.Kind != core.SessGuest || v.Session == ([32]byte{}) || s.d.Handshakes == nil {
		return Claimed{}, gate.ErrNotFound
	}
	account, err := s.d.Handshakes.ClaimRecovery(ctx, v.Session, string(claim))
	if err != nil {
		return Claimed{}, err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Claimed{}, err
	}
	ticket := b64.EncodeToString(raw[:])
	h := sha256.Sum256([]byte(ticket))
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE recovery_requests SET claimed_session = $2
		WHERE user_id = $1 AND kind = $3 AND state = $4 AND handshake IS NOT NULL AND claimed_session IS NULL`,
		int64(account), h[:], kindGuardian, stateOpen)
	if err != nil {
		return Claimed{}, err
	}
	if tag.RowsAffected() != 1 {
		return Claimed{}, gate.ErrNotFound
	}
	return Claimed{Ticket: ticket}, nil
}

// passedOutcome reports whether a recovery handshake outcome vouches.
func passedOutcome(o core.Outcome) bool {
	return o == core.OutcomeAccepted || o == core.OutcomeVerified
}

// vouch marks open guardian request r vouched once its handshake passed and
// tells the account's other guardians. It reports whether r is vouched.
func (s *Service) vouch(ctx context.Context, tx store.Q, r *recoveryRow, now time.Time) (bool, error) {
	if r.vouchedAt != nil {
		return true, nil
	}
	if r.handshake == nil || s.d.Handshakes == nil {
		return false, nil
	}
	o, err := s.d.Handshakes.Result(ctx, *r.handshake)
	if errors.Is(err, gate.ErrNotFound) || (err == nil && !passedOutcome(o)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET vouched_at = $2 WHERE id = $1", r.id, now); err != nil {
		return false, err
	}
	r.vouchedAt = &now
	for _, f := range s.d.Graph.Friends(r.user) {
		if f.Level != core.LevelFriendPlus || f.To == r.first {
			continue
		}
		if err := s.d.Notify.Notify(ctx, tx, core.Event{To: f.To, Kind: notifyRecoveryApproval, SubjectUser: r.user}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// RecoveryStatus is the recovering device's view of its request, found by
// its ticket. A request whose hold elapsed is released here if the job has
// not run yet; a released request answers a fresh one-time enrollment URL
// (any earlier one stops working).
func (s *Service) RecoveryStatus(ctx context.Context, ticket string) (RecoveryStatus, error) {
	if ticket == "" || len(ticket) > 64 {
		return RecoveryStatus{}, gate.ErrNotFound
	}
	h := sha256.Sum256([]byte(ticket))
	var id uuid.UUID
	err := s.d.DB.Q().QueryRow(ctx, `SELECT id FROM recovery_requests WHERE claimed_session = $1 AND kind = $2
		ORDER BY created_at DESC LIMIT 1`, h[:], kindGuardian).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecoveryStatus{}, gate.ErrNotFound
	}
	if err != nil {
		return RecoveryStatus{}, err
	}
	now := s.d.Clock.Now()
	var out RecoveryStatus
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		out = RecoveryStatus{}
		r, err := loadAny(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if r.state == stateOpen && now.Sub(r.createdAt) > OpenFor {
			r.state = stateExpired
			if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", id, stateExpired); err != nil {
				return err
			}
		}
		if r.state == stateHolding && r.releaseAt != nil && !now.Before(*r.releaseAt) {
			if err := s.releaseNow(ctx, tx, r); err != nil {
				return err
			}
			r.state = stateReleased
		}
		if r.state == stateReleased && r.releaseAt != nil && !now.Before(r.releaseAt.Add(LinkFor)) {
			r.state = stateExpired
			if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", id, stateExpired); err != nil {
				return err
			}
			if r.tokenHash != nil {
				if err := s.d.Enrollments.CancelEnrollment(ctx, tx, r.tokenHash); err != nil {
					return err
				}
			}
		}
		switch r.state {
		case stateOpen:
			ok, err := s.vouch(ctx, tx, &r, now)
			if err != nil {
				return err
			}
			out.State = StatusClaimed
			if ok {
				out.State = StatusApproving
			}
		case stateHolding:
			out.State, out.ReleaseAt = StatusHolding, utc(r.releaseAt)
		case stateReleased:
			if r.tokenHash != nil {
				if err := s.d.Enrollments.CancelEnrollment(ctx, tx, r.tokenHash); err != nil {
					return err
				}
			}
			tok, err := s.d.Passkeys.IssueEnrollment(ctx, tx, r.user, purposeRecovery, *r.releaseAt)
			if err != nil {
				return err
			}
			th := sha256.Sum256([]byte(tok))
			if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET enrollment_token = $2 WHERE id = $1", id, th[:]); err != nil {
				return err
			}
			out.State, out.ReleaseAt, out.EnrollURL = StatusReleased, utc(r.releaseAt), s.d.Cfg.Origin+"/enroll#"+tok
		case stateCancelled:
			out.State = StatusCancelled
		default:
			out.State = StatusExpired
		}
		return nil
	})
	if errors.Is(err, gate.ErrNotFound) {
		// The account was deleted meanwhile.
		return RecoveryStatus{State: StatusCancelled}, nil
	}
	return out, err
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// releaseNow releases holding request r (its hold elapsed): every session of
// the account ends when revoke_old (security #14).
func (s *Service) releaseNow(ctx context.Context, tx store.Q, r recoveryRow) error {
	if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", r.id, stateReleased); err != nil {
		return err
	}
	if r.revokeOld {
		return s.d.Passkeys.RevokeAll(ctx, tx, r.user)
	}
	return nil
}

// RecoveryRequests lists the guardian recoveries waiting for the viewer's
// approval: vouched, open requests of accounts the viewer is a friend+ of,
// which the viewer did not vouch for.
func (s *Service) RecoveryRequests(ctx context.Context) (GuardianRequests, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return GuardianRequests{}, err
	}
	var accounts []int64
	for _, f := range s.d.Graph.Friends(v.ID) {
		if f.Level == core.LevelFriendPlus {
			accounts = append(accounts, int64(f.To))
		}
	}
	out := GuardianRequests{Items: []GuardianRequest{}}
	if len(accounts) == 0 {
		return out, nil
	}
	now := s.d.Clock.Now()
	rows, err := s.d.DB.Q().Query(ctx, `SELECT id FROM recovery_requests
		WHERE user_id = ANY($1) AND kind = $2 AND state = $3 AND created_at > $4 ORDER BY created_at, id`,
		accounts, kindGuardian, stateOpen, now.Add(-OpenFor))
	if err != nil {
		return out, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		var (
			item GuardianRequest
			ok   bool
		)
		err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			ok = false
			r, err := loadAny(ctx, tx, id, true)
			if err != nil || r.state != stateOpen || r.first == v.ID || !s.isGuardian(ctx, v.ID, r.user) {
				return err
			}
			vouched, err := s.vouch(ctx, tx, &r, now)
			if err != nil || !vouched {
				return err
			}
			m, err := s.d.Users.Get(ctx, r.user)
			if err != nil {
				return err
			}
			item = GuardianRequest{ID: s.d.Refs.Handshake(v.ID, id), Account: PersonLabel{Handle: m.Handle,
				DisplayName: m.DisplayName}, ExpiresAt: r.createdAt.Add(OpenFor).UTC()}
			ok = true
			return nil
		})
		if errors.Is(err, gate.ErrNotFound) {
			continue
		}
		if err != nil {
			return out, err
		}
		if ok {
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

// canApproveGuardian checks that v may give the second approval of open
// guardian request r: a guardian of the account other than the one who
// vouched, the request vouched and not expired.
func (s *Service) canApproveGuardian(ctx context.Context, v core.Viewer, r recoveryRow) error {
	if r.state != stateOpen || !s.isGuardian(ctx, v.ID, r.user) || r.vouchedAt == nil {
		return gate.ErrNotFound
	}
	if !s.d.Clock.Now().Before(r.createdAt.Add(OpenFor)) {
		return gate.NewProblem(gate.StatusConflict, "recovery_expired")
	}
	if r.first == v.ID {
		return gate.NewProblem(gate.StatusConflict, "second_guardian_required")
	}
	return nil
}

// RecoveryGuardianApprove is guardian B's approval of guardian request id
// (a ref from RecoveryRequests), co-signed with B's passkey. It starts the
// 24 h hold, writes the RecoveryApproval record and tells the account's
// friends. Problems second_guardian_required, recovery_expired; anyone who is
// not a guardian of the account gets the uniform not-found.
func (s *Service) RecoveryGuardianApprove(ctx context.Context, id gate.Ref, c *Cosig) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	rid, ok := s.d.Refs.OpenHandshake(v.ID, id)
	if !ok {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		r, err := loadAny(ctx, tx, rid, true)
		if err != nil {
			return err
		}
		if r.kind != kindGuardian {
			return gate.ErrNotFound
		}
		if r.state == stateOpen {
			if _, err := s.vouch(ctx, tx, &r, now); err != nil {
				return err
			}
		}
		if err := s.canApproveGuardian(ctx, v, r); err != nil {
			return err
		}
		sg, err := s.verify(ctx, tx, v, PurposeRecoveryApprove, core.RecRecoveryApproval, c)
		if err != nil {
			return err
		}
		body, err := s.approval(ctx, r, v.ID, methodGuardians)
		if err != nil {
			return err
		}
		if err := s.sign(ctx, tx, core.RecRecoveryApproval, v.ID, body, []core.UserID{r.user, r.first, v.ID}, sg,
			func(rec []byte) bool { return sameApproval(rec, body) }); err != nil {
			return err
		}
		var asr []byte
		if sg != nil {
			if asr, err = json.Marshal(assertionJSON(sg.a)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_approvals (request_id, approver_id, role, at, auth_at, assertion)
			VALUES ($1, $2, $3, $4, $5, $6)`, rid, int64(v.ID), roleGuardian, now, v.AuthAt, asr); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2, release_at = $3 WHERE id = $1",
			rid, stateHolding, now.Add(HoldFor)); err != nil {
			return err
		}
		return s.notifyPending(ctx, tx, r.user)
	})
}

// notifyPending tells the account's friends that a recovery of it is holding.
func (s *Service) notifyPending(ctx context.Context, tx store.Q, user core.UserID) error {
	for _, f := range s.d.Graph.Friends(user) {
		if err := s.d.Notify.Notify(ctx, tx, core.Event{To: f.To, Kind: notifyFriendSecurity, SubjectUser: user,
			Data: map[string]any{"reason": "recovery_pending"}}); err != nil {
			return err
		}
	}
	return nil
}

// assertionJSON is the stored form of an approval's passkey assertion
// (recovery_approvals.assertion).
func assertionJSON(a core.Assertion) map[string]string {
	return map[string]string{
		"credential_id":      b64.EncodeToString(a.CredentialID),
		"authenticator_data": b64.EncodeToString(a.AuthenticatorData),
		"client_data_json":   b64.EncodeToString(a.ClientDataJSON),
		"signature":          b64.EncodeToString(a.Signature),
	}
}
