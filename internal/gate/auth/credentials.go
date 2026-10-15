package auth

import (
	"context"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// MaxPasskeyName bounds a passkey's label (code points).
const MaxPasskeyName = 50

// friend_security reasons (notification data codes).
const (
	notifyFriendSecurity = "friend_security"
	reasonPasskeyAdded   = "passkey_added"
	reasonPasskeyRemoved = "passkey_removed"
	reasonRecovered      = "recovery_enrolled"
)

// PasskeyChange ops.
const (
	opAdd    = "add"
	opRemove = "remove"
)

// passkeyChangeBody is the signed PasskeyChange record body (plan §4.7
// records): the COSE public key is inside the signed bytes so exported
// assertions verify offline (security #5).
type passkeyChangeBody struct {
	User         uuid.UUID  `json:"user"`
	Op           string     `json:"op"`
	CredentialID sigs.Bytes `json:"credential_id"`
	PublicKey    sigs.Bytes `json:"public_key"`
	Alg          int        `json:"alg"`
	At           sigs.Time  `json:"at"`
}

// account is the part of a user auth needs.
type account struct {
	id           core.UserID
	webauthnID   []byte
	handle, name string
	status       uint8
}

// loadAccount reads id's WebAuthn user handle and status through q (locking the
// row until q's transaction ends when lock is set) via core.Accounts, and its
// handle and display name via core.Users.
func (s *Service) loadAccount(ctx context.Context, q store.Q, id core.UserID, lock bool) (account, error) {
	a := account{id: id}
	webID, _, status, err := s.d.Accounts.Account(ctx, q, id, lock)
	if err != nil {
		return a, err
	}
	m, err := s.d.Users.Get(ctx, id)
	if err != nil {
		return a, err
	}
	a.webauthnID, a.status, a.handle, a.name = webID, status, m.Handle, m.DisplayName
	return a, nil
}

// storedCred is one webauthn_credentials row.
type storedCred struct {
	id, publicKey []byte
	alg           int
	signCount     int64
	aaguid        []byte
	transports    []string
	be, bs        bool
	name          string
	createdAt     time.Time
	lastUsedAt    *time.Time
}

func (c storedCred) webauthn() webauthn.Credential {
	tr := make([]protocol.AuthenticatorTransport, 0, len(c.transports))
	for _, t := range c.transports {
		tr = append(tr, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID: c.id, PublicKey: c.publicKey, Transport: tr,
		Flags:         webauthn.CredentialFlags{UserPresent: true, UserVerified: true, BackupEligible: c.be, BackupState: c.bs},
		Authenticator: webauthn.Authenticator{AAGUID: c.aaguid, SignCount: uint32(c.signCount)}, //nolint:gosec // sign_count is a uint32 counter
	}
}

// credentials lists user's credentials, oldest first.
func (s *Service) credentials(ctx context.Context, q store.Q, user core.UserID, lock bool) ([]storedCred, error) {
	order := "ORDER BY created_at, id"
	if lock {
		order = "ORDER BY created_at, id FOR UPDATE"
	}
	sql := `SELECT id, public_key, alg, sign_count, aaguid, transports, backup_eligible, backup_state, name, created_at,
		last_used_at FROM webauthn_credentials WHERE user_id = $1 ` + order
	rows, err := q.Query(ctx, sql, int64(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedCred
	for rows.Next() {
		var c storedCred
		var alg int32
		if err := rows.Scan(&c.id, &c.publicKey, &alg, &c.signCount, &c.aaguid, &c.transports, &c.be, &c.bs, &c.name,
			&c.createdAt, &c.lastUsedAt); err != nil {
			return nil, err
		}
		c.alg = int(alg)
		out = append(out, c)
	}
	return out, rows.Err()
}

// accountUser loads an account and its credentials for the webauthn library.
func (s *Service) accountUser(ctx context.Context, q store.Q, id core.UserID) (*waUser, error) {
	a, err := s.loadAccount(ctx, q, id, false)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentials(ctx, q, id, false)
	if err != nil {
		return nil, err
	}
	u := &waUser{id: a.webauthnID, name: a.handle, display: displayOr(a)}
	for _, c := range creds {
		u.creds = append(u.creds, c.webauthn())
	}
	return u, nil
}

func displayOr(a account) string {
	if a.name != "" {
		return a.name
	}
	return a.handle
}

// pendingUser describes an existing account for a registration ceremony.
func pendingUser(a account, creds []storedCred) core.PendingUser {
	pu := core.PendingUser{WebAuthnID: a.webauthnID, Handle: a.handle, DisplayName: a.name}
	for _, c := range creds {
		pu.Exclude = append(pu.Exclude, c.id)
	}
	return pu
}

// bindCredential inserts c for user and signs its PasskeyChange add record.
func (s *Service) bindCredential(ctx context.Context, tx store.Q, user core.UserID, c core.Credential, name string) error {
	if len(c.ID) == 0 || len(c.PublicKey) == 0 {
		return errors.New("auth: empty credential")
	}
	transports := c.Transports
	if transports == nil {
		transports = []string{}
	}
	now := s.d.Clock.Now()
	_, err := tx.Exec(ctx, `INSERT INTO webauthn_credentials (id, user_id, public_key, alg, sign_count, aaguid, transports,
		backup_eligible, backup_state, name, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		c.ID, int64(user), c.PublicKey, int32(c.Alg), int64(c.SignCount), c.AAGUID, transports, c.BackupEligible, //nolint:gosec // COSE alg IDs are small
		c.BackupState, name, now)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return gate.ErrNotFound // the credential is already registered (to anyone)
		}
		return err
	}
	return s.signChange(ctx, tx, user, opAdd, c.ID, c.PublicKey, c.Alg, now)
}

// signChange writes a PasskeyChange record (party: the account).
func (s *Service) signChange(ctx context.Context, tx store.Q, user core.UserID, op string, id, pub []byte, alg int, at time.Time) error {
	// Read through tx: the account may have been created in this transaction (join).
	_, ext, _, err := s.d.Accounts.Account(ctx, tx, user, false)
	if err != nil {
		return err
	}
	_, err = s.d.Signer.Sign(ctx, tx, core.RecPasskeyChange, user, passkeyChangeBody{User: ext, Op: op,
		CredentialID: id, PublicKey: pub, Alg: alg, At: sigs.T(at)}, []core.UserID{user})
	return err
}

// notifyFriends tells every friend of user that their sign-in keys changed.
func (s *Service) notifyFriends(ctx context.Context, tx store.Q, user core.UserID, reason string) error {
	if s.d.Graph == nil || s.d.Notify == nil {
		return nil
	}
	for _, f := range s.d.Graph.Friends(user) {
		if err := s.d.Notify.Notify(ctx, tx, core.Event{To: f.To, Kind: notifyFriendSecurity, SubjectUser: user,
			Data: map[string]any{"reason": reason}}); err != nil {
			return err
		}
	}
	return nil
}

// Session deletions; each returns (id_hash, user_id) for SessionEnded.
const (
	sqlEndSession    = "DELETE FROM sessions WHERE id_hash = $1 RETURNING id_hash, user_id"
	sqlEndUser       = "DELETE FROM sessions WHERE user_id = $1 RETURNING id_hash, user_id"
	sqlEndCredential = "DELETE FROM sessions WHERE user_id = $1 AND credential_id = $2 RETURNING id_hash, user_id" //nolint:gosec // SQL, not a credential
	sqlEndExpired    = "DELETE FROM sessions WHERE expires_at <= $1 RETURNING id_hash, user_id"
)

// endSessions runs one of the sqlEnd* deletions and returns the SessionEnded
// events to publish after commit.
func endSessions(ctx context.Context, q store.Q, sql string, args ...any) ([]core.SessionEnded, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.SessionEnded
	for rows.Next() {
		var (
			h   []byte
			uid *int64
		)
		if err := rows.Scan(&h, &uid); err != nil {
			return nil, err
		}
		var ev core.SessionEnded
		copy(ev.Session[:], h)
		if uid != nil {
			ev.User = core.UserID(*uid) //nolint:gosec // users.id < 2^32 (DB CHECK)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Service) publishEnded(evs []core.SessionEnded) {
	for _, e := range evs {
		s.d.Bus.Publish(e)
	}
}
