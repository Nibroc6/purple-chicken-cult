-- P1-W02: handshake protocol v2, friend+ upgrades and the Phase 1 probation rule (additive only).

ALTER TABLE handshake_sessions
  ADD COLUMN scan_index     bigint,   -- frame index (nonce_t) of the frame the claiming device scanned
  ADD COLUMN join_challenge bytea,    -- sha256 of that server-keyed frame: every later join call is signed over (hs, challenge)
  ADD COLUMN assertions     jsonb;    -- co-sign assertions verified earlier, waiting for the record write
                                      -- {"initiator": [...], "peer": [...]} (cleared when the handshake ends)

-- The join side finds its handshake by the claiming session (guest, or the peer's user session).
CREATE INDEX handshake_sessions_claimed ON handshake_sessions (claimed_session, claimed_at DESC)
  WHERE claimed_session IS NOT NULL;

-- Which probation rule an account finishes under (§8): 0 = Phase 0 (7 days and 3 edges outside
-- the own invite subtree), 1 = Phase 1 (30 days and 3 friend+ edges outside it). Accounts that are
-- mid-probation at the cut-over finish under the Phase 0 rule (fidelity #32); every later account,
-- and every account a quarantine puts back on probation, gets the Phase 1 rule.
ALTER TABLE users ADD COLUMN probation_rule smallint NOT NULL DEFAULT 1;
UPDATE users SET probation_rule = 0 WHERE probation_until IS NOT NULL AND probation_cleared_at IS NULL;
