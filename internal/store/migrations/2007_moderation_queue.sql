-- Phase 1 (SCAFFOLD-1): unified moderation queue, invite-tree quarantine, removed-account marks.
CREATE TABLE mod_queue (
  id               uuid        PRIMARY KEY,
  source           smallint    NOT NULL,                      -- 0 report, 1 signal cluster, 2 csam, 3 stepup repeat, 4 invite cluster
  priority         smallint    NOT NULL DEFAULT 1,            -- 0 critical (csam) .. 3 low
  subject_user     bigint,
  subject_post     uuid,
  report_id        uuid        REFERENCES reports(id),
  summary          jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- codes + counts
  created_at       timestamptz NOT NULL DEFAULT now(),
  claimed_by       bigint,
  claimed_at       timestamptz,
  resolved_at      timestamptz,
  resolution       text
);
CREATE INDEX mod_queue_open ON mod_queue (priority, created_at) WHERE resolved_at IS NULL;

CREATE TABLE quarantines (                                     -- section 8 invite-tree quarantine (k = PCC_QUARANTINE_K, default 3)
  id               uuid        PRIMARY KEY,
  root_inviter     bigint      NOT NULL,
  confirmed_bad    int         NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       bigint      NOT NULL,
  second_moderator bigint      NOT NULL
);
CREATE TABLE removed_account_marks (                           -- "invited a removed account", 90 days
  inviter_id       bigint      NOT NULL,
  removed_id       bigint      NOT NULL,
  viewers          bytea       NOT NULL,                      -- serialized roaring bitmap: users with a path to the removed
                                                              -- account at removal time (its component); row deleted at expiry
  at               timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  PRIMARY KEY (inviter_id, removed_id)
);
ALTER TABLE invite_budgets ADD COLUMN diversity real NOT NULL DEFAULT 1.0; -- section 8 diversity decay multiplier
