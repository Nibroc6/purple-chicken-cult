-- P1-W07: device-bound sessions, co-signed passkey additions, step-up history.
ALTER TABLE sessions
  ADD COLUMN dbsc_challenge_at timestamptz,            -- when dbsc_challenge was issued (a pending offer is reused for 5 min)
  ADD COLUMN pending_passkey   jsonb;                  -- a verified new credential awaiting its co-signature (5 min)
-- Repeated step-ups of one account go to moderators (§17.2(5)).
CREATE INDEX stepups_user ON stepups (user_id, required_at DESC);
-- At most one open step-up per session.
CREATE UNIQUE INDEX stepups_open ON stepups (session_hash) WHERE satisfied_at IS NULL;
