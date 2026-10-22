// Package moderation owns reports, the moderator queue, two-person actions, the
// blame inbox, the public moderation log and CSP report aggregation
// (ARCHITECTURE §8, §10.4, §17.3; plan §4.7 moderation).
//
//   - Reports (post, person, inviter routed through the invitee, misleading
//     preview) carry a reason from the closed enum; a target the reporter cannot
//     see is dropped silently, so the answer never depends on it. Each stored
//     report is a signed Report record.
//   - Moderators see the reported content, the involved handles as
//     moderator-scoped refs, the subject's inviter label and probation flag, and
//     nothing else of the graph. remove_post and dismiss take one moderator;
//     suspend_user waits for a second, different moderator for 24 h, both with a
//     fresh step-up.
//   - Only after a confirmed action does the subject's inviter get a blame_inbox
//     entry and an invitee_action notification; the post is in it only if the
//     inviter could see it when it was removed.
//   - The public log lists confirmed actions and counts per day under
//     pseudonyms; a handle appears only to viewers who have that person in their
//     horizon.
//
// Phase 1 (P1-W06):
//   - One queue (mod_queue) for reports, CSAM holds, behavioral signals
//     (gate/signals/behavior), step-up repeats and invite clusters, csam first;
//     a moderator may claim an item for an hour. CSAM items show the hold's
//     match metadata (core.CSAMHolds), never content or a media URL.
//   - Two-person actions (suspend_user, the csam_report filing, dismissing a
//     CSAM hold, quarantine_subtree) wait 24 h for a second, different
//     moderator, who co-signs the exact ModAction record with their own passkey
//     (core.Cosign, purpose mod_action, security #30); the record is stored
//     signed. Sessions without a passkey (test hooks only) fall back to the P0
//     fresh step-up.
//   - Invite-tree quarantine (§8): once ≥ PCC_QUARANTINE_K accounts of an
//     inviter's subtree are confirmed bad (suspended by two moderators since
//     its last quarantine), two moderators can send the subtree back to
//     probation and pause the inviter's invites for 30 days; a second offense
//     revokes them. Nothing outside the subtree changes.
//   - A confirmed suspension marks the inviter "invited a removed account" for
//     90 days, visible only to the users in the removed account's component at
//     that moment (removed_account_marks.viewers) and to moderators.
//   - Nothing reaches an inviter (blame entry, notification, mark) before a
//     moderator confirmed the action.
//
// Owner: P0-W12 → P1-W06.
package moderation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Actions (mod_actions.action).
const (
	ActionRemovePost  = "remove_post"
	ActionSuspendUser = "suspend_user"
	ActionDismiss     = "dismiss"
	ActionQuarantine  = "quarantine_subtree"
	ActionCSAMReport  = "csam_report"
	// ActionRemoveMessage erases a reported direct message (one moderator).
	ActionRemoveMessage = "remove_message"
)

// PurposeModAction is the co-sign purpose of the second moderator's
// confirmation (core.CosignPurposes; the record is a ModAction).
const PurposeModAction = "mod_action"

// Report target kinds (API name → reports.target_kind).
const (
	KindPost    = "post"
	KindPerson  = "person"
	KindInviter = "inviter"
	KindPreview = "preview"
	// KindMessage reports a direct message (gate/dm): the report keeps a copy
	// of its text, the only way a moderator ever sees a message.
	KindMessage = "message"
)

var targetKinds = map[string]int16{KindPost: 0, KindPerson: 1, KindInviter: 2, KindPreview: 3, KindMessage: 4}

// reports.state and mod_actions.state values.
const (
	reportOpen      int16 = 0
	reportActioned  int16 = 1
	reportDismissed int16 = 2

	actionPending int16 = 0
	actionDone    int16 = 1
	actionExpired int16 = 2
)

// Timings and sizes.
const (
	// SecondWithin is how long a two-person action waits for the second moderator.
	SecondWithin = 24 * time.Hour
	// NoteMax bounds a report note (code points).
	NoteMax = 1000
	// ExcerptMax bounds the blame-inbox excerpt of a removed post (code points).
	ExcerptMax = 280
	// QueuePage, BlamePage and LogPage are the page sizes.
	QueuePage = 30
	BlamePage = 30
	LogPage   = 50
	// PseudonymBytes of HMAC(PseudonymKey, user) name a log subject.
	PseudonymBytes = 6
	// ClaimFor is how long a moderator's claim on a queue item holds.
	ClaimFor = time.Hour
	// QuarantineFor is the quarantine's probation and invite pause (§8).
	QuarantineFor = 30 * 24 * time.Hour
	// MarkFor is how long an "invited a removed account" mark lasts (§8).
	MarkFor = 90 * 24 * time.Hour
)

