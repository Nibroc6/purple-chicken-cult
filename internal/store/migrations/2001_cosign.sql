-- Phase 1 (SCAFFOLD-1): passkey co-sign intents. Per-user keys use signing_keys (owner_id set).
CREATE TABLE cosign_intents (
  id               uuid        PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id),
  purpose          text        NOT NULL,                      -- friend_request, friend_accept, unfriend, invite_inviter, invite_invitee,
                                                              -- passkey_add, passkey_remove, account_delete, recovery_approve, block
  record_kind      smallint    NOT NULL,
  draft            bytea       NOT NULL,                      -- exact JCS bytes of the record to sign; the body contains
                                                              -- "nonce" (= this intent's id, server-random) and "issued_at"
  challenge        bytea       NOT NULL,                      -- sha256("pcc-v1:" || kind || draft): unique because of the nonce
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,                      -- 5 minutes
  used_at          timestamptz                                -- consumed in the SAME tx as the record write:
                                                              -- UPDATE ... SET used_at=now() WHERE id=$1 AND used_at IS NULL RETURNING
);
CREATE INDEX cosign_intents_user ON cosign_intents (user_id, created_at DESC);
