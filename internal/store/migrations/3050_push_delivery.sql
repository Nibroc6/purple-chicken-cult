-- P1-W05: Web Push delivery state beyond the SCAFFOLD-1 table (2004_push.sql).
-- SessionEnded deletes the subscriptions of that session (logout, expiry, revocation).
CREATE INDEX push_subscriptions_session ON push_subscriptions (session_hash) WHERE session_hash IS NOT NULL;
-- push.deliver fans a notification out to the recipient's subscriptions in a stable order.
ALTER TABLE push_subscriptions
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(); -- last (re)subscription; the per-user cap drops the oldest
