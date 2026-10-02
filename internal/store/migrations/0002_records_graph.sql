-- Phase 0 (SCAFFOLD-0a): signed records, keys, edges, requests, blocks, invites, handshakes.
CREATE TABLE signing_keys (
  key_id           text        PRIMARY KEY,                   -- "srv-..." or "usr-<uuid>"
  owner_id         bigint      REFERENCES users(id),          -- NULL = server key
  alg              text        NOT NULL DEFAULT 'ed25519',
  public_key       bytea       NOT NULL,
  private_key_enc  bytea,                                     -- P1 user keys only (AES-256-GCM under KEK); server key lives in a file
  valid_from       timestamptz NOT NULL,
  valid_until      timestamptz
);

CREATE TABLE signed_records (
  hash             bytea       PRIMARY KEY,                   -- sha256(body); body already contains "kind" and "schema_version"
  kind             smallint    NOT NULL,                      -- mirror of body.kind (1 Invite, 2 Friendship, 3 Revocation, 4 Post, 5 Tombstone,
                                                              -- 6 Block, 7 Report, 8 PasskeyChange, 9 ModAction)
  author_id        bigint,                                    -- NULL = server-authored (Phase 0)
  key_id           text        NOT NULL REFERENCES signing_keys(key_id),
  body             bytea       NOT NULL,                      -- exact JCS bytes; sig covers "pcc-rec-v1" NUL key_id NUL body
  sig              bytea       NOT NULL,
  cosigs           jsonb,                                     -- P1: [{key_id, sig}]
  assertions       jsonb,                                     -- P1: [{credential_id, authenticator_data, client_data_json, signature}]
  schema_version   smallint    NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  purge_after      timestamptz                                -- section 2.3: revoked edge records = revocation + 30 d
);
CREATE INDEX signed_records_purge ON signed_records (purge_after) WHERE purge_after IS NOT NULL;
CREATE TABLE record_parties (
  user_id          bigint      NOT NULL,
  record_hash      bytea       NOT NULL REFERENCES signed_records(hash) ON DELETE CASCADE,
  PRIMARY KEY (user_id, record_hash)
);

CREATE TABLE edges (                                           -- both directions stored
  user_id          bigint      NOT NULL REFERENCES users(id),
  friend_id        bigint      NOT NULL REFERENCES users(id),
  level            smallint    NOT NULL CHECK (level IN (1, 2)),  -- 1 friend, 2 friend+
  since            timestamptz NOT NULL,
  record_hash      bytea       NOT NULL,
  PRIMARY KEY (user_id, friend_id),
  CHECK (user_id <> friend_id)
);
CREATE INDEX edges_friend ON edges (friend_id);

CREATE TABLE hide_via (                                        -- created in P0 (empty) so Graph.Load never depends on P1
  user_id          bigint      NOT NULL REFERENCES users(id), -- reader never routes THROUGH via_id; used from P1
  via_id           bigint      NOT NULL REFERENCES users(id),
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, via_id)
);

CREATE TABLE edge_events (                                     -- revoke rows purged after 30 days (section 2.3)
  id               uuid        PRIMARY KEY,
  a                bigint      NOT NULL,
  b                bigint      NOT NULL,
  kind             smallint    NOT NULL,                      -- 0 create, 1 upgrade, 2 revoke
  at               timestamptz NOT NULL,
  record_hash      bytea
);
CREATE INDEX edge_events_at ON edge_events (at);

CREATE TABLE friend_requests (
  from_id          bigint      NOT NULL REFERENCES users(id),
  to_id            bigint      NOT NULL REFERENCES users(id),
  state            smallint    NOT NULL DEFAULT 0,            -- 0 pending, 1 declined (looks pending to sender), 2 accepted, 3 withdrawn
                                                              -- a request TO someone who blocked the sender is stored as 1 at creation
  created_at       timestamptz,                               -- nulled 30 d after decline
  decided_at       timestamptz,                               -- nulled with created_at
  draft            bytea,                                     -- P1: JCS Friendship draft both parties sign
  draft_assertion  jsonb,                                     -- P1: requester's passkey assertion
  PRIMARY KEY (from_id, to_id),
  CHECK (from_id <> to_id)
);
CREATE INDEX friend_requests_to ON friend_requests (to_id) WHERE state = 0;

CREATE TABLE blocks (
  blocker          bigint      NOT NULL REFERENCES users(id),
  blocked          bigint      NOT NULL REFERENCES users(id),
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (blocker, blocked)
);
CREATE INDEX blocks_blocked ON blocks (blocked);

CREATE TABLE mutes (
  user_id          bigint      NOT NULL REFERENCES users(id),
  author_id        bigint      NOT NULL REFERENCES users(id),
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, author_id)
);

CREATE TABLE invites (
  id               uuid        PRIMARY KEY,
  inviter_id       bigint      NOT NULL REFERENCES users(id),
  invitee_id       bigint      NOT NULL UNIQUE REFERENCES users(id),
  created_at       timestamptz NOT NULL,
  handshake        jsonb       NOT NULL,                      -- {mode, channels_passed, timings_bucketed}
  invite_record    bytea       NOT NULL,
  friendship_record bytea      NOT NULL
);
CREATE INDEX invites_inviter ON invites (inviter_id);

CREATE TABLE invite_budgets (
  user_id          bigint      PRIMARY KEY REFERENCES users(id),
  tokens           double precision NOT NULL DEFAULT 0,
  cap              smallint    NOT NULL DEFAULT 3,            -- genesis 5
  refreshed_at     timestamptz NOT NULL DEFAULT now(),
  paused_until     timestamptz,                               -- quarantine (section 8)
  revoked          boolean     NOT NULL DEFAULT false,
  offenses         smallint    NOT NULL DEFAULT 0
);

CREATE TABLE handshake_sessions (
  id               uuid        PRIMARY KEY,
  kind             smallint    NOT NULL,                      -- 0 invite, 1 friend_plus upgrade (P1), 2 recovery (P1)
  initiator_id     bigint      NOT NULL REFERENCES users(id),
  peer_user_id     bigint      REFERENCES users(id),          -- upgrade target / account being recovered
  secret           bytea       NOT NULL,                      -- frame MAC key (never leaves server)
  state            smallint    NOT NULL DEFAULT 0,            -- 0 open, 1 claimed, 2 confirmed (SAS matched), 3 done, 4 cancelled, 5 expired
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  claimed_session  bytea       REFERENCES sessions(id_hash) ON DELETE SET NULL,
  claimed_at       timestamptz,
  confirmed_at     timestamptz,                               -- inviter tapped "codes match" (security #6)
  handle_tries     smallint    NOT NULL DEFAULT 0,            -- handle_taken answers; at most 10 per handshake
  pending          jsonb,                                     -- {handle, display_name, locale, languages, webauthn_user_id}
  completed_at     timestamptz
);
CREATE INDEX handshake_sessions_initiator ON handshake_sessions (initiator_id, state);