// Jobs.
const (
	// JobExpire marks two-person actions older than SecondWithin expired and
	// deletes removed-account marks past their 90 days.
	JobExpire = "moderation.expire"
	// ExpireEvery is the moderation.expire period.
	ExpireEvery = time.Hour
)

// Service is the moderation module.
type Service struct {
	d *core.Deps
}

var (
	_ core.ModQueue     = (*Service)(nil)
	_ core.RemovedMarks = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers moderation.expire and the mod_action co-sign purpose.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobExpire, func(ctx context.Context, _ json.RawMessage) error { return s.Expire(ctx) })
		s.d.Jobs.Every(JobExpire, ExpireEvery)
	}
	if s.d.CosignPurposes != nil {
		s.d.CosignPurposes.RegisterPurpose(PurposeModAction, core.RecModAction, s.modActionDraft)
	}
	return nil
}

// Expire marks two-person actions that waited longer than SecondWithin expired
// and deletes expired removed-account marks (moderation.expire). Confirm and
// Marked check the deadlines themselves as well.
func (s *Service) Expire(ctx context.Context) error {
	now := s.d.Clock.Now()
	if _, err := s.d.DB.Q().Exec(ctx, "UPDATE mod_actions SET state = $1 WHERE state = $2 AND created_at <= $3",
		actionExpired, actionPending, now.Add(-SecondWithin)); err != nil {
		return err
	}
	_, err := s.d.DB.Q().Exec(ctx, "DELETE FROM removed_account_marks WHERE expires_at <= $1", now)
	return err
}

func validReason[T ~string](list []T, r string) bool {
	for _, x := range list {
		if string(x) == r {
			return true
		}
	}
	return false
}

// ---- GrantModerator (CLI) ----

// GrantModerator makes handle a moderator (CLI `pcc mod grant`). by must be an
// existing active admin; bootstrap makes handle the first admin and works only
// while no admin exists. users.role is this module's column (plan §3.4). A
// change of role is logged like one made on the roles page (roles.go); a
// handle that already has the role (or a higher one) changes nothing.
func (s *Service) GrantModerator(ctx context.Context, handle, by string, bootstrap bool) error {
	handle, by = canonHandle(handle), canonHandle(by)
	now := s.d.Clock.Now()
	var id int64
	err := s.d.DB.TxSerializable(ctx, func(tx pgx.Tx) error {
		var admins int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role = $1 AND status = $2",
			int16(core.RoleAdmin), int16(core.StatusActive)).Scan(&admins); err != nil {
			return err
		}
		role, action := int16(core.RoleModerator), ActionGrantModerator
		var byID int64
		if bootstrap {
			if admins > 0 {
				return errors.New("moderation: an admin already exists; use --by")
			}
			role, action = int16(core.RoleAdmin), ActionGrantAdmin
		} else {
			var byRole int16
			err := tx.QueryRow(ctx, "SELECT id, role FROM users WHERE handle_canon = $1 AND status = $2", by,
				int16(core.StatusActive)).Scan(&byID, &byRole)
			if err != nil || byRole != int16(core.RoleAdmin) {
				return errors.New("moderation: --by must name an active admin")
			}
		}
		var old int16
		err := tx.QueryRow(ctx, "SELECT id, role FROM users WHERE handle_canon = $1 AND status = $2 FOR UPDATE",
			handle, int16(core.StatusActive)).Scan(&id, &old)
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.ErrNotFound
		}
		if err != nil || old >= role {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE users SET role = $2 WHERE id = $1", id, role); err != nil {
			return err
		}
		if bootstrap {
			byID = id
		}
		return s.logRoleChange(ctx, tx, core.UserID(byID), core.UserID(id), action, nil, now) //nolint:gosec // users.id < 2^32 (DB CHECK)
	})
	if err != nil {
		return err
	}
	s.d.Bus.Publish(core.UserChanged{ID: core.UserID(id)}) //nolint:gosec // users.id < 2^32 (DB CHECK)
	return nil
}

