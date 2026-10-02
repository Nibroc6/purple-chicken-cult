-- Phase 0 (SCAFFOLD-0a): in-app notifications.
CREATE TABLE notifications (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id),
  kind             text        NOT NULL,                      -- friend_request, request_accepted, post_referenced, mention,
                                                              -- invitee_action, friend_security, invite_redeemed, recovery_approval (P1)
  group_key        text,                                      -- batching key, e.g. 'post_referenced:<parent uuid>'
  subject_user     bigint,                                    -- raw; turned into a per-recipient ref at read time
  subject_post     uuid,
  data             jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- codes and numbers only
  count            int         NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  read_at          timestamptz,
  delivered_at     timestamptz                                -- SSE/push batch flush marker
);
CREATE INDEX notifications_user ON notifications (user_id, updated_at DESC);
CREATE UNIQUE INDEX notifications_batch ON notifications (user_id, group_key) WHERE read_at IS NULL AND group_key IS NOT NULL;

CREATE TABLE notification_mutes (
  user_id          bigint      NOT NULL,
  post_id          uuid        NOT NULL,
  PRIMARY KEY (user_id, post_id)
);
