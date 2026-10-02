-- P0-W11: manual recovery bookkeeping (additive only).
-- Each moderator's fresh step-up time is kept with the approval (security #14).
ALTER TABLE recovery_approvals ADD COLUMN auth_at timestamptz;
-- At most one open or holding recovery per account.
CREATE UNIQUE INDEX recovery_requests_live ON recovery_requests (user_id) WHERE state IN (0, 1);
-- The release job scans holding requests by release time.
CREATE INDEX recovery_requests_release ON recovery_requests (release_at) WHERE state = 1;
-- Deleting a record (purge, unblock) cascades to its parties by record hash.
CREATE INDEX record_parties_record ON record_parties (record_hash);
