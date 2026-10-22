package cosign

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// The invitee signup ceremony (fidelity #21; core.SignupCosign).
//
// RegisterFinish creates the account, binds its first credential and writes
// the Invite and Friendship records in one serializable transaction. From P1
// the invitee co-signs those records with the credential just created, before
// anything commits:
//
//  1. register/finish: Passkeys.FinishRegistration verifies the new
//     credential; the invites module reserves the account's ext_id and builds
//     the record bodies; BeginSignup drafts them (one nonce, one issue time),
//     stores the ceremony for the guest session and returns get options
//     restricted to that credential, challenge = core.BundleChallenge over the
//     drafts' challenges (one prompt for every record).
//  2. register/cosign: inside the RegisterFinish transaction, FinishSignup
//     consumes the ceremony (UPDATE … WHERE used_at IS NULL RETURNING), checks
//     the caller's bodies are the drafted ones and verifies the assertion
//     against the new credential's public key with the same rules as
//     Passkeys.VerifyAssertion: type webauthn.get, the bundle challenge, origin
//     equal to PCC_ORIGIN byte for byte, crossOrigin false or absent, no
//     topOrigin, rpIdHash, UP and UV, the BE flag as registered, the counter
//     rule and the signature. Any failure is gate.ErrNotFound, the caller's
//     transaction rolls back, and no account, credential, record or edge is
//     created.

// MaxSignupRecords bounds the records one signup ceremony signs.
const MaxSignupRecords = 4

// signupDrafts drafts recs with one nonce and issue time.
func (s *Service) signupDrafts(recs []core.CosignRecord, nonce uuid.UUID, issued time.Time) (drafts [][]byte, chs [][]byte, err error) {
	for _, r := range recs {
		d, ch, err := s.d.Signer.Draft(r.Kind, r.Body, nonce, issued)
		if err != nil {
			return nil, nil, err
		}
		drafts = append(drafts, d)
		chs = append(chs, bytes.Clone(ch[:]))
	}
	return drafts, chs, nil
}

// BeginSignup binds the drafts of recs to guest session sess and returns the
// get options for cred and the drafts (Intent.Record is the JSON array of the
// drafts, in order). A new BeginSignup replaces the session's pending one.
func (s *Service) BeginSignup(ctx context.Context, sess [32]byte, cred core.Credential, recs []core.CosignRecord) (core.Intent, error) {
	if len(recs) == 0 || len(recs) > MaxSignupRecords || len(cred.ID) == 0 || len(cred.PublicKey) == 0 {
		return core.Intent{}, gate.ErrNotFound
	}
	nonce := s.newID()
	now := s.d.Clock.Now()
	drafts, chs, err := s.signupDrafts(recs, nonce, now)
	if err != nil {
		return core.Intent{}, err
	}
	bundle := core.BundleChallenge(chs)
	kinds := make([]int16, 0, len(recs))
	for _, r := range recs {
		kinds = append(kinds, int16(r.Kind))
	}
	tag, err := s.d.DB.Q().Exec(ctx, `INSERT INTO cosign_signups (session_hash, credential_id, nonce, record_kinds, drafts,
		challenge, created_at, expires_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8 WHERE EXISTS (SELECT 1 FROM sessions WHERE id_hash = $1 AND kind = $9)
		ON CONFLICT (session_hash) DO UPDATE SET credential_id = EXCLUDED.credential_id, nonce = EXCLUDED.nonce,
		record_kinds = EXCLUDED.record_kinds, drafts = EXCLUDED.drafts, challenge = EXCLUDED.challenge,
		created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at, used_at = NULL`,
		sess[:], cred.ID, nonce, kinds, drafts, bundle[:], now, now.Add(TTL), int16(core.SessGuest))
	if err != nil {
		return core.Intent{}, err
	}
	if tag.RowsAffected() != 1 {
		return core.Intent{}, gate.ErrNotFound
	}
	opts, err := json.Marshal(protocol.CredentialAssertion{Response: protocol.PublicKeyCredentialRequestOptions{
		Challenge:      protocol.URLEncodedBase64(bundle[:]),
		Timeout:        int(TTL.Milliseconds()),
		RelyingPartyID: s.d.Cfg.RPID,
		AllowedCredentials: []protocol.CredentialDescriptor{{Type: protocol.PublicKeyCredentialType,
			CredentialID: cred.ID}},
		UserVerification: protocol.VerificationRequired,
	}})
	if err != nil {
		return core.Intent{}, err
	}
	return core.Intent{Record: json.RawMessage(append(append([]byte("["), bytes.Join(drafts, []byte(","))...), ']')),
		Options: opts}, nil
}

