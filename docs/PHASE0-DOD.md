# Phase 0 definition of done — evidence (INT-0)

The checklist of ARCHITECTURE §18 "Phase 0 definition of done", ticked with the
tests that show each item (plan §7, INT-0 deliverables). Go tests run in
`scripts/check.sh` (`go test -p 4 ./...` against Postgres, `PCC_REQUIRE_DB=1`);
`TestPerf*` / `TestSim*` run with `go test -tags perf` (CI nightly / on demand,
plan §7); Playwright specs run in `scripts/e2e.sh` in the `en` and `en-XA`
projects. Journeys and the integrity and pseudo-locale passes are INT-0's
(`web/e2e/journeys/**`, `web/e2e/integrity.spec.ts`); every journey page fails
on a CSP or Trusted Types violation (`journeys/helpers.ts`).

Status on 2026-10-16: 13 of 14 items done. `CI=1 scripts/check.sh` is green (Go, lint,
archtest, 295 Vitest tests, 111 Playwright tests in en + en-XA, 1 en-only skip). The one open item is the trial
itself, which needs real people. Its measurements are in place (`pcc metrics`).

## Checklist

- [x] **Genesis users are seeded by CLI. Everyone else joins only via an
      in-person rotating-QR token.**
  - CLI `pcc genesis` → `auth.SeedGenesis`: `internal/gate/auth` `TestGenesisEnrollThenLogin`,
    `TestEnrollmentTokenBindingAndRules`; `internal/gate/wire` `TestWiredAppOverDatabase`.
  - Join only through a claimed handshake: `internal/gate/invites` `TestJoinEndToEnd`,
    `TestHandshakeLifecycle`, `TestClaimFailuresAreUniform`, `TestBystanderGetsADifferentCode`,
    `TestConcurrentRedeemCreatesOneUser`, `TestRegisterFinishWithoutCeremonyIsNotFound`.
  - End to end with a fake camera and a virtual authenticator: `web/e2e/p0-w01.spec.ts`
    (genesis enrollment), `web/e2e/p0-w04.spec.ts`, and the journey
    `web/e2e/journeys/onboarding.spec.ts` (enroll → QR → SAS → join → friends and
    "invited by @alice").
- [x] **Invite budget and probation are enforced server-side.**
  - `internal/gate/invites` `TestBudgetAccruesToTheCap`, `TestFriendCap`,
    `TestProbationClearsWhenBothPartsHold`, `TestResetPutsUsersBackOnProbation`;
    `internal/gate/posts` `TestProbationForcesD1`; `internal/gate/views`
    `TestAudienceForcedOnlyOnOwnView`, `TestForcedPostReadsD1WhateverIsStored`;
    `internal/gate/limits` `TestProbationFriendRequests`; `internal/gate/feed` `TestLowTrustCollapse`.
  - Journey: `onboarding.spec.ts` (the new invitee's post is forced friends-only, the note is
    shown to him only; probation shown on the inviter's invitee list).
- [x] **Friendships need both sides to accept. Unfriend and block take effect on
      the next load.**
  - `internal/gate/friends` `TestRequestAccept`, `TestCrossedRequestsAccept`,
    `TestRequesterViewUniform`, `TestBlockUnblock`, `TestUnfriendAndBlockFixtureEdges`;
    `internal/gate/feed` `TestNextPageRechecks`; `internal/gate/conformance` `TestFreshness`,
    `TestRequestToBlockerLooksDeclined`.
  - e2e: `p0-w03.spec.ts` (request → accept, block, unblock), `p0-w07.spec.ts` (block
    mid-session), journey `journeys/horizon.spec.ts` "unfriending takes effect on the next
    request, even inside an open feed session" (next page of the same session; the
    friend-of-friend loses the post on the next request).
- [x] **Feed: unseen tier 1 → 2 → 3 → … at exact shortest distance, with no
      maximum, then "caught up".**
  - `internal/gate/graph` `TestDistEqualsReferenceBFS`, `TestSynthAgreesWithReference`;
    `internal/gate/feed` `TestTiersMarkersAndCaughtUp`, `TestUnseenFirstStrict`;
    `internal/gate/conformance` `TestNoninterference` (tier == `graph.ReferenceBFS` property).
  - e2e: `p0-w07.spec.ts` "tiers in order under marker headers…"; journey
    `horizon.spec.ts` (tier 1 for a friend, tier 2 under its own header for a friend of a
    friend); `conversation.spec.ts` (seen posts leave the next session).
- [x] **Every feed item shows a shortest-path chain, collapsed per §4.5.**
  - `internal/gate/visibility` `TestChainNaming`, `TestChainFirstHopPlus`, `TestAgreesWithReference`;
    `internal/gate/graph` `TestFirstHopEqualsReferenceAndOrderIndependent`, `TestD22FirstHopRule`;
    `internal/gate/conformance` `TestNoninterference` (chain oracle: `len(chain) == tier`,
    first hop == `graph.ReferenceFirstHop`); web `src/integrity/integrity.test.tsx` (Chain).
  - e2e: journeys `horizon.spec.ts`, `onboarding.spec.ts`, `conversation.spec.ts` (chain
    through @bob); `integrity.spec.ts` (visible and legible in both formats).
