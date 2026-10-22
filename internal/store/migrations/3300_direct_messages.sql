-- Direct messages (gate/dm): 1:1 conversations between connected people and
-- small groups, plus posts shared into them. Additive only.
--
-- A direct conversation is unique per pair (pair_lo < pair_hi). A group has a
-- title and up to 32 members (dm.MaxMembers), counting pending invitations.
-- Messages are not signed records: they are private between their members,
-- kept until their sender deletes them (the body is erased at once) or the
-- sender's account is deleted, and purged once every member has cleared the
-- conversation past them or left it (dm.purge).
CREATE TABLE dm_conversations (
  id               uuid        PRIMARY KEY,
  kind             smallint    NOT NULL CHECK (kind IN (0, 1)),         -- 0 direct, 1 group
  pair_lo          bigint,                                              -- direct only: the smaller user id
  pair_hi          bigint,                                              -- direct only: the larger user id
  title            text        CHECK (char_length(title) <= 80),        -- group only; NULL = untitled
  created_by       bigint      NOT NULL,
  created_at       timestamptz NOT NULL,
  last_message_at  timestamptz NOT NULL,
  CHECK ((kind = 0) = (pair_lo IS NOT NULL AND pair_hi IS NOT NULL)),
  CHECK (pair_lo IS NULL OR pair_lo < pair_hi)
);
CREATE UNIQUE INDEX dm_conversations_pair ON dm_conversations (pair_lo, pair_hi) WHERE kind = 0;

CREATE TABLE dm_members (
  conversation_id  uuid        NOT NULL REFERENCES dm_conversations(id) ON DELETE CASCADE,
  user_id          bigint      NOT NULL,
  state            smallint    NOT NULL CHECK (state IN (0, 1)),        -- 0 member, 1 invited (group only)
  admin            boolean     NOT NULL DEFAULT false,
  invited_by       bigint,
  joined_at        timestamptz NOT NULL,                                -- invited or joined
  read_up_to       uuid,                                                -- last message read (UUIDv7, time-ordered)
  cleared_up_to    uuid,                                                -- messages up to here are gone for this member
  muted            boolean     NOT NULL DEFAULT false,
  hidden           boolean     NOT NULL DEFAULT false,                  -- direct: cleared and out of the list until a new message
  PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX dm_members_user ON dm_members (user_id, state);

CREATE TABLE dm_messages (
  id               uuid        PRIMARY KEY,                             -- UUIDv7: ordered by time
  conversation_id  uuid        NOT NULL REFERENCES dm_conversations(id) ON DELETE CASCADE,
  sender_id        bigint      NOT NULL,
  kind             smallint    NOT NULL CHECK (kind IN (0, 1)),         -- 0 text (or shared post), 1 group event
  body             text        CHECK (char_length(body) <= 4000),       -- NULL once deleted, and for events
  shared_post      uuid,                                                -- a post shared into the conversation
  event            text        CHECK (event ~ '^[a-z][a-z_]*$'),        -- kind 1: created | joined | left | removed | renamed
  subject_user     bigint,                                              -- kind 1: the person joined, left or removed
  created_at       timestamptz NOT NULL,
  deleted_at       timestamptz
);
CREATE INDEX dm_messages_conv ON dm_messages (conversation_id, id DESC);
CREATE INDEX dm_messages_sender ON dm_messages (sender_id);

-- Reports on a message (moderation target kind 4): the reported message and a
-- copy of its text as the reporter saw it, so a moderator can judge it after
-- the sender deletes it. Moderators see a message only through a report.
ALTER TABLE reports ADD COLUMN target_message uuid;
ALTER TABLE reports ADD COLUMN evidence text CHECK (char_length(evidence) <= 4000);
CREATE INDEX reports_open_message ON reports (target_message) WHERE state = 0 AND target_message IS NOT NULL;
-- A moderator can remove a reported message (one moderator, like remove_post).
ALTER TABLE mod_actions DROP CONSTRAINT mod_actions_action_check;
ALTER TABLE mod_actions ADD CONSTRAINT mod_actions_action_check CHECK (action IN ('remove_post','suspend_user','dismiss',
  'recovery_approve','quarantine_subtree','csam_report','break_glass','grant_moderator','revoke_moderator','grant_admin',
  'remove_message'));