// FinishSignup verifies the signup ceremony of sess inside tx (see above) and
// returns one assertion per record, each with its draft and the bundle.
func (s *Service) FinishSignup(ctx context.Context, tx store.Q, sess [32]byte, user core.UserID, cred core.Credential, recs []core.CosignRecord, assertion []byte) ([]core.Assertion, error) {
	if tx == nil || user == 0 || len(recs) == 0 {
		return nil, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var (
		credID, challenge []byte
		nonce             uuid.UUID
		kinds             []int16
		drafts            [][]byte
		issued            time.Time
	)
	err := tx.QueryRow(ctx, `UPDATE cosign_signups SET used_at = $2
		WHERE session_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING credential_id, nonce, record_kinds, drafts, challenge, created_at`, sess[:], now).
		Scan(&credID, &nonce, &kinds, &drafts, &challenge, &issued)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, gate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(credID, cred.ID) || len(kinds) != len(recs) || len(drafts) != len(recs) {
		return nil, gate.ErrNotFound
	}
	want, chs, err := s.signupDrafts(recs, nonce, issued)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	for i := range recs {
		if int16(recs[i].Kind) != kinds[i] || !bytes.Equal(want[i], drafts[i]) {
			return nil, gate.ErrNotFound
		}
	}
	if b := core.BundleChallenge(chs); !bytes.Equal(b[:], challenge) {
		return nil, gate.ErrNotFound
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(assertion)
	if err != nil || !s.signupAssertionOK(parsed, cred, challenge) {
		return nil, gate.ErrNotFound
	}
	raw := parsed.Raw.AssertionResponse
	out := make([]core.Assertion, 0, len(recs))
	for i := range recs {
		out = append(out, core.Assertion{CredentialID: bytes.Clone(cred.ID),
			AuthenticatorData: bytes.Clone(raw.AuthenticatorData), ClientDataJSON: bytes.Clone(raw.ClientDataJSON),
			Signature: bytes.Clone(raw.Signature), User: user, Draft: drafts[i], Bundle: chs})
	}
	return out, nil
}

// signupAssertionOK applies the assertion rules of Passkeys.VerifyAssertion to
// the not yet bound credential cred.
func (s *Service) signupAssertionOK(p *protocol.ParsedCredentialAssertionData, cred core.Credential, challenge []byte) bool {
	if !bytes.Equal(p.RawID, cred.ID) {
		return false
	}
	cd := p.Response.CollectedClientData
	if cd.Origin != s.d.Cfg.Origin || cd.CrossOrigin || cd.TopOrigin != "" {
		return false
	}
	err := p.Verify(base64.RawURLEncoding.EncodeToString(challenge), s.d.Cfg.RPID, "", []string{s.d.Cfg.Origin},
		nil, nil, protocol.TopOriginExplicitVerificationMode, false, true, true, cred.PublicKey,
		protocol.SignaturePolicy{})
	if err != nil {
		return false
	}
	flags := p.Response.AuthenticatorData.Flags
	if !flags.HasUserPresent() || !flags.HasUserVerified() || flags.HasBackupEligible() != cred.BackupEligible {
		return false
	}
	// Counter rule: a nonzero counter must move past the registration's.
	n := p.Response.AuthenticatorData.Counter
	return (n == 0 && cred.SignCount == 0) || n > cred.SignCount
}