- [x] **"Latest" is exactly chronological. "Balanced" follows §5.5.**
  - `internal/gate/rank` `TestPropertyLatestIsChronological`, `TestPropertyPermutation`,
    `TestBalancedShiftAndReasons`, `TestPropertyAuthorCap`, `TestReplyPenalty`,
    `TestShowLessPenalty`, `TestNoNetworkNoCredit`; `internal/gate/engage` `TestFarLikesNeverChangeOrder`;
    `internal/gate/feed` `TestPresets`, `TestSnapshotRanksTheWholeTier`.
  - e2e: `p0-w07.spec.ts` "Latest is newest first and becomes the saved order"; journey
    `conversation.spec.ts` (switching Balanced ↔ Latest, saved as the default).
- [x] **Horizon tests pass on `gate`: a user in a separate component gets
      responses identical to a world where the other component doesn't exist.**
  - `internal/gate/conformance` `TestNoninterference` (normalized W1/W2 comparison with H(V)
    held fixed, component isolation scan, oracle uniformity, foreign tokens),
    `TestOtherComponentInvisible`, `TestFreshness`; `internal/api/router`
    `TestUniformNotFound`, `TestChargingUniformForBodyRefs` (charging uniformity, commit
    c95a540); `internal/api/*api` `TestInvisibleRefsAreUniform`.
  - Journey: `horizon.spec.ts` (another component's reader sees only their own component;
    its refs answer byte-identically to garbage).
- [x] **A seeded bot with zero real edges never appears in any feed.**
  - `internal/gate/conformance` `TestNoninterference` (seeded zero-edge bot property),
    `TestSimBotIsolation` (perf tag; 500-account bot subtree on one bridge edge: 0.026% of
    impressions at degree ≤ 3 with the bridge — budget 0.1% — and exactly 0 after the
    unfriend; run 2026-10-16).
- [x] **On a synthetic 10k-user graph, tiers 3–6 match a reference BFS, the
      §4.2 planner picks the cheaper scan, and p95 feed open is under 50 ms.**
  - `internal/gate/feed` `TestPerfFeed10k` (perf tag; commit 96bb50b). Run on 2026-10-16:
    p50 15.2 ms, **p95 41.9 ms** (budget 50 ms) over 200 cold readers; 800 tiers 3–6 equal
    the reference BFS; 287 planner choices, 0 wrong. `internal/gate/graph`
    `TestSearch10kP95` (BFS p95 1.6 ms), `TestPlan`, `TestPlannerChoiceIsRecorded`.
- [x] **Export produces records that verify against the published keys.**
  - `internal/gate/records` `TestExportOfFriendsSharesByteIdenticalRecord`,
    `TestVerifyExportRejectsTampering`, `TestVerifyExportKeyWindow`, `TestRotateKeysAndPurge`;
    `internal/gate/account` `TestExportAndDeleteNeedFreshAuth`.
  - e2e: `p0-w11.spec.ts` (download needs a fresh step-up); journey `onboarding.spec.ts`
    downloads the export and runs `pcc export-verify FILE --fingerprints` pinned to
    `/api/keys`: every record valid, keys pinned.
