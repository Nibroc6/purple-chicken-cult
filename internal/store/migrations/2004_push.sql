-- Phase 1 (SCAFFOLD-1): Web Push subscriptions.
CREATE TABLE push_subscriptions (
  id               uuid        PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_hash     bytea,                                     -- removed on logout of that session
  endpoint         text        NOT NULL UNIQUE,               -- re-subscribing an existing endpoint reassigns it to the current user
  p256dh           bytea       NOT NULL,
  auth             bytea       NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_ok_at       timestamptz,
  failures         int         NOT NULL DEFAULT 0,
  hide_content     boolean     NOT NULL DEFAULT false         -- generic "new activity" text on the lock screen
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);