// ---- CSP reports ----

// cspDirectives is the closed set of directives counted by name; anything else
// counts as "other".
var cspDirectives = map[string]bool{
	"default-src": true, "script-src": true, "script-src-elem": true, "script-src-attr": true, "style-src": true,
	"style-src-elem": true, "style-src-attr": true, "img-src": true, "font-src": true, "connect-src": true,
	"worker-src": true, "manifest-src": true, "media-src": true, "frame-src": true, "child-src": true,
	"frame-ancestors": true, "base-uri": true, "form-action": true, "object-src": true,
	"require-trusted-types-for": true, "trusted-types": true, "upgrade-insecure-requests": true,
}

// CSPDirective normalizes a reported directive ("script-src-elem 'self'" →
// "script-src-elem"; unknown → "other").
func CSPDirective(raw string) string {
	f := strings.Fields(strings.ToLower(raw))
	if len(f) == 0 || !cspDirectives[f[0]] {
		return "other"
	}
	return f[0]
}

// CSPReport counts one CSP violation of directive (aggregate count per UTC day
// and directive only: no URL, no user, no address is stored).
func (s *Service) CSPReport(ctx context.Context, directive string) error {
	day := s.d.Clock.Now().UTC().Truncate(24 * time.Hour)
	_, err := s.d.DB.Q().Exec(ctx, `INSERT INTO csp_violations (day, directive, count) VALUES ($1, $2, 1)
		ON CONFLICT (day, directive) DO UPDATE SET count = csp_violations.count + 1`, day, CSPDirective(directive))
	return err
}

// CSPCounts returns the per-directive counts of the last days UTC days (CLI and tests).
func (s *Service) CSPCounts(ctx context.Context, days int) (map[string]int64, error) {
	since := s.d.Clock.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1-max(days, 1))
	rows, err := s.d.DB.Q().Query(ctx, "SELECT directive, sum(count) FROM csp_violations WHERE day >= $1 GROUP BY directive", since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			d string
			n int64
		)
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		out[d] = n
	}
	return out, rows.Err()
}

// ---- cursors: base64url(offset ‖ HMAC16) bound to the viewer and the list ----

func (s *Service) mac(v core.UserID, list string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	m.Write([]byte("pcc/moderation/v1\x00"))
	m.Write([]byte(list))
	m.Write([]byte{0})
	m.Write(binary.BigEndian.AppendUint32(nil, uint32(v)))
	m.Write(payload)
	return m.Sum(nil)[:16]
}

func (s *Service) cursorString(v core.UserID, list string, offset int) string {
	payload := binary.BigEndian.AppendUint32(nil, uint32(offset)) //nolint:gosec // offsets are small and non-negative
	return base64.RawURLEncoding.EncodeToString(append(payload, s.mac(v, list, payload)...))
}

// openCursor returns the offset of a cursor ("" = 0); a foreign, tampered or
// garbage cursor is not ok.
func (s *Service) openCursor(v core.UserID, list, str string) (int, bool) {
	if str == "" {
		return 0, true
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(str)
	if err != nil || len(b) != 20 {
		return 0, false
	}
	if !hmac.Equal(b[4:], s.mac(v, list, b[:4])) {
		return 0, false
	}
	return int(binary.BigEndian.Uint32(b[:4])), true
}

// pseudonym is the public log's name for u: HMAC(PseudonymKey, u)[:6], hex.
func (s *Service) pseudonym(u core.UserID) string {
	m := hmac.New(sha256.New, s.d.Keys.Pseudonym)
	m.Write([]byte("pcc/modlog/v1\x00"))
	m.Write(binary.BigEndian.AppendUint32(nil, uint32(u)))
	const hexdigits = "0123456789abcdef"
	sum := m.Sum(nil)[:PseudonymBytes]
	out := make([]byte, 0, 2*len(sum))
	for _, c := range sum {
		out = append(out, hexdigits[c>>4], hexdigits[c&15])
	}
	return string(out)
}

func (s *Service) freshModerator(ctx context.Context) (core.Viewer, error) {
	if _, err := core.RequireModerator(ctx); err != nil {
		return core.Viewer{}, err
	}
	return core.RequireFresh(ctx, s.d.Clock.Now())
}
