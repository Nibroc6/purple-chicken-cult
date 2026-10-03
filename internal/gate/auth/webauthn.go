package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// CeremonyTTL bounds every pending WebAuthn ceremony (plan §4.7 auth).
const CeremonyTTL = 2 * time.Minute

// Ceremony purposes stored in sessions.ceremony.
const (
	purposeLogin    = "login"
	purposeRegister = "register"
	purposeAdd      = "add"
	purposeStepUp   = "stepup"
	purposeEnroll   = "enroll"
)

// rpName is the relying-party display name shown by authenticators.
const rpName = "pcc"

// errCeremony is any failed ceremony check. Callers map it to the uniform 404
// (anonymous flows) or Problem passkey_rejected (signed-in flows); its detail
// never leaves the server.
var errCeremony = errors.New("auth: ceremony rejected")

// ceremony is the ONE pending WebAuthn ceremony of a session (sessions.ceremony).
type ceremony struct {
	Purpose string               `json:"purpose"`
	Data    webauthn.SessionData `json:"session_data"`
	Exp     time.Time            `json:"exp"`
	User    int64                `json:"user,omitempty"`
}

// waUser adapts a (pending) account to the webauthn library.
type waUser struct {
	id            []byte
	name, display string
	creds         []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.id }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.display }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// rp returns the relying party, built once from the configuration: exact
// origin, no cross-origin use, discoverable credentials with user verification
// required and no attestation (passkeys, §17.2).
func (s *Service) rp() (*webauthn.WebAuthn, error) {
	s.rpOnce.Do(func() {
		s.wa, s.waErr = webauthn.New(&webauthn.Config{
			RPID:                  s.d.Cfg.RPID,
			RPDisplayName:         rpName,
			RPOrigins:             []string{s.d.Cfg.Origin},
			AttestationPreference: protocol.PreferNoAttestation,
			AuthenticatorSelection: protocol.AuthenticatorSelection{
				ResidentKey:        protocol.ResidentKeyRequirementRequired,
				RequireResidentKey: protocol.ResidentKeyRequired(),
				UserVerification:   protocol.VerificationRequired,
			},
			Timeouts: webauthn.TimeoutsConfig{
				Login:        webauthn.TimeoutConfig{Timeout: CeremonyTTL, TimeoutUVD: CeremonyTTL},
				Registration: webauthn.TimeoutConfig{Timeout: CeremonyTTL, TimeoutUVD: CeremonyTTL},
			},
		})
	})
	return s.wa, s.waErr
}

// marshalCeremony encodes c for sessions.ceremony.
func marshalCeremony(c ceremony) ([]byte, error) { return json.Marshal(c) }

// putCeremony stores c as the session's one pending ceremony (replacing any).
func (s *Service) putCeremony(ctx context.Context, q store.Q, sess [32]byte, c ceremony) error {
	raw, err := marshalCeremony(c)
	if err != nil {
		return err
	}
	tag, err := q.Exec(ctx, "UPDATE sessions SET ceremony = $2 WHERE id_hash = $1 AND expires_at > $3",
		sess[:], raw, s.d.Clock.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return gate.ErrNotFound
	}
	return nil
}

// takeCeremony atomically clears the session's pending ceremony (whatever the
// outcome: a ceremony is single use) and returns it when it has the wanted
// purpose and has not expired.
func (s *Service) takeCeremony(ctx context.Context, sess [32]byte, purpose string) (ceremony, error) {
	var raw []byte
	err := s.d.DB.Q().QueryRow(ctx, `WITH old AS (
			SELECT id_hash, ceremony FROM sessions WHERE id_hash = $1 AND ceremony IS NOT NULL FOR UPDATE)
		UPDATE sessions s SET ceremony = NULL FROM old WHERE s.id_hash = old.id_hash RETURNING old.ceremony`,
		sess[:]).Scan(&raw)
	if err != nil {
		return ceremony{}, errCeremony
	}
	var c ceremony
	if err := json.Unmarshal(raw, &c); err != nil {
		return ceremony{}, errCeremony
	}
	if c.Purpose != purpose || !s.d.Clock.Now().Before(c.Exp) {
		return ceremony{}, errCeremony
	}
	return c, nil
}

// clientDataOK applies the exact-origin rules the library leaves to policy:
// origin equal to cfg.Origin byte for byte, crossOrigin false or absent, and no
// topOrigin (security #9).
func (s *Service) clientDataOK(cd protocol.CollectedClientData) bool {
	return cd.Origin == s.d.Cfg.Origin && !cd.CrossOrigin && cd.TopOrigin == ""
}

// descriptors turns credential IDs into allow/exclude descriptors.
func descriptors(ids [][]byte) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(ids))
	for _, id := range ids {
		out = append(out, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: id})
	}
	return out
}

