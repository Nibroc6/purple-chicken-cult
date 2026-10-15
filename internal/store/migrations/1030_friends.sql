-- P0-W03: friendships, requests, blocks (additive only).
-- The Block record of a block, deleted together with the row on unblock (security #16).
ALTER TABLE blocks ADD COLUMN record_hash bytea;
-- Outgoing requests by sender: cap accounting (D23) and the daily request limit.
CREATE INDEX friend_requests_from ON friend_requests (from_id, state);
-- The scrub job scans declined requests by decision time.
CREATE INDEX friend_requests_declined ON friend_requests (decided_at) WHERE state = 1 AND decided_at IS NOT NULL;
