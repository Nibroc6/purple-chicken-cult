-- P1-W06: the unified moderation queue beyond the SCAFFOLD-1 tables (2007/2008).
-- A two-person action belongs to a queue item (reports, csam holds, signals);
-- an item has at most one action awaiting its second moderator.
ALTER TABLE mod_actions ADD COLUMN item_id uuid REFERENCES mod_queue(id);
CREATE UNIQUE INDEX mod_actions_item_pending ON mod_actions (item_id) WHERE state = 0 AND item_id IS NOT NULL;
-- Quarantine counts confirmed suspensions per subject.
CREATE INDEX mod_actions_confirmed ON mod_actions (target_user, done_at) WHERE state = 1 AND action = 'suspend_user';

-- Open reports filed before the queue existed become report items.
INSERT INTO mod_queue (id, source, priority, subject_user, subject_post, report_id, summary, created_at)
SELECT gen_random_uuid(), 0, CASE reason WHEN 'csam' THEN 0 WHEN 'illegal' THEN 1 ELSE 2 END,
       CASE WHEN target_kind = 1 THEN target_user END, target_post, id,
       jsonb_build_object(CASE target_kind WHEN 0 THEN 'kind_post' WHEN 1 THEN 'kind_person' WHEN 2 THEN 'kind_inviter'
                                           ELSE 'kind_preview' END, 1),
       created_at
FROM reports WHERE state = 0;
UPDATE mod_actions a SET item_id = q.id FROM mod_queue q WHERE q.report_id = a.report_id AND a.state = 0;

CREATE INDEX mod_queue_report ON mod_queue (report_id) WHERE report_id IS NOT NULL;
-- Signal sources merge into the open item of the same subject.
CREATE INDEX mod_queue_open_subject ON mod_queue (source, subject_user) WHERE resolved_at IS NULL;
-- Quarantines by root (the 30-day pause, the second offense).
CREATE INDEX quarantines_root ON quarantines (root_inviter, created_at DESC);
-- Removed-account marks expire after 90 days (moderation.expire deletes them).
CREATE INDEX removed_account_marks_expiry ON removed_account_marks (expires_at);
-- signals.purge drops rows after 30 days; the nightly jobs read recent rows.
CREATE INDEX automation_signals_at ON automation_signals (at);
