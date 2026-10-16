-- P0-W12: moderation bookkeeping (additive only).
-- Open reports are looked up by target (one resolution closes every open report on it).
CREATE INDEX reports_open_post ON reports (target_post) WHERE state = 0 AND target_post IS NOT NULL;
CREATE INDEX reports_open_user ON reports (target_user) WHERE state = 0 AND target_user IS NOT NULL;
-- The first moderator's fresh step-up time of a two-person action (security #14);
-- the second one's goes into second_auth.
ALTER TABLE mod_actions ADD COLUMN first_auth_at timestamptz;
-- A report has at most one two-person action awaiting the second moderator.
CREATE UNIQUE INDEX mod_actions_pending ON mod_actions (report_id) WHERE state = 0 AND report_id IS NOT NULL;
-- The public moderation log reads confirmed actions by day.
CREATE INDEX mod_actions_done ON mod_actions (done_at) WHERE state = 1;
-- §10.4: the removed post is shown in the blame inbox only if the inviter could
-- already see it, so the excerpt the inviter saw is kept with the entry (never for
-- csam). NULL = no post shown.
ALTER TABLE blame_inbox ADD COLUMN post_excerpt text CHECK (char_length(post_excerpt) <= 280);
ALTER TABLE blame_inbox ADD COLUMN post_lang text;
ALTER TABLE blame_inbox ADD COLUMN post_created_at timestamptz;
ALTER TABLE blame_inbox ADD COLUMN post_audience smallint;
-- CSP violation reports: an aggregate count per UTC day and directive only
-- (no URLs, no user data, no IPs).
CREATE TABLE csp_violations (
  day              date        NOT NULL,
  directive        text        NOT NULL CHECK (directive ~ '^[a-z][a-z-]*$'),
  count            bigint      NOT NULL DEFAULT 0,
  PRIMARY KEY (day, directive)
);
