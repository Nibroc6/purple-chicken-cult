-- Admin role management: granting and revoking moderators is a moderation-log
-- entry (mod_actions, state 1, reason role_change). The CHECKs only widen; every
-- existing row stays valid.
ALTER TABLE mod_actions DROP CONSTRAINT mod_actions_action_check;
ALTER TABLE mod_actions ADD CONSTRAINT mod_actions_action_check CHECK (action IN ('remove_post','suspend_user','dismiss',
  'recovery_approve','quarantine_subtree','csam_report','break_glass','grant_moderator','revoke_moderator','grant_admin'));
ALTER TABLE mod_actions DROP CONSTRAINT mod_actions_reason_check;
ALTER TABLE mod_actions ADD CONSTRAINT mod_actions_reason_check CHECK (reason IN ('spam','bot','harassment','csam','illegal',
  'impersonation','misleading_preview','ban_evasion','other','role_change'));
-- The roles page reads the latest role changes.
CREATE INDEX mod_actions_roles ON mod_actions (done_at DESC)
  WHERE state = 1 AND action IN ('grant_moderator','revoke_moderator','grant_admin');