// beginRegistration starts a registration in session sess for the pending
// account pu; user is the account's ID when it already exists (add, enroll).
func (s *Service) beginRegistration(ctx context.Context, q store.Q, sess [32]byte, purpose string, user core.UserID,
	pu core.PendingUser) (json.RawMessage, error) {
	wa, err := s.rp()
	if err != nil {
		return nil, err
	}
	display := pu.DisplayName
	if display == "" {
		display = pu.Handle
	}
	opts := []webauthn.RegistrationOption{}
	if len(pu.Exclude) > 0 {
		opts = append(opts, webauthn.WithExclusions(descriptors(pu.Exclude)))
	}
	creation, data, err := wa.BeginRegistration(&waUser{id: pu.WebAuthnID, name: pu.Handle, display: display}, opts...)
	if err != nil {
		return nil, err
	}
	if q == nil {
		q = s.d.DB.Q()
	}
	c := ceremony{Purpose: purpose, Data: *data, Exp: s.d.Clock.Now().Add(CeremonyTTL), User: int64(user)}
	if err := s.putCeremony(ctx, q, sess, c); err != nil {
		return nil, err
	}
	return json.Marshal(creation)
}

// finishRegistration verifies a registration response against the session's
// pending ceremony of the given purpose (cleared first, whatever the outcome).
func (s *Service) finishRegistration(ctx context.Context, sess [32]byte, purpose string, body []byte) (core.Credential, ceremony, error) {
	c, err := s.takeCeremony(ctx, sess, purpose)
	if err != nil {
		return core.Credential{}, c, err
	}
	wa, err := s.rp()
	if err != nil {
		return core.Credential{}, c, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return core.Credential{}, c, errCeremony
	}
	if !s.clientDataOK(parsed.Response.CollectedClientData) {
		return core.Credential{}, c, errCeremony
	}
	cred, err := wa.CreateCredential(&waUser{id: c.Data.UserID}, c.Data, parsed)
	if err != nil {
		return core.Credential{}, c, errCeremony
	}
	flags := parsed.Response.AttestationObject.AuthData.Flags
	if !flags.HasUserPresent() || !flags.HasUserVerified() {
		return core.Credential{}, c, errCeremony
	}
	var pk webauthncose.PublicKeyData
	if err := webauthncbor.Unmarshal(cred.PublicKey, &pk); err != nil {
		return core.Credential{}, c, errCeremony
	}
	out := core.Credential{
		ID: cred.ID, PublicKey: cred.PublicKey, Alg: int(pk.Algorithm), AAGUID: cred.Authenticator.AAGUID,
		SignCount: cred.Authenticator.SignCount, BackupEligible: cred.Flags.BackupEligible,
		BackupState: cred.Flags.BackupState, UV: flags.HasUserVerified(), AttestationFmt: cred.AttestationFormat,
	}
	for _, t := range cred.Transport {
		out.Transports = append(out.Transports, string(t))
	}
	if len(out.ID) == 0 || len(out.ID) > 1023 {
		return core.Credential{}, c, errCeremony
	}
	return out, c, nil
}

// assertionResult is a verified assertion and the stored credential it used.
type assertionResult struct {
	user      core.UserID
	credID    []byte
	signCount uint32
	backup    bool
	parsed    *protocol.ParsedCredentialAssertionData
}

// checkAssertion runs the post-library checks shared by every assertion: exact
// origin, UP and UV, and the sign-count rule (a stored nonzero counter must
// strictly increase; 0 forever is allowed for synced passkeys).
func (s *Service) checkAssertion(parsed *protocol.ParsedCredentialAssertionData, cred *webauthn.Credential) error {
	if !s.clientDataOK(parsed.Response.CollectedClientData) {
		return errCeremony
	}
	flags := parsed.Response.AuthenticatorData.Flags
	if !flags.HasUserPresent() || !flags.HasUserVerified() {
		return errCeremony
	}
	if cred.Authenticator.CloneWarning {
		return errCeremony
	}
	return nil
}

// recordUse stores the counter, backup state and last use of a credential.
func (s *Service) recordUse(ctx context.Context, q store.Q, a assertionResult) error {
	_, err := q.Exec(ctx, `UPDATE webauthn_credentials SET sign_count = GREATEST(sign_count, $2), backup_state = $3,
		last_used_at = $4 WHERE id = $1`, a.credID, int64(a.signCount), a.backup, s.d.Clock.Now())
	return err
}

// ---- core.Passkeys ----

// BeginRegistration starts a WebAuthn registration in session sess (the join
// flow's guest session) for a not yet created account.
func (s *Service) BeginRegistration(ctx context.Context, sess [32]byte, u core.PendingUser) (json.RawMessage, error) {
	if len(u.WebAuthnID) == 0 || len(u.WebAuthnID) > 64 || u.Handle == "" {
		return nil, fmt.Errorf("auth: invalid pending user")
	}
	return s.beginRegistration(ctx, nil, sess, purposeRegister, 0, u)
}

