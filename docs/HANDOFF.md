# Handoff: state of the build (2026-10-20)

Where development stands, what is left, and how to get a dev environment running. Read with
`docs/IMPLEMENTATION.md` (the plan) and `docs/contract-requests/*.md` (per-package hand-off
notes, which stay the detailed record).

## Status

| Phase | Package | State |
|---|---|---|
| 0 | SCAFFOLD-0a/0b, P0-W01…W12, INT-0 | Done, merged to `main`. DoD in `docs/PHASE0-DOD.md` (13/14; the open item is the 20-person, 2-week trial) |
| 1 | SCAFFOLD-1 | Done, squash-merged to `main` |
| 1 | P1-W01 co-signing, W02 handshake v2, W03 graph P1, W04 media + CSAM, W05 push + server i18n, W06 moderation queue + signals, W07 device-bound sessions + padding + limits v2 | Done on `claude/phase-1`, not yet on `main` |
| 1 | P1-W08 ranking P1 (saves, grandchildren credit, Top) | Done on `claude/project-thread-t09nsu`, PR into `claude/phase-1` |
| 1 | P1-W09…W13 | **Next**, in parallel (plan: W01…W13 in parallel → INT-1); each branches off `claude/phase-1` |
| 1 | INT-1 | After W09…W13 |

At the last run (P1-W08 branch), `CI=1 scripts/check.sh` was fully green: Go vet/lint/tests,
363 Vitest, 157 Playwright (en + en-XA, incl. the INT journeys).

## Branches and merging

- `claude/phase-1` holds the Phase 1 work. **Always squash-merge it into `main`.** Its history
  contains a stray 26 MB `pcc` binary (commits `4d29431`, removed in `fc97a7b`); a normal merge
  would bring it into `main`'s history. `/pcc` is now in `.gitignore`.
- Each work package commits as `p1-wNN: …`; steward changes to scaffold-owned files go in a
  separate `steward: apply contract request p1-wNN-M` commit with `docs/contract-requests/p1-wNN-M.md`.

## Product decisions made during the build

- Hidden-post records are kept 365 days (D31). The family tree applies the reader's hides and
  mutes; the focal post always shows (D32).
- 30-day retention for behavioural signal rows; removed-account marks last 90 days (P1-W06).
- The "original" media variant is capped at 4096 px on the long edge (P1-W04; a 40 MP WebP encode
  takes ~9 s).

## INT-1 work list

Collected from the P1 package reports. Items already resolved by a later package are marked.

**Cross-cutting**
- Raise `router_test` `requirePhase` from 0 to 1 once all P1 routes are registered.
- **Co-sign enforcement** (`records.EnforceCosign`) is off by default. Turn it on once every caller
  registers its purposes and calls `Cosign.Require` (done so far: W02 join + inviter + upgrade, W03
  friendship changes, W06 `mod_action`, W07 passkey add/remove; still to check: W10). Test-hook
  sessions have no passkey and skip co-signing, so their records stay unattested. The
  `cosign_check` purpose exists only under `PCC_ENV=test`.
- **Spelling mismatch:** invites writes the friend+ level as `friend+` in Friendship records, friends
  writes `friend_plus`. Pick one.
- **Conformance suite** (`internal/gate/conformance`): extend the generators to friend+ edges,
  `friend_plus` posts and hide-via; run it once with `PCC_LAZY_BFS=1`; decide whether lazy BFS
  becomes the default.
- **Timing:** KS test and histogram check over the whole padded route set; journey timing
  expectations (padded routes take ≥150 ms, feed/tree ≥600 ms; W07 aligned its padding with the
  graph's 600 ms deep-tier bucket so they don't stack).
- `/api/_test/reset` truncates the jobs table, so periodic jobs never fire in e2e runs (Go tests
  cover them).
- Review the handshake protocol deviations P1-W02 documented in code: later join calls are signed
  over the hash of the scanned frame; the sound MAC also covers the attempt number; the SAS also
  covers the invitee key E_v.

