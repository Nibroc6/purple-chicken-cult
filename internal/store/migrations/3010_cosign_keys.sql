-- P1-W01: custodial user keys, the co-sign enforcement cut-over and the invitee signup ceremony (additive only).

-- At most one current custodial key per user (signing_keys rows with owner_id set; the
-- private key is AES-256-GCM-wrapped under PCC_KEK with AAD key_id || owner_id).
CREATE UNIQUE INDEX signing_keys_owner_current ON signing_keys (owner_id)
  WHERE owner_id IS NOT NULL AND valid_until IS NULL;

-- One row: from when graph and passkey records must carry a passkey assertion. Published in
-- export keys.json as "cosign_since"; VerifyExport flags later high-value records without one.
CREATE TABLE records_policy (
  id               boolean     PRIMARY KEY DEFAULT true CHECK (id),
  cosign_since     timestamptz
);

-- The invitee signup ceremony (fidelity #21): one pending get assertion per guest session,
-- with the NEW credential over the drafts of the signup's records; consumed inside the
-- RegisterFinish transaction (UPDATE ... WHERE used_at IS NULL RETURNING).
CREATE TABLE cosign_signups (
  session_hash     bytea       PRIMARY KEY,                   -- sha256 of the guest session token
  credential_id    bytea       NOT NULL,
  nonce            uuid        NOT NULL,
  record_kinds     smallint[]  NOT NULL,
  drafts           bytea[]     NOT NULL,                      -- exact draft bytes, in order
  challenge        bytea       NOT NULL,                      -- core.BundleChallenge over the drafts' challenges
  created_at       timestamptz NOT NULL,
  expires_at       timestamptz NOT NULL,
  used_at          timestamptz
);
CREATE INDEX cosign_intents_expires ON cosign_intents (expires_at);
