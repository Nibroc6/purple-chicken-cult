-- Recovery hardening (§10.3, additive only).
-- How many guardians a guardian recovery needs (the one who vouched in person
-- included), fixed when it opens: max(2, ceil(log2(friend+ count))), at most 5.
ALTER TABLE recovery_requests ADD COLUMN guardians_needed smallint NOT NULL DEFAULT 2;
-- When a credential was enrolled through recovery: for RecoveryLockFor it may
-- not remove the account's older passkeys.
ALTER TABLE webauthn_credentials ADD COLUMN recovered_at timestamptz;