**Hand-offs to later packages**
- W08: asking for a deep graph layer can wait up to 600 ms.
- From W08 (contract request p1-w08-1): `account.UpdateSettings` accepts `rank_preset: top` and
  the settings page offers Top. W10 owns those files and may restyle the change.
- From W08: the `network_likes` and `good_replies` reason texts (integrity, W09) don't mention
  saves on purpose. Saves are private, and the reason must not hint that someone saved the post.
- W10: pass the JSON claim body to `Handshakes.ClaimRecovery`; register co-sign purposes.
- W11: call `Media.Ingest` for preview images.
- W12: call `Media.Attach` in the post transaction, write `post_media`, render media in posts and
  the composer.
- W13: the audio codec must honour `listen({input})` (the `AudioInput` test seam).
- Any new notification kind needs a `push.<kind>` key in
  `internal/platform/i18n/catalogs/en/server.json`, or its push delivery fails.

**Gaps and tests to add**
- People page doesn't show `PersonView.invited_removed` yet.
- Journeys: media (upload → attach → friend views → unfriend → 404), push.
- Add `node --test deploy/cloudflare/media-worker.test.mjs` to `check.sh`.
- Decide whether the `/media/check` and `/cosign/check` pages stay.
- The e2e spec makes moderators with the real `pcc mod grant` CLI; consider a test hook. "Joins after
  removal never sees the mark" is Go-tested only.

**Resolved already:** the friend+ upgrade e2e spec (W03); W06's persistence of `subject_media`,
CSAM item view and two-person NCMEC report (W04 hand-off); `net_h` on W07's signal rows and repeated
step-ups queued (W06 hand-off); the enroll-page CSRF race on first load (fixed at the root in W07).

## Needs the operator before production

- R2 buckets: media (private) and evidence (1-year object lock), plus the Cloudflare Worker secret.
- Production VAPID keys; a `pcc vapid-keys` command and runbook entry are still to be written.
- Egress firewall rule for push services on port 443.
- DBSC enabled in production over https.
- A KEK (production refuses to start without one).

## Dev environment setup (cloud container)

Each fresh container needs:

1. **Postgres 16** (binaries in `/usr/lib/postgresql/16/bin`), as user `postgres`:
   `initdb -D /tmp/pgdata/d` if missing, then
   `pg_ctl -D /tmp/pgdata/d -l /tmp/pgdata/log -o '-p 54329 -c max_connections=400 -k /tmp' start`.
   Create superuser role `pcc` (password `pcc`) and databases `pcc` and `pcc_test`
   (`scripts/lib.sh` has the URLs).
2. **Run scripts with `CI=1`**: there is no Docker daemon.
3. **Web deps:** `npm ci` in `web/` (works under Node 22, though the repo asks for 24).
4. **golangci-lint v2.14.0** (the preinstalled one is too old):
   `cd /tmp && GOTOOLCHAIN=go1.27.0 GOBIN=$HOME/go/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`,
   with `$HOME/go/bin` first on `PATH`.
5. **Playwright** expects Chromium revision 1243 but `/opt/pw-browsers` has 1194. Make
   `/tmp/pwb` with `chromium-1243` and `chromium_headless_shell-1243` symlinked to the 1194
   directories (plus the other entries linked as-is), and run with
   `PLAYWRIGHT_BROWSERS_PATH=/tmp/pwb`. Never run `playwright install`.
6. **Ownership check:** `scripts/check-wp.sh <id>` needs `PCC_SCAFFOLD_BASE=<the package's last
   steward commit>` so steward commits and earlier packages' files are excluded.
7. **Perf and simulation** (not in the normal suite):
   `PCC_REQUIRE_DB=1 go test -tags perf -run 'TestPerf|TestSim' ./internal/gate/feed/ ./internal/gate/conformance/`
   (the bot simulation takes about 6 minutes).
