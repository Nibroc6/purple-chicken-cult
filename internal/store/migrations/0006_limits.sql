-- Phase 0 (SCAFFOLD-0a): read budgets and write limits (section 17).
CREATE TABLE rate_counters (
  user_id          bigint      NOT NULL,
  bucket           text        NOT NULL,                      -- e.g. 'r:feed_page', 'w:post'
  window_start     timestamptz NOT NULL,                      -- truncated to hour or UTC day
  count            int         NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, bucket, window_start)
);
CREATE INDEX rate_counters_window ON rate_counters (window_start);
