-- Phase 0 (SCAFFOLD-0a): reports, moderator actions, blame inbox, account stubs, recovery.
CREATE TABLE reports (
  id               uuid        PRIMARY KEY,
  reporter_id      bigint      NOT NULL REFERENCES users(id),
  target_kind      smallint    NOT NULL,                      -- 0 post, 1 person, 2 inviter (routed via invitee), 3 preview_misleading
  target_post      uuid,
  target_user      bigint,                                    -- for kind 2: the INVITEE; inviter resolved by moderators
  reason           text        NOT NULL CHECK (reason IN ('spam','bot','harassment','csam','illegal','impersonation',
                                                          'misleading_preview','other')),
  note             text        CHECK (char_length(note) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  state            smallint    NOT NULL DEFAULT 0,            -- 0 open, 1 actioned, 2 dismissed
  resolved_by      bigint,
  resolved_at      timestamptz
);
CREATE INDEX reports_open ON reports (state, created_at);

CREATE TABLE mod_actions (
  id               uuid        PRIMARY KEY,
  moderator_id     bigint      NOT NULL REFERENCES users(id),
  second_moderator bigint      REFERENCES users(id),          -- two-person actions (suspend, recovery, quarantine, break-glass)
  state            smallint    NOT NULL DEFAULT 1,            -- 0 awaiting second moderator, 1 done, 2 expired (24 h)
  action           text        NOT NULL CHECK (action IN ('remove_post','suspend_user','dismiss','recovery_approve',
                                                          'quarantine_subtree','csam_report','break_glass')),
  target_post      uuid,
  target_user      bigint,
  reason           text        NOT NULL CHECK (reason IN ('spam','bot','harassment','csam','illegal','impersonation',
                                                          'misleading_preview','ban_evasion','other')),
  report_id        uuid        REFERENCES reports(id),
  second_auth      jsonb,                                     -- P0: {at, auth_at} of the 2nd moderator's fresh UV step-up;
                                                              -- P1: passkey assertion over the JCS draft of this action
  report_ref       text,                                      -- csam_report: the NCMEC CyberTipline report id
  created_at       timestamptz NOT NULL DEFAULT now(),
  done_at          timestamptz
);
CREATE INDEX mod_actions_created ON mod_actions (created_at);

CREATE TABLE blame_inbox (                                     -- section 10.4: entry for the inviter after confirmed action
  id               uuid        PRIMARY KEY,
  inviter_id       bigint      NOT NULL REFERENCES users(id),
  invitee_id       bigint      NOT NULL REFERENCES users(id),
  action           text        NOT NULL CHECK (action IN ('remove_post','suspend_user')),
  reason           text        NOT NULL CHECK (reason IN ('spam','bot','harassment','csam','illegal','impersonation',
                                                          'misleading_preview','ban_evasion','other')),
  post_id          uuid,                                      -- shown only if the inviter could already see it
  created_at       timestamptz NOT NULL DEFAULT now(),
  read_at          timestamptz
);
CREATE INDEX blame_inbox_inviter ON blame_inbox (inviter_id, created_at DESC);

CREATE TABLE account_stubs (                                   -- moderators only (section 10.2)
  user_id          bigint      PRIMARY KEY,
  invited_by       bigint,
  invitees         bigint[]    NOT NULL DEFAULT '{}',
  deleted_at       timestamptz NOT NULL
);

CREATE TABLE recovery_requests (
  id               uuid        PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id),
  kind             smallint    NOT NULL,                      -- 0 manual (two moderators, P0), 1 guardians (two friend+, P1)
  state            smallint    NOT NULL DEFAULT 0,            -- 0 open, 1 approved (holding), 2 released, 3 cancelled, 4 expired
  created_at       timestamptz NOT NULL DEFAULT now(),
  release_at       timestamptz,                               -- approval + 24 h hold (P0 manual AND P1 guardians; security #14)
  revoke_old       boolean     NOT NULL DEFAULT true,         -- on release: revoke all sessions
  claimed_session  bytea,                                     -- P1: device that will enroll
  enrollment_token bytea                                      -- hash; set on release
);
CREATE TABLE recovery_approvals (
  request_id       uuid        NOT NULL REFERENCES recovery_requests(id),
  approver_id      bigint      NOT NULL REFERENCES users(id),
  role             smallint    NOT NULL,                      -- 0 moderator, 1 guardian
  at               timestamptz NOT NULL DEFAULT now(),
  assertion        jsonb,                                     -- P1 guardian passkey assertion
  PRIMARY KEY (request_id, approver_id)
);