- [x] **Posts can reference up to 3 parents. The tree view ranks each row like
      the feed, and the tree noninterference tests pass (§13).**
  - `internal/gate/posts` `TestParentProblems` (`too_many_parents`), `TestReferenceResolution`,
    `TestRefCap`, `TestShadowLink`; `internal/gate/tree` `TestRowOrder`, `TestPresets`,
    `TestOpenRowsAroundFocal`, `TestDiamondShownOnce`, `TestNoninterference`,
    `TestHiddenParentsLookLikeRoot`, `TestTraversalNeverThroughInvisible`;
    `internal/gate/conformance` `TestNoninterference` (tree open / row / more calls).
  - e2e: `p0-w08.spec.ts`; journey `conversation.spec.ts` (a reply to two parents: "also
    replies to @bob" below, both parents above when refocused); `horizon.spec.ts` (a blocked
    reader's reply is a shadow link the blocker never sees).
  - Note: the composer UI attaches one parent (`/compose?parent=&kind=`); two or three
    parents are created through `POST /api/posts` (the journey does this). A multi-parent
    picker is not part of any Phase 0 work package.
- [x] **Markdown headings render at body size, and link previews are built
      server-side, per post.**
  - web `src/markdown/markdown.test.tsx` "a heading-only post renders as one
    paragraph-sized line"; `internal/gate/previews` `TestPreviewAppearsAtTheFirstBucket`,
    `TestAtMostThreeDistinctLinks`, `TestCacheAcrossPosts`, `TestOptOutDeleteAndEdit`,
    `TestFailuresNeverShowAndNeverRetry`; `internal/platform/safefetch` tests.
  - e2e: `p0-w09.spec.ts` "the Markdown subset renders safely", "link previews are built by
    the server and appear only at the fixed delay".
- [x] **The whole app renders in the pseudo-locale with no untranslated or
      truncated text, and the string lint passes.**
  - `web/e2e/journeys/pseudo-locale.spec.ts` (en-XA): 40 pages and states — signed out,
    member (feed in Cards and Compact, "why" panel, tree, likes, post management,
    people, requests, notifications, seen, every settings page, invites, composer, report),
    moderator (queue, recovery, log), the blame inbox — with no visible text outside the
    `[ … ]` markers (user content in `data-user-content`, Intl output in `<time>` /
    `data-intl`) and no truncated integrity element. It found display names and Intl
    language names unmarked (fixed in 8efb7c3).
  - Every Playwright spec also runs in the `en-XA` project; per-feature Vitest
    pseudo-locale passes; `npm run lint` (eslint-plugin-i18next `no-literal-string`,
    `scripts/check-i18n.mjs`, `check-css-logical.mjs`) in `scripts/check.sh`.
  - Integrity elements (§15.5): `web/e2e/integrity.spec.ts` — all seven present, visible,
    ≥ 12 px, buttons ≥ 24 × 24 px, not truncated, text contrast ≥ 4.5:1 and button borders
    ≥ 3:1 against the painted background, in Cards and Compact × light/dark × normal/high
    contrast, on a feed card and on the tree's focal post.
- [ ] **20 real people use it for 2 weeks. Measure invites per day, feed depth,
      the mix of posts by degree, and preset switching.** — **Open.**
  - The trial has not been run: it needs a deployment and 20 people, which no test can
    stand in for.
  - The measurements are ready: `pcc metrics [--days 14] [--json] [--raw]` prints
    invites redeemed per day, feed depth (sessions by deepest tier reached), impressions by
    degree (the mix of posts by degree), preset switches and the daily feed p95 from
    `metrics_daily` (aggregates only). Tests: `internal/gate/metrics`
    `TestSummarizeAnswersTheTrialQuestions`, `TestSummaryFromRecordedCounters`,
    `TestFlushWritesOnlyAggregates`, `TestClosedListRejectsEverythingElse`; `cmd/pcc`
    `TestPrintMetrics`, `TestWriteSummaryTables`; the producers' tests in `internal/gate/{feed,engage,invites}`.

## INT-0 end-to-end journeys (plan §7 "Required e2e journeys")

| Journey (plan §7) | Spec |
|---|---|
| genesis enroll → invite → SAS confirm → join (fake camera + virtual authenticator) → both see the friendship and "invited by" | `journeys/onboarding.spec.ts` |
| post → friend tier 1, friend of a friend tier 2 with chain, stranger in another component sees nothing | `journeys/horizon.spec.ts` |
| reply with parents → tree rows | `journeys/conversation.spec.ts`, `journeys/onboarding.spec.ts` |
| split into thread | `journeys/conversation.spec.ts` |
| like → "in your network" count | `journeys/conversation.spec.ts`, `journeys/onboarding.spec.ts` |
| unfriend → gone on the next request, even mid-session | `journeys/horizon.spec.ts` |
| block → the blocked user can still reference and gets a shadow link the blocker never sees | `journeys/horizon.spec.ts` |
| export downloads (after step-up) and `pcc export-verify` passes | `journeys/onboarding.spec.ts` |
| manual recovery with hold + cancel | `journeys/conversation.spec.ts` |
| pseudo-locale pass over all pages | `journeys/pseudo-locale.spec.ts` |
| integrity elements visible with contrast ≥ AA in every format | `integrity.spec.ts` |
| CSP / Trusted Types violation listener on every test | `journeys/helpers.ts` (auto fixture) |
| also: notifications live over SSE, seen, presets, report → moderator removal → inviter's blame inbox, account deletion | `journeys/onboarding.spec.ts`, `journeys/conversation.spec.ts` |

## Integration bugs found by the INT-0 (c) journeys

- The moderation queue never showed the subject's moderator-scoped account reference,
  which the recovery page asks for ("copy it from the moderation queue"): no moderator
  could open a manual recovery through the UI. Fixed in 76c30b4 (`queue-subject-ref`; Vitest
  assertion in `moderation.test.tsx`, journey `conversation.spec.ts`).
- Display names (feed and tree cards in both formats, person cards) were not marked as user
  content, and Intl language names (composer, post management, language settings) were not
  marked as Intl output, so the pseudo-locale pass could not tell them from untranslated
  literals. Fixed in 8efb7c3.

## Known deviations (not DoD items)

- The UI language follows `navigator.languages` (or `?lng=`) only; `settings.ui_locale` is
  not applied (plan §6.5). Phase 0 ships English and the pseudo-locale only, so it has no
  visible effect yet.
