-- Phase 1 (SCAFFOLD-1): automation signals, step-ups, device-bound sessions.
CREATE TABLE automation_signals (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          bigint      NOT NULL,
  session_hash     bytea,
  kind             text        NOT NULL,                      -- webdriver, cdp, no_input, sustained_rate, no_seen_reads,
                                                              -- bfs_tree_walk, chain_speed, cadence, similarity, invite_cluster
  score            real        NOT NULL,
  data             jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- aggregates only, no content
  net_h            bytea,                                     -- HMAC(daily salt, IPv4 /24 or IPv6 /56); never a raw IP
  at               timestamptz NOT NULL DEFAULT now()          -- rows purged after 30 d
);
CREATE INDEX automation_signals_user ON automation_signals (user_id, at DESC);

CREATE TABLE stepups (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          bigint      NOT NULL,
  session_hash     bytea       NOT NULL,
  required_at      timestamptz NOT NULL,
  satisfied_at     timestamptz,
  reason           text        NOT NULL
);

ALTER TABLE sessions
  ADD COLUMN dbsc_bound      boolean NOT NULL DEFAULT false, -- once true, NEVER accepts an unbound request again
  ADD COLUMN dbsc_public_jwk jsonb,                          -- ES256 or RS256 (many Windows TPMs are RSA-only)
  ADD COLUMN dbsc_challenge  bytea,                          -- single-use; cleared when consumed
  ADD COLUMN dbsc_refreshed_at timestamptz,
  ADD COLUMN bound_token_hash bytea,                         -- short-lived (10 min) bound cookie __Host-pcc-b
  ADD COLUMN bound_expires_at timestamptz,
  ADD COLUMN short_lived     boolean NOT NULL DEFAULT false;
