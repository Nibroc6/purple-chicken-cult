-- P1-W10: guardian recovery (two friend+ guardians, §10.3; additive only).
-- The in-person handshake guardian A opened for the request, and when its
-- outcome was first seen passed (the other guardians are told then).
ALTER TABLE recovery_requests
  ADD COLUMN handshake  uuid,
  ADD COLUMN vouched_at timestamptz;
-- The recovering device finds its request by the sha256 of its recovery
-- ticket (claimed_session holds that hash for guardian requests).
CREATE INDEX recovery_requests_claimed ON recovery_requests (claimed_session) WHERE claimed_session IS NOT NULL;
