# Contract request dm-1: direct messages, group chats and sharing a post into them

**Status:** applied in the same change (one PR, at Nibroc's request).

**From:** direct messages · **Kind:** additions only (no existing contract changes
meaning) · **Migration:** `3300_direct_messages.sql`

## Rules chosen (ARCHITECTURE §10.1 said only "a later addition")

* **Who can write to whom.** A 1:1 conversation needs a direct edge (acquaintance or
  friend, §2.2). Someone the viewer cannot see is the uniform not-found; someone visible
  but not connected is `not_connected` (409). An unfriend or a block (either side) stops
  sending with the same `not_connected`, so a block is not revealed; history stays
  readable (`can_send: false`).
* **Groups.** Created with a title (optional) and members from the creator's own
  connections; any member may invite their own connections; ≤ 32 people, invitations
  included. Invitees join or decline; until they join they see only who invited them,
  the title and the member count, and members do not see them. The creator is admin;
  only the admin removes people; admin passes to the earliest remaining member when
  the admin leaves; a group with nobody left is deleted. Any member renames.
* **Probation.** Probation accounts can write 1:1 but cannot create groups or invite
  (`probation`, 409).
* **Blocks inside a group.** A member never sees messages from someone they blocked
  (nor counts them as unread, nor gets a push for them).
* **Shared posts.** The sender must be able to see the post (else the uniform
  not-found). Each reader's view is built from their own horizon on every read
  (`SearchFor` → `CheckPosts` → `Views.Posts`); anyone who could not already see it gets
  `{unavailable: true}` and nothing else. List previews say only "shared a post", never
  its text.
* **Privacy.** No read receipts, no typing indicators, no signed records. Moderators see
  a message only when it is reported; the report keeps the text the reporter saw
  (`reports.evidence`).
* **Retention.** A sender's delete erases the body and the shared post at once (a
  "deleted" stub stays for order). Leaving or deleting a 1:1 conversation clears it for
  that member only; `dm.purge` (hourly) erases messages every member has cleared, and
  conversations with no members. Account deletion erases the account's messages and
  memberships.

## Additions

* **Refs:** `refs.KindConv = 9`, `refs.KindMessage = 10`; `core.Refs.Conv/OpenConv`,
  `Message/OpenMessage`.
* **core:** `core.Message`, `core.Messages` (`ForReport`, `MessageByID`,
  `RemoveMessage`), `core.PushText` / `core.MessagePush`; `Deps.Messages`,
  `Deps.MessagePush` (wired to `gate/dm` and `gate/notify`).
* **gate views:** `ConversationView`, `ConvMember`, `MessagePreview`, `MessageView`,
  `SharedPostView`, `MessagesPage`, `ModMessageView`; `ModItem.message`.
* **notify:** the live topic `message` `{conversation, unread}` (the viewer's total
  unread messages); the job `push.text` renders a catalog key in the recipient's locale
  (`push.message`, `push.message_group`, `push.message_post`, `push.group_invite`), with
  the generic text for `hide_content`. A push goes out only when a conversation turns
  unread for that member (once per unread stretch), never when muted.
* **limits:** read class `messages` (5,000/day); write `message` as a GCRA refill
  (120 at once, one more every 30 s, no 2 s spacing); write `message_group`
  (30/day: create, invite); `social` for join/leave/remove/rename/mute; marking read is
  free.
* **moderation:** report kind `message` (code 4), `reports.target_message` and
  `reports.evidence`; action `remove_message` (one moderator, no blame entry; the sender
  is not told), `message_gone` when already deleted.
* **HTTP** (`api/openapi/messages.yaml`): `GET /messages`, `GET /messages/unread`,
  `POST /messages/direct`, `POST /messages/groups`, `GET|PATCH|DELETE /messages/{ref}`,
  `POST /messages/{ref}/join`, `POST /messages/{ref}/members`,
  `DELETE /messages/{ref}/members/{id}`, `PUT|DELETE /messages/{ref}/mute`,
  `GET|POST /messages/{ref}/messages`, `DELETE /messages/{ref}/messages/{id}`,
  `POST /messages/{ref}/read`.
* **Web:** the `messages` namespace and feature; `FeatureSlots.navBadgeFor` (a badge
  names the nav entry it belongs to; the default stays the notifications entry);
  `PostActions.share` (the share button in `EngageBar`, the sheet at `?share=<post ref>`);
  a Messages entry in the sidebar and the phone tab bar ("Chats"); the Message button on
  a person's page.

## Follow-ups

* **Export:** `GET /export` does not include messages yet.
* **Media:** messages are text plus at most one shared post; pictures are not attached.
* **Rate limits:** if the elastic framework (PR #47) lands after this, `message` goes to
  `Refills` with no `Spaced` entry and `message_group` stays a day cap.
