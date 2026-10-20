-- P1-W01: when co-signing becomes mandatory. From cosign_required_from on, every
-- Invite, Friendship and Revocation record and every PasskeyChange add after an
-- account's first must carry a passkey assertion, and exports publish the time so
-- offline verifiers flag records without one. NULL (the default) until every
-- caller co-signs (INT-1).
CREATE TABLE records_policy (
  id                   smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  cosign_required_from timestamptz
);
INSERT INTO records_policy (id) VALUES (1);