// FinishRegistration verifies a registration response of session sess. The
// credential is not bound to any account yet (BindCredential does that).
func (s *Service) FinishRegistration(ctx context.Context, sess [32]byte, body []byte) (core.Credential, error) {
	c, _, err := s.finishRegistration(ctx, sess, purposeRegister, body)
	if errors.Is(err, errCeremony) {
		return core.Credential{}, gate.ErrNotFound
	}
	return c, err
}

// BindCredential stores c for user and writes its PasskeyChange record.
func (s *Service) BindCredential(ctx context.Context, tx store.Q, user core.UserID, c core.Credential) error {
	return s.bindCredential(ctx, tx, user, c, "")
}

// Upgrade turns the guest session sess into a user session authenticated by
// credential cred: the old row is deleted and a NEW token with a new CSRF seed
// is issued (no fixation).
func (s *Service) Upgrade(ctx context.Context, tx store.Q, sess [32]byte, user core.UserID, cred []byte) (string, error) {
	tag, err := tx.Exec(ctx, "DELETE FROM sessions WHERE id_hash = $1 AND kind <> $2", sess[:], int16(core.SessUser))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		return "", gate.ErrNotFound
	}
	return s.issue(ctx, tx, core.SessUser, user, s.d.Clock.Now(), cred, nil)
}

// BeginAssertion starts a user-verifying assertion over challenge (random when
// empty), restricted to v's own credentials (security #9). The ceremony lives in
// v's session under purpose.
func (s *Service) BeginAssertion(ctx context.Context, v core.Viewer, challenge []byte, purpose string) (json.RawMessage, error) {
	if v.Kind != core.SessUser || v.ID == 0 || purpose == "" {
		return nil, gate.ErrNotFound
	}
	wa, err := s.rp()
	if err != nil {
		return nil, err
	}
	u, err := s.accountUser(ctx, s.d.DB.Q(), v.ID)
	if err != nil {
		return nil, err
	}
	if len(u.creds) == 0 {
		return nil, gate.ErrNotFound
	}
	ids := make([][]byte, 0, len(u.creds))
	for _, c := range u.creds {
		ids = append(ids, c.ID)
	}
	opts := []webauthn.LoginOption{webauthn.WithAllowedCredentials(descriptors(ids)),
		webauthn.WithUserVerification(protocol.VerificationRequired)}
	if len(challenge) > 0 {
		opts = append(opts, webauthn.WithChallenge(challenge))
	}
	assertion, data, err := wa.BeginLogin(u, opts...)
	if err != nil {
		return nil, err
	}
	c := ceremony{Purpose: purpose, Data: *data, Exp: s.d.Clock.Now().Add(CeremonyTTL), User: int64(v.ID)}
	if err := s.putCeremony(ctx, s.d.DB.Q(), v.Session, c); err != nil {
		return nil, err
	}
	return json.Marshal(assertion)
}

// VerifyAssertion verifies an assertion for v's pending ceremony of purpose:
// the credential must be one of v's own (allowCredentials), UP=1 and UV=1, type
// webauthn.get, exact origin, no crossOrigin/topOrigin, rpIdHash, the BE flag
// unchanged and the sign-count rule. The ceremony is cleared whatever the
// outcome. Failure is Problem passkey_rejected.
func (s *Service) VerifyAssertion(ctx context.Context, v core.Viewer, purpose string, body []byte) (core.Assertion, error) {
	if v.Kind != core.SessUser || v.ID == 0 {
		return core.Assertion{}, gate.ErrNotFound
	}
	c, err := s.takeCeremony(ctx, v.Session, purpose)
	if err != nil || c.User != int64(v.ID) {
		return core.Assertion{}, rejected()
	}
	wa, err := s.rp()
	if err != nil {
		return core.Assertion{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return core.Assertion{}, rejected()
	}
	u, err := s.accountUser(ctx, s.d.DB.Q(), v.ID)
	if err != nil {
		return core.Assertion{}, err
	}
	cred, err := wa.ValidateLogin(u, c.Data, parsed)
	if err != nil {
		return core.Assertion{}, rejected()
	}
	if err := s.checkAssertion(parsed, cred); err != nil {
		return core.Assertion{}, rejected()
	}
	res := assertionResult{user: v.ID, credID: cred.ID, signCount: cred.Authenticator.SignCount,
		backup: parsed.Response.AuthenticatorData.Flags.HasBackupState(), parsed: parsed}
	if err := s.recordUse(ctx, s.d.DB.Q(), res); err != nil {
		return core.Assertion{}, err
	}
	raw := parsed.Raw.AssertionResponse
	return core.Assertion{CredentialID: bytes.Clone(cred.ID), AuthenticatorData: bytes.Clone(raw.AuthenticatorData),
		ClientDataJSON: bytes.Clone(raw.ClientDataJSON), Signature: bytes.Clone(raw.Signature), User: v.ID}, nil
}

// rejected is the signed-in failure of an assertion ceremony.
func rejected() error { return gate.NewProblem(gate.StatusBadRequest, "passkey_rejected") }
