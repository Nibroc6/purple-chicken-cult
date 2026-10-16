# Implementation Plan — Phase 0 + Phase 1

> **Working on a package?** Read [SCAFFOLD.md](SCAFFOLD.md) first: it lists the frozen
> files and how a package adds routes, migrations, i18n keys and API types.

Status: reviewed plan (draft + fidelity, buildability and security critiques merged; every
critique point is dispositioned in Appendix A). Source of truth for *behavior* is
`ARCHITECTURE.md` (v4) and `README.md`; this document fixes *how* we build it: layout,
contracts, schema, API, and the parallel work breakdown. Section references like §4.2
point at `ARCHITECTURE.md`; "plan §N" points at this document.

Build order: **SCAFFOLD-0a ∥ SCAFFOLD-0b → Phase 0 work packages (parallel, one worktree
each, merge queue) → INT-0 → SCAFFOLD-1 → Phase 1 work packages (parallel) → INT-1.**
A **contract steward** (plan §9.3) is live during each parallel phase.

Items that need Corbin's call are collected in plan §11; the plan proceeds with the
recommended default for each.

---

## 0. Ground rules for parallel work

1. **File ownership is exclusive.** Every path in the repo is owned by exactly one of:
   the scaffold (`SCAFFOLD-0a/0b/1`), the contract steward (scaffold-owned files during a
   phase), one work package (WP), or an integration step (`INT-0`/`INT-1`). A WP edits
   only files it owns ("## Work packages" lists them; `scripts/ownership.json` is the
   machine-readable copy, SCAFFOLD-owned). Two WPs never share a Go package: where a
   later phase needs a new slice of an existing package, it gets its own package
   (e.g. `gate/signals/behavior` vs `gate/signals/automation`). Ownership of a stub
   passes to the named WP at hand-off.
2. **One git worktree and branch per WP.** Each WP works in
   `../pcc-wt/<wp-id>` on branch `wp/<wp-id>`, created from the scaffold commit
   (`git worktree add`). Its tree therefore contains only scaffold + its own changes, so
   a half-finished sibling WP can never break its build, tests or generated files.
   `scripts/check-ownership.sh <wp-id>` (run by `check-wp.sh`) fails if the branch diff
   touches a path the WP does not own.
3. **Merge queue.** Finished WPs are merged into `main` one at a time by the merge-queue
   step (the orchestrator or INT agent): rebase onto `main`, run the global
   `bash scripts/check.sh`, merge fast-forward, next. A red global check bounces the WP
   back with the failure; it never blocks other WPs' development.
4. **Contracts are frozen except through the steward.** Files marked *contract*
   (core interfaces, view types, `rank` signatures, route list, migrations, i18n
   namespace list, OpenAPI common schemas, `scripts/ownership.json`) are scaffold-owned.
   A WP that needs a change writes `docs/contract-requests/<wp-id>-<n>.md` (the one kind
   of doc a WP may add). The steward applies **additive-only** changes (new method on a
   new interface, new optional field, new event type, new nullable column) to `main`
   within the phase; WPs rebase to pick them up. Breaking changes wait for INT.
5. **Additive schema only.** A WP that discovers a missing column adds a migration in its
   reserved number range (plan §3.1). WP migrations add **nullable or defaulted** columns
   only, never drop/rename, never add a NOT NULL column without a default. All SQL names
   its columns (no `SELECT *`, no positional `INSERT`). Plan §3.4 lists which WP writes
   and which WPs read each table.
6. **No dependency edits by WPs.** WPs never run `go get`, `go mod tidy`, `npm install`
   or `npm ci` (which rewrite `go.mod`/`go.sum`/`package-lock.json`/`node_modules` for
   everyone). Every library of plan §2 is pinned by the scaffold and kept alive by
   `internal/deps/deps.go` (`//go:build tools`, blank imports). A new dependency is a
   contract request. Each worktree runs `npm ci` once at creation, before work starts.
7. **Codes, not prose (§15.4).** No Go code returns user-facing English. Errors are
   `{"error":"<code>"}`; codes match `^[a-z][a-z0-9_]*$`. Every user-visible string in the
   web app goes through i18next with ICU. Reasons shown to users (blame inbox,
   moderation log, report reasons) are codes from closed enums (plan §3.2 CHECKs).
8. **Uniform failure (§13).** A gate service returns a view, `gate.ErrNotFound`, a
   `*gate.Problem` about the caller's *own input*, `gate.ErrRateLimited`,
   `gate.ErrStepUp` or `gate.ErrDisabled`. Nothing else. Handlers never write status
   codes for not-found/forbidden themselves. **Check order inside every service:**
   charge (already done by `apix`) → resolve every ref (`ErrNotFound` on any failure) →
   only then evaluate Problems that depend on the target.
9. **Go module path:** `github.com/Nibroc6/purple-chicken-cult`. Go 1.27, `go.mod`
   `go 1.27` with an `ignore ./web/node_modules` directive (Go ≥1.25) so `./...`,
   `go vet` and golangci-lint never walk npm packages that ship stray `.go` files
   (e.g. `flatted`). Windows dev, Linux CI: scripts are POSIX `bash`, paths use `/`, and
   `.gitattributes` has `* text=auto eol=lf` (the system has `core.autocrlf=true`; CRLF
   would break `.sh` scripts, migration sha256 checks and byte-offset fixtures).
10. **Every WP ends green in its worktree:** `bash scripts/check-wp.sh <wp-id>`
    (ownership check, `go vet` + golangci-lint + `go test -p 4` over the whole module —
    cheap because the tree contains only scaffold + this WP — web lint/typecheck/unit,
    i18n check scoped to the WP's namespaces, and the WP's own Playwright spec). The
    global `scripts/check.sh` runs only in the merge queue and in CI.
11. **Database tests never silently skip.** `PCC_TEST_DATABASE_URL` defaults to
    `postgres://pcc:pcc@localhost:54329/pcc_test?sslmode=disable` whenever `PCC_ENV` is
    not `prod`; scripts and CI set `PCC_REQUIRE_DB=1`, which turns "DB unreachable" into a
    test failure instead of a skip.

---
## 1. Repository layout

```
.
├── go.mod / go.sum                       SCAFFOLD (WPs never edit; plan §0.6)
├── .gitattributes                        SCAFFOLD (* text=auto eol=lf)
├── Makefile                              SCAFFOLD (thin: calls scripts/*.sh; CI uses it)
├── docker-compose.yml                    SCAFFOLD (postgres:18 on 54329, dbs pcc + pcc_test, max_connections=400)
├── .golangci.yml                         SCAFFOLD (depguard + forbidigo + std linters)
├── .github/workflows/ci.yml              SCAFFOLD
├── .gitignore                            SCAFFOLD
├── api/openapi/                          OpenAPI fragments, one per module (merged in Go at test time, in Node for TS)
│   ├── _common.yaml                      SCAFFOLD contract: shared schemas (views, Error)
│   └── <module>.yaml                     owned by the module's WP
├── cmd/pcc/                              SCAFFOLD: main.go (subcommand dispatch), serve.go, cli.go
├── deploy/                               runbooks (deploy/runbooks/*.md, owner per WP), postgres init, P1 cloudflare worker
├── internal/
│   ├── archtest/                         SCAFFOLD: import-rule + signature-rule tests
│   ├── deps/deps.go                      SCAFFOLD: //go:build tools — blank-imports every pinned library
│   ├── store/                            DB infrastructure ONLY (pool, tx, migrations, jobs)
│   │   ├── store.go  tx.go  migrate.go   SCAFFOLD
│   │   ├── migrations/NNNN_*.sql         SCAFFOLD (+ WP reserved ranges)
│   │   ├── jobq/                         SCAFFOLD: Postgres job queue
│   │   └── storetest/                    SCAFFOLD: template-DB test helper
│   ├── gate/                             THE DOMAIN LAYER. Only this tree touches store.
│   │   ├── gate.go views.go errors.go    SCAFFOLD contract: views, errors, Ref
│   │   ├── internal/core/                SCAFFOLD contract: Viewer, IDs, cross-module ifaces, Deps, Bus
│   │   ├── internal/coreref/             SCAFFOLD: correct in-memory reference implementations of core ifaces (no `testing` import)
│   │   ├── internal/coretest/            SCAFFOLD: test helpers over coreref + World builder (DB + memory)
│   │   ├── wire/                         SCAFFOLD: constructs every service, late-binds core.Deps
│   │   ├── users/                        SCAFFOLD-0a complete (core.Users); P0-W11 owns afterwards
│   │   ├── auth/  graph/  visibility/  views/  friends/  invites/  posts/  postindex/
│   │   ├── engage/  rank/  feed/  tree/  previews/  notify/  metrics/  records/  account/
│   │   ├── limits/  moderation/  devseed/                         (Phase 0 WPs)
│   │   ├── cosign/  media/  themes/  signals/behavior/  signals/automation/   (Phase 1 WPs)
│   │   └── conformance/                  INT-0: horizon/noninterference suite
│   ├── api/                              HTTP handlers. Import gate/<domain>, never store.
│   │   ├── apix/                         SCAFFOLD: handler adapter, decode, error mapping, read+write classes, padding
│   │   ├── router/                       SCAFFOLD: router.go lists every module's Register
│   │   ├── deps.go                       SCAFFOLD: api.Deps (concrete service pointers)
│   │   ├── apitest/                      SCAFFOLD: per-service and full-app HTTP test harness
│   │   ├── testhooks/                    SCAFFOLD: /api/_test/* (PCC_ENV=test only)
│   │   └── authapi/ friendsapi/ invitesapi/ postsapi/ engageapi/ feedapi/ treeapi/ notifyapi/
│   │       accountapi/ modapi/ staticapi/  (P1:) mediaapi/ pushapi/ signalsapi/ cosignapi/ themeapi/
│   └── platform/                         Infra with NO DB and NO domain knowledge
│       ├── config/ clock/ ids/ refs/ sigs/ httpx/ logx/   SCAFFOLD (complete)
│       ├── secheaders/                   P0-W12
│       ├── safefetch/ urlx/ mdscan/      P0-W09
│       ├── i18n/                         P1-W05 (server catalogs, ICU, Go string check)
│       ├── blob/ scan/ imageproc/        P1-W04
│       ├── webpush/                      P1-W05
│       └── dbsc/                         P1-W07
├── scripts/                              SCAFFOLD: check.sh, check-wp.sh, check-ownership.sh, ownership.json, …
├── web/                                  Vite + React + TS PWA
│   ├── embed.go                          SCAFFOLD: package webdist, //go:embed all:dist (placeholder dist/.keep committed)
│   ├── package.json  vite.config.ts  tsconfig*.json  eslint.config.js  index.html   SCAFFOLD
│   ├── scripts/                          SCAFFOLD: openapi-merge.mjs, pseudo-locale.mjs, check-i18n.mjs,
│   │                                     check-css-logical.mjs; gen-y4m.mjs is P0-W04's
│   ├── e2e/                              Playwright: fixtures/ (SCAFFOLD; fixtures/camera.ts is P0-W04's)
│   │                                     + <wp-id>.spec.ts (WPs) + journeys/ (INT)
│   └── src/
│       ├── main.tsx app/ (App, routes.tsx, Shell, ErrorBoundary)        SCAFFOLD
│       ├── api/ client.ts errors.ts schema.gen.ts(generated)            SCAFFOLD
│       ├── api/sse.ts                                                   P0-W10
│       ├── vm/types.ts                   SCAFFOLD contract: FeedPageVM, PostVM, PersonVM, ChainVM, TreeVM
│       ├── i18n/ setup.ts resources.d.ts namespaces.ts                  SCAFFOLD contract
│       ├── i18n/locales/en/<ns>.json     owned by the namespace's WP (stubs from SCAFFOLD)
│       ├── i18n/locales/en-XA/           generated pseudo-locale (gitignored)
│       ├── integrity/types.ts            SCAFFOLD contract: props of the seven integrity components
│       ├── integrity/*                   P0-W10: TierHeader, Chain, InviterLabel, WhyButton,
│       │                                 AudienceBadge, EditedMark, ReportAction
│       ├── formats/registry.ts types.ts  SCAFFOLD contract
│       ├── formats/cards/ formats/compact/   P0-W07 (feed + post layouts) / P0-W08 (tree layouts)
│       ├── formats/explorer/             P1-W09
│       ├── markdown/                     P0-W09
│       ├── lib/                          SCAFFOLD: time.ts (Intl), a11y.ts, trustedTypes.ts
│       └── features/<feature>/           one dir per feature, owned by one WP
└── docs/IMPLEMENTATION.md                this plan; docs/contract-requests/ (WPs add files)
```

### 1.1 Layering and the §13 rule

```
cmd/pcc ──► internal/api/router ──► internal/api/<mod>api ──► internal/gate/<domain> ──► internal/store
   │                                     │                         │    ▲                     │
   │                                     ▼                         ▼    │                     ▼
   └──► internal/gate/wire        internal/api/apix         internal/gate/internal/core   pgx/v5
                                         │                         │
                                         ▼                         ▼
                                  internal/gate (views)     internal/platform/*
```

The ARCHITECTURE rule "only `gate` imports `store`; handlers import `gate`" is implemented
as **"only packages under `internal/gate/` (plus `internal/store/...` itself and
`cmd/pcc`, which only opens the pool and runs migrations) import `internal/store/...`"**.
All domain modules live under `internal/gate/`, so every database read is a gate read.

Three mechanisms make it structural, not just conventional:

1. **Go's `internal` rule.** Raw identifiers (`core.UserID`, `core.PostID`), `core.Viewer`,
   raw row types and cross-module interfaces live in `internal/gate/internal/core`. Only
   packages rooted at `internal/gate/` can import it, so a handler *cannot name* a user ID
   or a row. The authenticated viewer travels in `context.Context` (set by the auth
   middleware, read by gate services via `core.ViewerFrom(ctx)`); handlers never hold it.
2. **depguard** in `.golangci.yml` (CI lint step):
   ```yaml
   version: "2"                       # golangci-lint v2 config format
   linters:
     default: standard                # errcheck, govet, ineffassign, staticcheck, unused
     enable: [depguard, forbidigo, gosec, bodyclose, sqlclosecheck, rowserrcheck]
     settings:
       depguard:
         rules:
           api-no-store:
             files: ["**/internal/api/**/*.go"]
             deny:
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/store"
                 desc: "handlers talk to gate only (§13)"
               - pkg: "github.com/jackc/pgx/v5"
                 desc: "no SQL in handlers"
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/platform/refs"
                 desc: "only gate mints refs"
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
                 desc: "only gate signs"
           platform-leaf:
             files: ["**/internal/platform/**/*.go"]
             deny:
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/gate"
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/store"
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/api"
           gate-no-api:
             files: ["**/internal/gate/**/*.go"]
             deny:
               - pkg: "github.com/Nibroc6/purple-chicken-cult/internal/api"
       forbidigo:
         forbid:
           - pattern: '^http\.Error$'
             msg: "use apix error mapping (uniform responses)"
           - pattern: '^fmt\.Fprint.*$'
             msg: "no ad-hoc response bodies"
     exclusions:
       rules:
         - path: '^(cmd|internal/(platform|store|archtest))/'
           linters: [forbidigo]
   ```
3. **`internal/archtest` (Go test, runs in `go test ./...`)** — authoritative, because
   depguard prefix rules are easy to misconfigure. It loads all packages with
   `golang.org/x/tools/go/packages` (mode `NeedName|NeedImports|NeedDeps|NeedTypes`) and asserts:
   - **A1** importers of `internal/store/...` ⊆ `internal/gate/...` ∪ `internal/store/...`
     ∪ `{cmd/pcc}` ∪ `internal/archtest`. `cmd/pcc` may only reference
     `store.Open`, `store.Migrate`, `(*store.DB).Close` (checked via `types.Info.Uses`).
   - **A2** no package under `internal/api/...` imports `pgx`, `internal/store/...`,
     `internal/platform/refs`, `internal/platform/sigs`.
   - **A3** no package under `internal/platform/...` imports `internal/gate/...`,
     `internal/store/...`, `internal/api/...`.
   - **A4** only `internal/gate/auth` and `internal/gate/notify` (SSE stream type) under
     `internal/gate/...` import `net/http` (P1 adds `internal/gate/signals/automation`
     for the request-pattern middleware).
   - **A5 (no raw values reach handlers)** domain packages legitimately export
     `New(*core.Deps)` and methods that implement `core` interfaces (e.g.
     `(*graph.Graph).Search`), so the rule is checked at the *consumer*: for every package
     under `internal/api/...` (and `cmd/pcc`, minus the `store.Open/Migrate/Close` and
     `wire.Build` call sites allowed by A1), walk `types.Info.Types` and fail if any
     expression's type is, or structurally contains, a named type from
     `internal/gate/internal/core`, `internal/store/...`, `github.com/jackc/pgx/...` or
     `github.com/google/uuid`. "Structurally contains" walks pointer/slice/map/array
     element types, func signatures, **exported** struct fields and the signatures of
     **exported** methods only. It never descends into unexported fields, so
     `d.Feed.Page(...)` on a `*feed.Service` whose private field is `d *core.Deps` passes.
     Consequence for authors: every `*Service` method that a handler calls takes and
     returns only builtin/stdlib types, `gate.*` views, `gate.Ref`, `json.RawMessage`,
     `io.Reader/Writer`, or the service package's own exported DTOs (whose exported fields
     obey the same rule).
   - **A6** `internal/api/...` packages never call `encoding/json.NewEncoder(w)` on an
     `http.ResponseWriter` directly; they use `apix.JSON` (keeps TDM/no-store headers and
     timing padding in one place). Implemented by scanning `types.Info.Uses`.
   - **A7** error and reason codes: every string constant passed to
     `gate.NewProblem(...)`, and every value of the `core.ModReason` / `core.ReportReason`
     enums, matches `^[a-z][a-z0-9_]*$` (AST scan).
   - **A8 (domain isolation)** a package `internal/gate/<a>` never imports
     `internal/gate/<b>` for `a ≠ b`, except: anything may import `internal/gate/rank`
     (pure functions); `internal/gate/devseed` may import `internal/gate/graph` (`Synth`;
     same owner); `internal/gate/wire` and `internal/gate/conformance` import everything.
     Cross-module calls go through `core` interfaces in `core.Deps`. This is what lets
     12 agents work without waiting on each other's concrete code.
   - **A9 (no server prose, §15.4)** AST scan of non-test files under `internal/gate/...`
     and `internal/api/...`: a string literal that contains a letter-space-letter sequence
     (looks like prose) is allowed only as an argument to `slog.*`/`(*slog.Logger).*`,
     `fmt.Errorf`, `errors.New`, as SQL (literal starts with
     `SELECT|INSERT|UPDATE|DELETE|WITH|CREATE|ALTER` after trimming), or in a struct tag.
     P1-W05 extends this to `platform/i18n` payload builders.
   - **A10 (visibility capability, security #18)** only `internal/gate/visibility` and
     `internal/gate/internal/coreref` call `core.MintChecked` (the constructor of
     `core.Checked`, the only input `core.Views.Posts` accepts), and only `visibility`,
     `postindex` and `coreref` call `core.PostIndex.Children`/`Parents` directly. Everyone
     else uses `Visibility.VisibleChildren/VisibleParents`.
   - **A11 (logging policy, §2.3)** in `internal/gate/...` and `internal/api/...`, every
     `slog` attribute key comes from the allowlist in `platform/logx`
     (`req_id`, `route`, `status`, `dur_ms`, `code`, `job`, `uid_h`, `count`, …). A raw
     `core.UserID`, `uuid.UUID`, `netip.Addr`, `gate.Ref` or `*http.Request` value passed as
     a log argument fails the test. User IDs are logged only as `logx.UID(id)`
     (HMAC under a salt that rotates daily and is never persisted). Request logs carry no
     IP and no path parameters (`httpx` logs the **route pattern**, never the URL path).
   - **A12 (charging)** every route registered through `apix.Route` declares a read
     class or a write class (or `apix.Free` from a short reviewed allowlist: `/session`,
     `/events`, `/seen`, `/notifications/read`); a route with neither fails the test.

---

## 2. Libraries (verified 2026-10-01 with `go list -m -versions` / `npm view`)

### 2.1 Go (`go 1.27`)

| Purpose | Module | Version |
|---|---|---|
| Postgres driver + pool | `github.com/jackc/pgx/v5` (`pgxpool`) | v5.11.0 |
| Router | `github.com/go-chi/chi/v5` | v5.3.2 |
| WebAuthn / passkeys | `github.com/go-webauthn/webauthn` (`webauthn`, `protocol`) — uses `BeginDiscoverableLogin`, `FinishPasskeyLogin`, `WithChallenge` for co-signing | v0.18.2 |
| JCS (RFC 8785) | `github.com/gowebpki/jcs` (`jcs.Transform`) | v1.0.2 |
| AES-SIV (opaque refs) | `github.com/tink-crypto/tink-go/v2/daead/subtle` (`NewAESSIV`, `EncryptDeterministically(pt, ad)`) | v2.8.0 |
| Ed25519, ECDH P-256, HMAC, AES-GCM | stdlib `crypto/ed25519`, `crypto/ecdh`, `crypto/hmac`, `crypto/cipher` | — |
| UUIDv7 | `github.com/google/uuid` (`NewV7`) | v1.6.0 |
| Roaring bitmaps | `github.com/RoaringBitmap/roaring/v2` | v2.29.0 |
| Server-side Markdown scan | `github.com/yuin/goldmark` (+ `extension.Linkify`, `extension.Strikethrough`) | v1.8.6 |
| HTML parse (previews), IDNA | `golang.org/x/net/html`, `golang.org/x/net/idna`, `golang.org/x/net/publicsuffix` | v0.59.0 |
| BCP-47, Unicode norm, script checks | `golang.org/x/text/language`, `golang.org/x/text/unicode/norm`, `golang.org/x/text/secure/precis` (no UTS #39 confusables data: our "lookalike" flag is **mixed-script + non-ASCII-in-ASCII-lookalike-script detection only**, documented as such) | v0.42.0 |
| HKDF (key derivation from the master key) | stdlib `crypto/hkdf` | — |
| Arch test loader | `golang.org/x/tools/go/packages` | v0.50.0 |
| Property tests | `pgregory.net/rapid` | v1.3.0 |
| Diffs in tests | `github.com/google/go-cmp/cmp` | v0.7.0 |
| OpenAPI response validation in tests | `github.com/getkin/kin-openapi/openapi3filter` | v0.149.0 |
| **P1** Web Push (VAPID, RFC 8291) | `github.com/SherClockHolmes/webpush-go` | v1.4.0 |
| **P1** S3/R2 client | `github.com/minio/minio-go/v7` | v7.3.0 |
| **P1** Image decode/resize | `golang.org/x/image` (`webp` decode, `draw.CatmullRom`) + stdlib `image/jpeg`, `image/png`, `image/gif` | v0.46.0 |
| **P1** WebP / AVIF encode (pure Go, wazero) | `github.com/gen2brain/webp`, `github.com/gen2brain/avif` | v0.6.4 / v0.6.0 |
| **P1** ICU MessageFormat (server push text) | `github.com/kaptinlin/messageformat-go` (maintained; replaces the 2022-abandoned gotnospirit module) | v0.8.6 |
| Linter (tool, not a dependency) | `github.com/golangci/golangci-lint/v2/cmd/golangci-lint` — SCAFFOLD-0a installs it locally with `go install …@v2.14.0` into `~/go/bin` | v2.14.0 |

All of the above (P1 rows from SCAFFOLD-1) are blank-imported in `internal/deps/deps.go` so no
`go mod tidy` can drop them before a WP imports them.

**Not used, deliberately:** an ORM, sqlc (one fewer codegen step for 12 agents; each gate
package owns hand-written SQL in its own files), goose (our runner is 120 lines and
embeds), Valkey, River, any telemetry SaaS.

### 2.2 Web (Node 24, npm 11)

| Purpose | Package | Version (pin `^`) |
|---|---|---|
| UI | `react`, `react-dom` | 19.3.0 |
| Routing | `react-router` (data router, `createBrowserRouter`) | 8.4.0 |
| Server state | `@tanstack/react-query` | 5.104.0 |
| i18n | `i18next` 26.4.2, `react-i18next` 17.0.15, `i18next-icu` 2.4.4, `intl-messageformat` **11.2.15** (i18next-icu peer `<12`) | |
| ICU parsing for pseudo-locale + key check | `@formatjs/icu-messageformat-parser` | 3.5.20 |
| No-literal-string lint | `eslint-plugin-i18next` | 6.1.5 |
| Markdown | `markdown-it` 15.0.2 + `@types/markdown-it` 14.2.0 | |
| IDN display | `punycode` (npm package, not node builtin) | 2.3.1 |
| Typed API client | `openapi-typescript` 7.13.0 (dev) + `openapi-fetch` 0.17.0; merge with `yaml` 2.9.1 | |
| WebAuthn client | `@simplewebauthn/browser` | 14.0.0 |
| QR render / scan | `qrcode` 1.5.4 (+ `@types/qrcode` 1.5.6), `jsqr` 1.4.0 (in a module Worker; unmaintained since 2021 but pure JS — kept because the maintained `zxing-wasm` would need `'wasm-unsafe-eval'` in `script-src`) | |
| PWA / service worker | `vite-plugin-pwa` 1.3.0 (`injectManifest`; peer-requires `workbox-build` + `workbox-window` ^7.4.1, install both as dev deps), `workbox-precaching` 7.4.1 | |
| Build | `vite` 8.3.2, `@vitejs/plugin-react` 6.1.1 | |
| Types | `typescript` **~6.0.3** (typescript-eslint peer `<6.1`; do not take TS 7 yet), `@types/react` 19.3.0, `@types/react-dom` 19.3.0 (React 19 ships no types), `@types/node` 24.x (vite config, `.mjs` scripts) | |
| Lint | `eslint` 10.x, `@eslint/js`, `typescript-eslint` 8.71.0, `eslint-plugin-react-hooks` 7.1.1, `globals` (no `eslint-plugin-react`: its peer range stops at ESLint 9; `dangerouslySetInnerHTML` is banned with core `no-restricted-syntax` on `JSXAttribute[name.name='dangerouslySetInnerHTML']`) | |
| Unit tests | `vitest` 5.0.3, `jsdom` 30.1.1, `@testing-library/react` 16.3.3 | |
| E2E | `@playwright/test` 1.63.0 (Chromium: fake media + CDP virtual authenticator) | |

`package.json` carries `"overrides": { "openapi-typescript": { "typescript": "$typescript" } }`:
openapi-typescript 7.13 declares peer `typescript ^5.x` (plain install fails with
`ERESOLVE`), and it was smoke-tested to generate correctly under TS 6.0.3.

CSS: plain CSS Modules with logical properties and CSS custom properties (design
tokens) — no CSS-in-JS (keeps `style-src 'self'` strict and themes in P1 token-based).

---
## 3. Postgres schema

### 3.1 Migration runner and numbering

- Files: `internal/store/migrations/NNNN_name.sql`, embedded with
  `//go:embed migrations/*.sql` in `internal/store/migrate.go`.
- Runner (`store.Migrate(ctx, db)`): takes `pg_advisory_lock(7313)`; creates
  `schema_migrations(version int PRIMARY KEY, name text, applied_at timestamptz, sha256 bytea)`;
  applies every file whose numeric prefix is not recorded, in numeric order, **each in its
  own transaction**; refuses to start if an applied file's sha256 changed (edits to applied
  migrations are a bug). Up-only; no down migrations. `pcc migrate` runs it; `pcc serve`
  runs it when `PCC_AUTO_MIGRATE=1` (default in dev/test).
- Numbering:
  - `0001–0099` SCAFFOLD-0a (Phase 0 schema below).
  - `1000 + 10·n … +9` reserved for P0 WP `n` (e.g. P0-W05 → `1050–1059`), additive only.
  - `2001–2099` SCAFFOLD-1 (Phase 1 schema below).
  - `3000 + 10·n … +9` reserved for P1 WP `n`.
- Conventions: `bigint` user IDs (`GENERATED BY DEFAULT AS IDENTITY`; tests that insert
  explicit IDs take them from the high range `≥ 3_000_000_000`, and `coretest.World`
  finishes with a `setval` on the `users.id` sequence so later service-created users
  never collide), `uuid` (v7) post/media/other IDs, `timestamptz`
  everywhere, `smallint` enums documented inline, no `ON DELETE CASCADE` from `users`
  except session/credential/ephemeral tables (account deletion is an explicit procedure).
- User IDs must stay `< 2^32` (roaring32 bitmaps over likers/2-hop sets). A `CHECK
  (id < 4294967296)` enforces it.

### 3.2 Phase 0 migrations (SCAFFOLD-0a)

`0001_users_auth.sql`
```sql
CREATE TABLE users (
  id                   bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY CHECK (id > 0 AND id < 4294967296),
  ext_id               uuid        NOT NULL UNIQUE,
  handle               text        NOT NULL,                  -- as typed (display)
  handle_canon         text        NOT NULL UNIQUE CHECK (handle_canon ~ '^[a-z0-9_]{3,20}$'),
  display_name         text        NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 50),
  invited_by           bigint      REFERENCES users(id),      -- NULL only for genesis
  is_genesis           boolean     NOT NULL DEFAULT false,
  role                 smallint    NOT NULL DEFAULT 0,        -- 0 member, 1 moderator, 2 admin
  status               smallint    NOT NULL DEFAULT 0,        -- 0 active, 1 pending_enrollment, 2 suspended, 3 deleted
  created_at           timestamptz NOT NULL DEFAULT now(),
  probation_until      timestamptz,                           -- time part of probation; NULL = none (genesis)
  probation_cleared_at timestamptz,                           -- set once both parts satisfied (monotonic)
  name_in_chains       boolean     NOT NULL DEFAULT false,
  suggestible          boolean     NOT NULL DEFAULT true,
  named_in_likes       boolean     NOT NULL DEFAULT true,
  slippery_default     boolean     NOT NULL DEFAULT false,
  rank_preset          smallint    NOT NULL DEFAULT 1,        -- 0 latest, 1 balanced, 2 top (P1; P0 rejects 2)
  ui_prefs             jsonb       NOT NULL DEFAULT '{}'::jsonb, -- {format, density, a11y:{...}, theme (P1)}
  ui_locale            text        NOT NULL DEFAULT 'en',
  languages            text[]      NOT NULL DEFAULT '{en}',
  translate_to         text,
  autotranslate        boolean     NOT NULL DEFAULT false,
  webauthn_user_id     bytea       NOT NULL UNIQUE,           -- random 32 bytes (WebAuthn user.id)
  tos_version          smallint,                              -- terms accepted at join/enroll (§13 Compliance)
  tos_accepted_at      timestamptz,
  age_attested_at      timestamptz,                           -- "I am at least PCC_MIN_AGE"; no birth date stored
  deleted_at           timestamptz,
  CHECK (NOT is_genesis OR invited_by IS NULL),
  CHECK (is_genesis OR invited_by IS NOT NULL OR status = 1)
);
CREATE INDEX users_invited_by ON users (invited_by);

CREATE TABLE sessions (
  id_hash          bytea       PRIMARY KEY,                   -- sha256(cookie token)
  kind             smallint    NOT NULL,                      -- 0 guest, 1 user, 2 enrollment
  user_id          bigint      REFERENCES users(id) ON DELETE CASCADE,
  csrf_seed        bytea       NOT NULL,                      -- rotated with every token rotation
  ceremony         jsonb,                                     -- ONE pending WebAuthn ceremony: {purpose: login|register|add|stepup|enroll|cosign,
                                                              --  session_data, allow_credentials, exp (≤2 min)}; cleared on ANY finish attempt
  credential_id    bytea,                                     -- credential that authenticated this session (removal revokes the session)
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_seen_at     timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  auth_at          timestamptz,                               -- last UV passkey verification (step-up freshness: 5 min)
  stepup_required  boolean     NOT NULL DEFAULT false,        -- set by P1 signals
  CHECK (kind <> 1 OR user_id IS NOT NULL)
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE webauthn_credentials (
  id               bytea       PRIMARY KEY,                   -- credential ID
  user_id          bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  public_key       bytea       NOT NULL,
  alg              integer     NOT NULL,                      -- COSE alg (-7 ES256, -257 RS256, -8 EdDSA)
  sign_count       bigint      NOT NULL DEFAULT 0,            -- 0 allowed forever (synced passkeys); a decrease from nonzero = clone warning
  aaguid           bytea,
  transports       text[]      NOT NULL DEFAULT '{}',
  backup_eligible  boolean     NOT NULL DEFAULT false,        -- must never change after registration (assertion refused otherwise)
  backup_state     boolean     NOT NULL DEFAULT false,
  name             text        NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_used_at     timestamptz
);
CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);

CREATE TABLE enrollment_tokens (                               -- genesis + manual recovery (§10.3, §10.8)
  token_hash       bytea       PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id),
  purpose          smallint    NOT NULL,                      -- 0 genesis, 1 recovery
  created_at       timestamptz NOT NULL DEFAULT now(),
  usable_from      timestamptz NOT NULL DEFAULT now(),        -- recovery: release time after the 24 h hold
  expires_at       timestamptz NOT NULL,
  bound_session    bytea,                                     -- set by EnrollBegin; EnrollFinish must come from it
  cancelled_at     timestamptz,                               -- recovery cancelled from a live session of the account
  used_at          timestamptz                                -- set atomically (UPDATE … WHERE used_at IS NULL RETURNING)
);
```

`0002_records_graph.sql`
```sql
CREATE TABLE signing_keys (
  key_id           text        PRIMARY KEY,                   -- "srv-<yyyymmdd>-<n>" or "usr-<uuid>"
  owner_id         bigint      REFERENCES users(id),          -- NULL = server key
  alg              text        NOT NULL DEFAULT 'ed25519',
  public_key       bytea       NOT NULL,
  private_key_enc  bytea,                                     -- P1 user keys only (AES-256-GCM under KEK); server key lives in a file
  valid_from       timestamptz NOT NULL,
  valid_until      timestamptz
);

CREATE TABLE signed_records (
  hash             bytea       PRIMARY KEY,                   -- sha256(body); body already contains "kind" and "schema_version"
  kind             smallint    NOT NULL,                      -- mirror of body.kind (1 Invite, 2 Friendship, 3 Revocation, 4 Post, 5 Tombstone,
                                                              -- 6 Block, 7 Report, 8 PasskeyChange, 9 ModAction); verified equal on read/export
  author_id        bigint,                                    -- NULL = server-authored (Phase 0)
  key_id           text        NOT NULL REFERENCES signing_keys(key_id),
  body             bytea       NOT NULL,                      -- exact JCS bytes; sig covers "pcc-rec-v1\0" ‖ key_id ‖ 0x00 ‖ body (plan §4.2 sigs)
  sig              bytea       NOT NULL,
  cosigs           jsonb,                                     -- P1: [{key_id, sig}] extra user signatures
  assertions       jsonb,                                     -- P1: [{credential_id, authenticator_data, client_data_json, signature}]
  schema_version   smallint    NOT NULL,                      -- mirror of body.schema_version
  created_at       timestamptz NOT NULL DEFAULT now(),
  purge_after      timestamptz                                -- §2.3: Friendship/Revocation of a revoked edge = revocation + 30 d
);
CREATE INDEX signed_records_purge ON signed_records (purge_after) WHERE purge_after IS NOT NULL;
CREATE TABLE record_parties (
  user_id          bigint      NOT NULL,
  record_hash      bytea       NOT NULL REFERENCES signed_records(hash) ON DELETE CASCADE,
  PRIMARY KEY (user_id, record_hash)
);

CREATE TABLE edges (                                           -- both directions stored
  user_id          bigint      NOT NULL REFERENCES users(id),
  friend_id        bigint      NOT NULL REFERENCES users(id),
  level            smallint    NOT NULL CHECK (level IN (1, 2)),  -- 1 friend, 2 friend+
  since            timestamptz NOT NULL,
  record_hash      bytea       NOT NULL,
  PRIMARY KEY (user_id, friend_id),
  CHECK (user_id <> friend_id)
);
CREATE INDEX edges_friend ON edges (friend_id);

CREATE TABLE hide_via (                                        -- created in P0 (empty) so Graph.Load never depends on P1
  user_id          bigint      NOT NULL REFERENCES users(id), -- reader never routes THROUGH via_id (§4, §10.4); used from P1
  via_id           bigint      NOT NULL REFERENCES users(id),
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, via_id)
);

CREATE TABLE edge_events (                                     -- revoke rows purged after 30 days (§2.3)
  id               uuid        PRIMARY KEY,
  a                bigint      NOT NULL,
  b                bigint      NOT NULL,
  kind             smallint    NOT NULL,                      -- 0 create, 1 upgrade, 2 revoke
  at               timestamptz NOT NULL,
  record_hash      bytea
);
CREATE INDEX edge_events_at ON edge_events (at);

CREATE TABLE friend_requests (
  from_id          bigint      NOT NULL REFERENCES users(id),
  to_id            bigint      NOT NULL REFERENCES users(id),
  state            smallint    NOT NULL DEFAULT 0,            -- 0 pending, 1 declined (looks pending to sender), 2 accepted, 3 withdrawn
                                                              -- a request TO someone who blocked the sender is stored as 1 at creation
  created_at       timestamptz,                               -- nulled 30 d after decline (row kept so the sender still reads "outgoing")
  decided_at       timestamptz,                               -- nulled with created_at
  draft            bytea,                                     -- P1: JCS Friendship draft both parties sign
  draft_assertion  jsonb,                                     -- P1: requester's passkey assertion
  PRIMARY KEY (from_id, to_id),
  CHECK (from_id <> to_id)
);
CREATE INDEX friend_requests_to ON friend_requests (to_id) WHERE state = 0;

CREATE TABLE blocks (
  blocker          bigint      NOT NULL REFERENCES users(id),
  blocked          bigint      NOT NULL REFERENCES users(id),
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (blocker, blocked)
);
CREATE INDEX blocks_blocked ON blocks (blocked);

CREATE TABLE mutes (
  user_id          bigint      NOT NULL REFERENCES users(id),
  author_id        bigint      NOT NULL REFERENCES users(id),
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, author_id)
);

CREATE TABLE invites (
  id               uuid        PRIMARY KEY,
  inviter_id       bigint      NOT NULL REFERENCES users(id),
  invitee_id       bigint      NOT NULL UNIQUE REFERENCES users(id),
  created_at       timestamptz NOT NULL,
  handshake        jsonb       NOT NULL,                      -- {mode, channels_passed, timings_bucketed}
  invite_record    bytea       NOT NULL,
  friendship_record bytea      NOT NULL
);
CREATE INDEX invites_inviter ON invites (inviter_id);

CREATE TABLE invite_budgets (
  user_id          bigint      PRIMARY KEY REFERENCES users(id),
  tokens           double precision NOT NULL DEFAULT 0,
  cap              smallint    NOT NULL DEFAULT 3,            -- genesis 5
  refreshed_at     timestamptz NOT NULL DEFAULT now(),
  paused_until     timestamptz,                               -- quarantine (§8)
  revoked          boolean     NOT NULL DEFAULT false,
  offenses         smallint    NOT NULL DEFAULT 0
);

CREATE TABLE handshake_sessions (
  id               uuid        PRIMARY KEY,
  kind             smallint    NOT NULL,                      -- 0 invite, 1 friend_plus upgrade (P1), 2 recovery (P1)
  initiator_id     bigint      NOT NULL REFERENCES users(id),
  peer_user_id     bigint      REFERENCES users(id),          -- upgrade target / account being recovered
  secret           bytea       NOT NULL,                      -- frame MAC key (never leaves server)
  state            smallint    NOT NULL DEFAULT 0,            -- 0 open, 1 claimed, 2 confirmed (SAS matched), 3 done, 4 cancelled, 5 expired
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  claimed_session  bytea       REFERENCES sessions(id_hash) ON DELETE SET NULL,
  claimed_at       timestamptz,
  confirmed_at     timestamptz,                               -- inviter tapped "codes match" (security #6)
  handle_tries     smallint    NOT NULL DEFAULT 0,            -- handle_taken answers; ≤10 per handshake
  pending          jsonb,                                     -- {handle, display_name, locale, languages, webauthn_user_id}
  completed_at     timestamptz
);
CREATE INDEX handshake_sessions_initiator ON handshake_sessions (initiator_id, state);
```

`0003_posts.sql`
```sql
CREATE TABLE posts (
  id               uuid        PRIMARY KEY,
  author_id        bigint      NOT NULL REFERENCES users(id),
  body             text,                                      -- NFC-normalized at write; NULL after delete (hard delete)
  body_salt        bytea,                                     -- 16 random bytes per version; NULL after delete (security #25)
  audience         smallint    CHECK (audience IN (1, 2, 3, 10)), -- NULL everyone connected; 10 = friend+ only (P1)
  audience_forced  boolean     NOT NULL DEFAULT false,        -- set because author was on probation; permanent (D6)
  ref_policy       smallint    NOT NULL DEFAULT 0,            -- 0 anyone, 1 two_hops, 2 friends, 3 nobody (slippery)
  lang             text        NOT NULL DEFAULT 'und',
  no_preview       boolean     NOT NULL DEFAULT false,
  version          int         NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL,
  edited_at        timestamptz,
  deleted_at       timestamptz,
  record_hash      bytea       NOT NULL,                      -- latest version's record
  CHECK (char_length(body) <= 20000)
);
CREATE INDEX posts_author_time ON posts (author_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_time ON posts (created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE post_versions (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  version          int         NOT NULL,
  record_hash      bytea       NOT NULL,                      -- the version's record signs body_commit = sha256(salt ‖ utf8(body))
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (post_id, version)
);

CREATE TABLE post_parents (
  child_id         uuid        NOT NULL REFERENCES posts(id),
  parent_id        uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL CHECK (ord BETWEEN 0 AND 2),
  kind             smallint    NOT NULL CHECK (kind IN (0, 1)),   -- 0 ref, 1 cont
  child_author     bigint      NOT NULL,
  parent_author    bigint      NOT NULL,
  detached         boolean     NOT NULL DEFAULT false,
  shadow           boolean     NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (child_id, parent_id)
);
CREATE INDEX post_parents_parent ON post_parents (parent_id, created_at DESC) INCLUDE (child_id, child_author, kind, detached, shadow);
CREATE UNIQUE INDEX post_parents_one_cont ON post_parents (parent_id) WHERE kind = 1;
CREATE INDEX post_parents_refcap ON post_parents (child_author, parent_id, created_at);

CREATE TABLE mentions (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL,                      -- index of the @handle occurrence in body
  target_id        bigint      NOT NULL REFERENCES users(id),
  PRIMARY KEY (post_id, ord)
);
CREATE INDEX mentions_target ON mentions (target_id);

CREATE TABLE link_previews (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL,
  url_canon        text        NOT NULL,                      -- tracking params stripped
  site_host        text        NOT NULL,                      -- ASCII (punycode) host — the trusted field
  title            text,
  description      text        CHECK (char_length(description) <= 200),
  status           smallint    NOT NULL DEFAULT 0,            -- 0 pending, 1 ok, 2 failed, 3 skipped
  fetched_at       timestamptz,
  ready_at         timestamptz NOT NULL,                      -- created_at + k·PreviewDelay, k ∈ {1,2}; never shown before.
                                                              -- A fetch not finished by bucket 1 moves to bucket 2 once;
                                                              -- not finished by bucket 2 ⇒ status 2 (plan §4.7 previews)
  PRIMARY KEY (post_id, ord)
);

CREATE TABLE link_fetch_cache (                                -- internal, TTL ~1h (§10.1.6)
  url_canon        text        PRIMARY KEY,
  status           smallint    NOT NULL,
  title            text,
  description      text,
  site_host        text        NOT NULL,
  fetched_at       timestamptz NOT NULL,
  expires_at       timestamptz NOT NULL
);
```

`0004_engagement.sql`
```sql
CREATE TABLE engagements (
  user_id          bigint      NOT NULL REFERENCES users(id),
  post_id          uuid        NOT NULL REFERENCES posts(id),
  kind             smallint    NOT NULL,                      -- 1 like, 2 save (P1)
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, post_id, kind)
);
CREATE INDEX engagements_post ON engagements (post_id, kind);

CREATE TABLE seen (                                            -- 14-day retention, nightly purge
  user_id          bigint      NOT NULL,
  post_id          uuid        NOT NULL,
  at               timestamptz NOT NULL,
  PRIMARY KEY (user_id, post_id)
);
CREATE INDEX seen_user_at ON seen (user_id, at DESC);
CREATE INDEX seen_at ON seen (at);

CREATE TABLE hides (
  user_id          bigint      NOT NULL,
  post_id          uuid        NOT NULL,
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, post_id)
);
CREATE TABLE show_less (
  user_id          bigint      NOT NULL,
  author_id        bigint      NOT NULL,
  n                int         NOT NULL DEFAULT 1,
  last_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, author_id)
);
```

`0005_notifications.sql`
```sql
CREATE TABLE notifications (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id),
  kind             text        NOT NULL,                      -- friend_request, request_accepted, post_referenced, mention,
                                                              -- invitee_action, friend_security, invite_redeemed, recovery_approval (P1)
  group_key        text,                                      -- batching key, e.g. 'post_referenced:<parent uuid>'
  subject_user     bigint,                                    -- raw; turned into a per-recipient ref at read time
  subject_post     uuid,
  data             jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- codes and numbers only
  count            int         NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  read_at          timestamptz,
  delivered_at     timestamptz                                -- SSE/push batch flush marker
);
CREATE INDEX notifications_user ON notifications (user_id, updated_at DESC);
CREATE UNIQUE INDEX notifications_batch ON notifications (user_id, group_key) WHERE read_at IS NULL AND group_key IS NOT NULL;

CREATE TABLE notification_mutes (
  user_id          bigint      NOT NULL,
  post_id          uuid        NOT NULL,
  PRIMARY KEY (user_id, post_id)
);
```

`0006_limits.sql`
```sql
CREATE TABLE rate_counters (                                   -- read budgets + write limits (§17)
  user_id          bigint      NOT NULL,
  bucket           text        NOT NULL,                      -- e.g. 'r:feed_page', 'w:post'
  window_start     timestamptz NOT NULL,                      -- truncated to hour or UTC day
  count            int         NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, bucket, window_start)
);
CREATE INDEX rate_counters_window ON rate_counters (window_start);
```

`0007_moderation_account.sql`
```sql
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

CREATE TABLE blame_inbox (                                     -- §10.4: entry for the inviter after confirmed action
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

CREATE TABLE account_stubs (                                   -- moderators only (§10.2)
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
  revoke_old       boolean     NOT NULL DEFAULT true,         -- on release: revoke all sessions; old passkeys removed if the user opts in at enroll
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
```

`0008_jobs.sql`
```sql
CREATE TABLE jobs (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind             text        NOT NULL,
  payload          jsonb       NOT NULL DEFAULT '{}'::jsonb,
  dedupe_key       text,                                      -- optional idempotency key
  run_at           timestamptz NOT NULL DEFAULT now(),
  attempts         int         NOT NULL DEFAULT 0,
  max_attempts     int         NOT NULL DEFAULT 5,
  locked_until     timestamptz,
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  done_at          timestamptz
);
CREATE INDEX jobs_ready ON jobs (run_at) WHERE done_at IS NULL;
CREATE UNIQUE INDEX jobs_dedupe ON jobs (kind, dedupe_key) WHERE done_at IS NULL AND dedupe_key IS NOT NULL;
```

`0009_metrics.sql` (§13 product metrics, §18 DoD; aggregates only — no user IDs, edges, chains or IPs)
```sql
CREATE TABLE metrics_daily (
  day              date        NOT NULL,
  name             text        NOT NULL CHECK (name ~ '^[a-z][a-z0-9_.]*$'),
  bucket           text        NOT NULL DEFAULT '',           -- closed label set per name (plan §4.7 metrics)
  value            bigint      NOT NULL DEFAULT 0,
  PRIMARY KEY (day, name, bucket)
);
```

Not stored (in memory, by decision): feed tier snapshots and tree row snapshots (LRU,
30 min TTL, keyed by `(viewer, random 128-bit id)` — a restart just means the client
starts a new feed session), the `recent` index, the graph, liker bitmaps, the
link-preview per-host limiter, anonymous per-IP-prefix rate counters, in-flight metric
counters (flushed to `metrics_daily` every minute).

### 3.3 Phase 1 migrations (SCAFFOLD-1)

`2001_cosign.sql` — per-user keys use `signing_keys` (owner_id set). Co-sign intents:
```sql
CREATE TABLE cosign_intents (
  id               uuid        PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id),
  purpose          text        NOT NULL,                      -- friend_request, friend_accept, unfriend, invite_inviter, invite_invitee,
                                                              -- passkey_add, passkey_remove, account_delete, recovery_approve, block
  record_kind      smallint    NOT NULL,
  draft            bytea       NOT NULL,                      -- exact JCS bytes of the record to sign; the body contains
                                                              -- "nonce" (= this intent's id, server-random) and "issued_at"
  challenge        bytea       NOT NULL,                      -- sha256("pcc-v1:" || kind || draft) — unique because of the nonce
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,                      -- 5 minutes
  used_at          timestamptz                                -- consumed in the SAME tx as the record write:
                                                              -- UPDATE … SET used_at=now() WHERE id=$1 AND used_at IS NULL RETURNING
);
CREATE INDEX cosign_intents_user ON cosign_intents (user_id, created_at DESC);
```

`2002_handshake_audio.sql`
```sql
ALTER TABLE handshake_sessions
  ADD COLUMN mode            smallint NOT NULL DEFAULT 0,   -- 0 qr (P0), 1 qr+audio, 2 dual_qr (accessibility)
  ADD COLUMN quiet           boolean  NOT NULL DEFAULT false,
  ADD COLUMN frame_key       bytea,                         -- given to inviter device to render 250ms frames
  ADD COLUMN inviter_epub    bytea,                         -- E_i (P-256, uncompressed)
  ADD COLUMN invitee_epub    bytea,                         -- E_v
  ADD COLUMN kmac            bytea,                         -- HKDF(S) reported by invitee device
  ADD COLUMN kmac_commit     bytea,                         -- sha256(HKDF(S)) reported by inviter device
  ADD COLUMN invitee_sig_pub bytea,                         -- separate non-extractable WebCrypto ECDSA P-256 key of the invitee
                                                            -- device; every later join call is signed over (hs, server challenge)
  ADD COLUMN play_at         timestamptz,                   -- SERVER time the play-start was issued
  ADD COLUMN report_at       timestamptz,                   -- SERVER time the inviter's decode report arrived
  ADD COLUMN report          jsonb,                         -- inviter decode report (bits + client timings, which can only TIGHTEN)
  ADD COLUMN attempts        smallint NOT NULL DEFAULT 0 CHECK (attempts <= 5),
  ADD COLUMN outcome         smallint NOT NULL DEFAULT 0;   -- 0 none, 1 retry, 2 accepted, 3 verified
```

`2003_media.sql`
```sql
CREATE TABLE media (
  id               uuid        PRIMARY KEY,
  owner_id         bigint      REFERENCES users(id),          -- NULL = system ingest (preview image)
  source_post      uuid,                                      -- preview images: the post whose link produced it
  status           smallint    NOT NULL,                      -- 0 received, 1 ready, 2 rejected, 3 held (CSAM match; evidence preserved)
  kind             smallint    NOT NULL DEFAULT 0,            -- 0 image (video is Phase 2)
  width            int,
  height           int,
  alt              text        CHECK (char_length(alt) <= 1000),
  scan_driver      text        NOT NULL,
  scanned_at       timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  deleted_at       timestamptz
);
CREATE TABLE media_variants (
  media_id         uuid        NOT NULL REFERENCES media(id),
  variant          smallint    NOT NULL,                      -- 0 thumb (320), 1 display (1080), 2 original (re-encoded, metadata stripped)
  blob_key         text        NOT NULL UNIQUE,               -- 128-bit random, base32; never a content hash
  mime             text        NOT NULL,
  width            int         NOT NULL,
  height           int         NOT NULL,
  bytes            int         NOT NULL,
  PRIMARY KEY (media_id, variant)
);
CREATE TABLE post_media (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL CHECK (ord BETWEEN 0 AND 3),
  media_id         uuid        NOT NULL UNIQUE REFERENCES media(id),
  PRIMARY KEY (post_id, ord)
);
CREATE TABLE csam_holds (                                      -- legal preservation; content access = break-glass only
  media_id         uuid        PRIMARY KEY REFERENCES media(id),
  evidence_key     text        NOT NULL,                      -- key in the separate evidence blob store (1-year retention)
  match_info       jsonb       NOT NULL,                      -- scanner verdict metadata only; this is what moderators see
  source_url       text,                                      -- preview images: canonical URL that served the image
  subject_user     bigint,                                    -- uploader, or author of the post that linked it
  held_at          timestamptz NOT NULL DEFAULT now(),
  retain_until     timestamptz NOT NULL,                      -- held_at + 1 year (REPORT Act 2024 preservation)
  reported_at      timestamptz,
  report_ref       text                                       -- filled by the csam_report moderator action
);
ALTER TABLE link_previews ADD COLUMN image_media_id uuid REFERENCES media(id);
```

`2004_push.sql`
```sql
CREATE TABLE push_subscriptions (
  id               uuid        PRIMARY KEY,
  user_id          bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_hash     bytea,                                     -- removed on logout of that session
  endpoint         text        NOT NULL UNIQUE,               -- re-subscribing an existing endpoint reassigns it to the current user
  p256dh           bytea       NOT NULL,
  auth             bytea       NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_ok_at       timestamptz,
  failures         int         NOT NULL DEFAULT 0,
  hide_content     boolean     NOT NULL DEFAULT false         -- generic "new activity" text on the lock screen
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);
```

`2005_themes.sql` (`hide_via` already exists since 0002)
```sql
CREATE TABLE user_themes (                                     -- §15.2 A: design tokens only
  user_id          bigint      PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  tokens           jsonb       NOT NULL,                      -- validated server-side (contrast, size floor, font allowlist)
  updated_at       timestamptz NOT NULL DEFAULT now()
);
```

`2006_signals_dbsc.sql`
```sql
CREATE TABLE automation_signals (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          bigint      NOT NULL,
  session_hash     bytea,
  kind             text        NOT NULL,                      -- webdriver, cdp, no_input, sustained_rate, no_seen_reads,
                                                              -- bfs_tree_walk, chain_speed, cadence, similarity, invite_cluster
  score            real        NOT NULL,
  data             jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- aggregates only, no content
  net_h            bytea,                                     -- HMAC(daily salt, IPv4 /24 or IPv6 /56); never a raw IP
  at               timestamptz NOT NULL DEFAULT now()          -- rows purged after 30 d
);
CREATE INDEX automation_signals_user ON automation_signals (user_id, at DESC);

CREATE TABLE stepups (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          bigint      NOT NULL,
  session_hash     bytea       NOT NULL,
  required_at      timestamptz NOT NULL,
  satisfied_at     timestamptz,
  reason           text        NOT NULL
);

ALTER TABLE sessions
  ADD COLUMN dbsc_bound      boolean NOT NULL DEFAULT false, -- once true, NEVER accepts an unbound request again
  ADD COLUMN dbsc_public_jwk jsonb,                          -- ES256 or RS256 (many Windows TPMs are RSA-only)
  ADD COLUMN dbsc_challenge  bytea,                          -- single-use; cleared when consumed
  ADD COLUMN dbsc_refreshed_at timestamptz,
  ADD COLUMN bound_token_hash bytea,                         -- short-lived (10 min) bound cookie __Host-pcc-b
  ADD COLUMN bound_expires_at timestamptz,
  ADD COLUMN short_lived     boolean NOT NULL DEFAULT false;
```

`2007_moderation_queue.sql`
```sql
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

CREATE TABLE quarantines (                                     -- §8 invite-tree quarantine (k = PCC_QUARANTINE_K, default 3)
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
ALTER TABLE invite_budgets ADD COLUMN diversity real NOT NULL DEFAULT 1.0; -- §8 diversity decay multiplier
```

### 3.4 Table ownership (buildability C5)

Exactly one WP **writes** each table (or each named column group). Other packages read
with explicit column lists, or go through the named `core` interface. A reader never
depends on a column added by another WP's own migration range until INT has merged it.

| Table | Writer (Phase 0 → Phase 1) | Direct SQL readers | Everyone else uses |
|---|---|---|---|
| `users` — insert | `gate/users` via `core.Users.Create` (scaffold, then W11); called by W01 (genesis) and W04 (join) | `gate/users` | `core.Users` |
| `users` — settings, status, deletion | W11 → P1-W10 | `gate/users` | `core.Users` |
| `users` — `probation_*` | W04 → P1-W02 (P1-W06 resets on quarantine via `core.Probation.Reset`) | `gate/users` | `core.Users` (`OnProbation`) |
| `users` — `role` | W12 → P1-W06 | `gate/users` | `core.Viewer.Role` |
| `sessions`, `webauthn_credentials`, `enrollment_tokens` | W01 → P1-W07 (W11/P1-W10 issue tokens via `core.Passkeys.IssueEnrollment`) | — | `core.Passkeys` |
| `signing_keys`, `signed_records`, `record_parties` | W11 → P1-W01 | `gate/records` | `core.Signer` |
| `edges`, `edge_events` | W03 → P1-W03 (W04 and W11 call `core.Edges`) | `gate/graph` (`Load`) | `core.Graph`, `core.Edges` |
| `friend_requests`, `blocks`, `mutes`, `hide_via` | W03 → P1-W03 | `gate/graph` (`blocks`, `hide_via` on `Load`) | `core.Graph.Exclusions`, `core.Mutes` (implemented by W03; used by feed and tree) |
| `invites`, `invite_budgets`, `handshake_sessions` | W04 → P1-W02 | `gate/users` (`invites` for inviter labels) | `core.Invites` |
| `posts`, `post_versions`, `post_parents`, `mentions` | W05 → P1-W12 | `gate/postindex` | `core.PostIndex` |
| `link_previews`, `link_fetch_cache` | W09 → P1-W11 | `gate/views` (`link_previews`, read-only) | — |
| `engagements`, `seen`, `hides`, `show_less` | W06 → P1-W08 | — | `core.Engagement`, `core.Prefer` |
| `notifications`, `notification_mutes` | W10 → P1-W05 | — | `core.Notifier` |
| `metrics_daily` | W10 (`gate/metrics`) | — | `core.Metrics` |
| `rate_counters` | W12 → P1-W07 | — | `core.Limiter` |
| `reports`, `mod_actions`, `blame_inbox` | W12 → P1-W06 | — | `core.ModQueue` (P1) |
| `account_stubs`, `recovery_requests`, `recovery_approvals` | W11 → P1-W10 | — | — |
| `jobs` | `store/jobq` (scaffold) | — | `jobq.Queue` |
| `cosign_intents` | P1-W01 | — | `core.Cosign` |
| `media`, `media_variants`, `csam_holds` | P1-W04 | `gate/views` (variants, read-only) | `core.Media` |
| `post_media` | P1-W12 | — | `core.Media.Views` |
| `push_subscriptions` | P1-W05 | — | — |
| `user_themes` | P1-W09 | — | — |
| `automation_signals` | P1-W07 (kinds webdriver, cdp, no_input, sustained_rate, no_seen_reads, bfs_tree_walk, chain_speed) and P1-W06 (kinds cadence, similarity, invite_cluster) | — | — |
| `stepups` | P1-W07 | — | `core.StepUp` |
| `mod_queue`, `quarantines`, `removed_account_marks` | P1-W06 | — | `core.ModQueue` |

Deletion fan-out never crosses tables directly: `AccountDeleted`, `PostDeleted` and
`SessionEnded` Bus events let each owner clean up its own rows (e.g. W09 drops
`link_previews` on `PostDeleted`, W06 drops engagements on `AccountDeleted`).

---
## 4. Go packages

Notation: **Imports** lists the internal packages a package may import (stdlib and the
libraries in plan §2 are always allowed unless stated). **Owner** is the WP ("Work packages").
Module prefix `M = github.com/Nibroc6/purple-chicken-cult`.

### 4.1 `cmd/pcc` (SCAFFOLD)

One binary, subcommands (stdlib `flag`, one `FlagSet` per subcommand):

| Command | Calls | Purpose |
|---|---|---|
| `pcc serve` | `wire.Build` → `router.New` → `http.Server` | API + SSE + static SPA + job workers |
| `pcc migrate` | `store.Migrate` | apply migrations |
| `pcc genesis --handle H [--name N]` | `app.Auth.SeedGenesis` | create genesis user, print one-time enrollment URL `<Origin>/enroll#<token>` (§10.8; 24 h; bootstrap only — refused once 50 non-genesis users exist) |
| `pcc mod grant --handle H --by H2` | `app.Moderation.GrantModerator` | role=1; requires an existing admin `--by` (first admin: `--bootstrap` flag only when no admin exists) |
| `pcc keys rotate` | `app.Records.RotateServerKey` | new server key, close old validity window; prints the new key's fingerprint for out-of-band publication |
| `pcc keys fingerprints` | `app.Records.Keys` | prints `key_id  sha256(pubkey)` lines to publish out-of-band (landing page, repo, social) |
| `pcc export-verify FILE.zip [--fingerprints F]` | `records.VerifyExport` | verifies an export offline (§10.2), optionally pinning keys to published fingerprints |
| `pcc audit-edges` | `app.Records.AuditEdges` | §4.5: every `edges` row's `record_hash` resolves to a valid, unrevoked Friendship record (also a weekly job) |
| `pcc metrics [--days 14]` | `app.Metrics.Report` | prints the aggregate product metrics (§13, §18 DoD); no identifiers exist to print |
| `pcc dev seed --users 10000 --model ba` | `app.DevSeed.Synthetic` | refuses unless `PCC_ENV ∈ {dev, test}` |

Manual recovery is **not** a CLI command (security #14): two moderators approve it in the
moderator UI, each with a fresh passkey step-up, and a 24 h hold follows (plan §4.7
account).

`serve.go` is the only place that builds `api.Deps` from `*wire.App`. All files in
`cmd/pcc` are SCAFFOLD-owned; WPs implement the called methods.

### 4.2 `internal/platform/*` (leaf infrastructure; imports nothing internal except other `platform/*`)

**`platform/config`** (SCAFFOLD, complete)
```go
type Env string // "dev" | "test" | "prod" — NO default (security #1)
type Config struct {
    Env            Env           // PCC_ENV, REQUIRED: unset or unknown ⇒ Load() fails, the binary refuses to start
    HTTPAddr       string        // PCC_HTTP_ADDR, default ":8080" (dev: default "127.0.0.1:8080")
    Origin         string        // PCC_ORIGIN, exact first-party origin, e.g. "https://pcc.example"; dev = Vite "http://localhost:5173"
    RPID           string        // PCC_RP_ID (WebAuthn RP ID), default host of Origin; must be a registrable suffix of it
    TrustedProxies []netip.Prefix // PCC_TRUSTED_PROXIES (CIDRs, e.g. Cloudflare ranges); CF-Connecting-IP / X-Forwarded-For
                                  // are honored ONLY from these peers; otherwise RemoteAddr
    BehindCDN      bool          // PCC_BEHIND_CDN=1 ⇒ prod Validate() requires TrustedProxies
    DatabaseURL    string        // PCC_DATABASE_URL
    AutoMigrate    bool          // PCC_AUTO_MIGRATE
    MasterKey      []byte        // PCC_MASTER_KEY (hex, 32 bytes). Every symmetric app key is HKDF-SHA256(MasterKey, label):
                                 //   "pcc/ref/v1" (64 B AES-SIV), "pcc/csrf/v1", "pcc/cursor/v1", "pcc/media-url/v1",
                                 //   "pcc/pseudonym/v1", "pcc/log-salt/v1/<day>", "pcc/metrics-salt/v1/<day>" — distinct by construction
    ServerKeyFile  string        // PCC_SERVER_KEY_FILE (ed25519 seed, 32 bytes; dev/test auto-create ./var/keys/server.key)
    KEK            []byte        // P1: PCC_KEK (hex, 32 bytes) wraps per-user keys; deliberately NOT derived (rotates separately)
    Blob           BlobConfig    // P1: PCC_BLOB_DRIVER=fs|s3, PCC_BLOB_FS_DIR, PCC_S3_ENDPOINT/BUCKET/ACCESS_KEY/SECRET_KEY, PCC_MEDIA_BASE_URL
    AllowFSBlob    bool          // P1: PCC_ALLOW_FS_BLOB=1 — prod fs blobs; honored only with a real scanner, logged at WARN every start
    EvidenceBlob   BlobConfig    // P1: PCC_EVIDENCE_* — separate store for CSAM preservation (1-year retention policy)
    Scanner        ScannerConfig // P1: PCC_SCANNER=photodna|dev|none (any other value ⇒ Load() fails), PCC_PHOTODNA_KEY, PCC_PHOTODNA_URL
    Push           PushConfig    // P1: PCC_VAPID_PUBLIC, PCC_VAPID_PRIVATE, PCC_VAPID_SUBJECT
    DBSC           bool          // P1: PCC_DBSC=1
    MinAge         int           // PCC_MIN_AGE, default 16 (age attestation at join/enroll)
    TOSVersion     int           // PCC_TOS_VERSION, default 1
    QuarantineK    int           // P1: PCC_QUARANTINE_K, default 3 (§8)
    PreviewDelay   time.Duration // default 30s (fixed delay bucket before a preview shows)
    FeedWindow     time.Duration // default 168h
    TestHooks      bool          // PCC_TEST_HOOKS=1, honored ONLY when Env == "test"
    LogLevel       slog.Level
}
func Load() (*Config, error)
// Dev-only behavior is gated by an ALLOWLIST (Env ∈ {dev,test}), never by `Env != prod`:
// random MasterKey, auto-created server key file, dev scanner, fs blobs, devseed, `pcc dev seed`.
func (c *Config) DevLike() bool       // Env == dev || Env == test — the only predicate dev paths may use
func (c *Config) Validate() error
// Validate():
//  all envs: MasterKey 32 B and not all-zero; KEK (if set) 32 B, not all-zero, ≠ MasterKey; RPID suffix of Origin host;
//            Env=dev ⇒ HTTPAddr is loopback unless Origin is http://localhost*; TestHooks ⇒ Env == test.
//  prod:     https Origin; MasterKey + server key file present (never generated); TestHooks off;
//            BehindCDN ⇒ TrustedProxies non-empty; P1: KEK present.
// Fail-closed feature switches (computed, never configured directly):
func (c *Config) MediaEnabled() bool
//   prod: Scanner == photodna (configured, key present) AND Blob configured (s3, or fs with AllowFSBlob)
//         AND EvidenceBlob configured AND media-URL key derivable. Anything else ⇒ false.
//   dev/test: true with the dev scanner + fs drivers.
//   PCC_SCANNER=dev or none in prod ⇒ false (logged at ERROR on start); the server still starts.
func (c *Config) PushEnabled() bool    // VAPID keys present
func (c *Config) DBSCEnabled() bool    // c.DBSC (any env); when false, sessions use the short-lived re-verify fallback (§17.2(2))
// Media/push being disabled is NOT a startup error: those features report feature_disabled (fail closed,
// app still runs). Unknown enum values, a missing PCC_ENV, or weak/duplicate keys ARE startup errors.
```
`config_test.go` holds the fail-closed matrix (buildability F, fidelity #5): every combination
of `Env × Scanner{photodna, dev, none} × Blob{none, fs, fs+allow, s3} × Evidence{none, set}`
→ expected `MediaEnabled()`, plus `PCC_ENV` unset/garbage ⇒ error, `PCC_SCANNER=cloudflare` ⇒
error (Cloudflare's CSAM tool scans at the CDN *after* storage, so it can never be the §6.1
pre-storage scanner), `TestHooks` with `Env=dev` ⇒ error.

**`platform/clock`** (SCAFFOLD): `type Clock interface{ Now() time.Time }`, `System`, `*Fake` (`Advance`, `Set`).

**`platform/ids`** (SCAFFOLD): `type Gen interface{ New() uuid.UUID }`; `V7{}`; `Seq`
(deterministic counter, unit tests only); `Named(label string) uuid.UUID` (UUIDv8 from
`sha256("pcc-fixture:" ‖ label)`, used by `coretest.World` so that a fixture post's ID depends
only on its own label, never on how many other posts exist — buildability D1, fidelity #9).
Only gate code uses it; handlers never see UUIDs.

**`platform/logx`** (SCAFFOLD): the attribute-key allowlist for A11, `UID(id uint32) slog.Attr`
(`uid_h` = HMAC(daily log salt, id)[:8], salt derived from MasterKey and the UTC day, never
stored), `Route(r)` (chi route pattern, never the raw path). Request logs carry no IPs.

**`platform/refs`** (SCAFFOLD, complete) — opaque per-viewer references (§13)
```go
type Kind byte
const (KindUser Kind = 1; KindPost Kind = 2; KindMedia Kind = 3; KindNotif Kind = 4; KindReport Kind = 5; KindCred Kind = 6; KindHandshake Kind = 7; KindModItem Kind = 8)
type Codec struct{ /* tink AESSIV, keyed per version */ }
func New(keys map[byte][]byte, current byte) (*Codec, error) // version byte → 64-byte key; allows rotation
// ref = base64url_nopad( version(1B) ‖ AES-SIV(k_version, pt=target, ad="pcc-ref-v1"‖version‖viewer(8B BE)‖kind) )
func (c *Codec) Seal(viewer uint64, k Kind, target []byte) string // panics on viewer == 0 (never mint guest-shared refs)
func (c *Codec) Open(viewer uint64, k Kind, ref string) ([]byte, bool)
// Open: base64.RawURLEncoding.Strict(); length bounds checked before decrypt; unknown version, other viewer,
// other kind, garbage ⇒ false. Refs whose version is not current are still accepted until the old key is retired.
```
Viewer 0 is never used: guests get no refs (handshake/claim flows use session-bound random
tokens). Moderator-scoped refs use `viewer = 1<<63 | modID`.

**`platform/sigs`** (SCAFFOLD, complete) — JCS + Ed25519 (§3)
```go
// Record value grammar (security #4), enforced by Canonical via reflection — violating values return an error:
//   timestamps: strings "YYYY-MM-DDTHH:MM:SS.sssZ" (UTC, exactly millisecond precision) — type sigs.Time
//   IDs: lowercase UUID strings; user references use ext_id UUIDs, never bigint IDs or handles
//   integers: |n| ≤ 2^53−1; floats forbidden
//   bytes: base64url without padding — type sigs.Bytes
//   optional fields: omitted, never null (omitempty on pointer/slice fields only)
//   every body has "kind" (string, e.g. "friendship") and "schema_version" (int) INSIDE the canonical bytes
func Canonical(v any) ([]byte, error)                 // grammar check → json.Marshal → jcs.Transform → assert jcs(parse(b)) == b
func RecordHash(canon []byte) [32]byte                // sha256(canon)
func SignedMessage(keyID string, canon []byte) []byte // "pcc-rec-v1\x00" ‖ keyID ‖ 0x00 ‖ canon  (domain separation; key_id bound)
func Challenge(kind string, canon []byte) [32]byte    // sha256("pcc-v1:"‖kind‖canon) — passkey challenge (§3); canon contains a nonce
type Key struct{ ID string; Priv ed25519.PrivateKey; Pub ed25519.PublicKey; From, Until time.Time }
func LoadServerKey(path string) (*Key, error)
func CreateServerKey(path string) (*Key, error)       // callable only when cfg.DevLike() (wire enforces)
func (k *Key) Sign(canon []byte) []byte               // Ed25519 over SignedMessage(k.ID, canon)
func Verify(pub ed25519.PublicKey, keyID string, canon, sig []byte) bool
// Envelope used in exports (records.jsonl lines):
type Envelope struct {
    Kind string `json:"kind"`; SchemaVersion int `json:"schema_version"`; KeyID string `json:"key_id"`
    Body json.RawMessage `json:"body"`; Sig string `json:"sig"`
    Cosigs []Cosig `json:"cosigs,omitempty"`; Assertions []json.RawMessage `json:"assertions,omitempty"`
}
// VerifyEnvelope rejects an envelope whose kind/schema_version differ from the body's, and never
// re-canonicalizes: it verifies the stored bytes as-is (and checks they are already canonical).
```
**Decision (D3, revised by security #25):** each `Post` version record signs
`body_commit = sha256(salt ‖ utf8(body))` with a fresh 16-byte random salt per version, not
the body and not a bare hash. The §10.2 hard delete removes the body **and** the salt, so the
surviving record is no confirmation oracle for guesses of short deleted text. The body is
NFC-normalized by the server before it is stored, committed to and exported; the exported
`posts.jsonl` carries `{id, version, body, salt}` so a holder can check the commit.

**`platform/httpx`** (SCAFFOLD): `RequestID`, `Recover`, `RealIP(cfg.TrustedProxies)`
(`CF-Connecting-IP`, then the right-most untrusted `X-Forwarded-For` hop, honored **only**
when the TCP peer is in `TrustedProxies`), `MaxBytes(n)`,
`SetSessionCookie(w, token, maxAge, secure bool)` — always `HttpOnly; SameSite=Strict;
Path=/` and `Secure` in prod (unit-tested header string) — `ClearSessionCookie`, cookie name
`__Host-pcc` (prod) / `pcc_dev` (dev, http://localhost). `AccessLog` logs route pattern,
status and duration only (no path, no query, no IP). No JSON helpers here (those are in
`api/apix`, which may import gate).

**`platform/secheaders`** (P0-W12)
```go
type Options struct{ Origin string; MediaOrigin string; Prod bool }
func Middleware(o Options) func(http.Handler) http.Handler
// Sets on EVERY response: Content-Security-Policy (see plan §6.6), tdm-reservation: 1,
// tdm-policy: <Origin>/terms#tdm, X-Robots-Tag: noindex, nofollow, noai, noimageai,
// X-Frame-Options: DENY, Reporting-Endpoints: csp="/api/csp-report",
// Referrer-Policy: no-referrer, X-Content-Type-Options: nosniff, Cross-Origin-Opener-Policy: same-origin,
// Cross-Origin-Resource-Policy: same-origin, Permissions-Policy: camera=(self), microphone=(self), geolocation=(),
// Strict-Transport-Security (prod), Cache-Control: no-store on /api/*.
func RobotsTxt() []byte; func AITxt() []byte; func TDMRepJSON(origin string) []byte
```

**`platform/safefetch`** (P0-W09) — SSRF-guarded fetcher (§10.1.6)
```go
type Response struct{ FinalURL *url.URL; ContentType string; Body []byte }
type Client struct{ /* unexported */ }
func New() *Client
// http/https, ports 80/443 only; URLs with userinfo rejected; Transport.Proxy = nil (never HTTP(S)_PROXY);
// no cookie jar; fixed UA "pcc-preview/1"; ≤3 redirects, each re-checked from scratch.
// Address policy, checked on netip.Addr.Unmap() of every resolved address AND again in net.Dialer.Control
// (covers every dial, incl. Happy Eyeballs fallbacks), dialing only the checked IP (rebinding-proof):
//   deny 0/8, 10/8, 100.64/10, 127/8, 169.254/16, 172.16/12, 192.0.0/24, 192.168/16, 198.18/15, 240/4,
//   255.255.255.255, 224/4; ::/128, ::1, ::/96, ::ffff:0:0/96, 64:ff9b::/96, 2001::/32, 2002::/16,
//   fc00::/7, fe80::/10, fec0::/10, ff00::/8; plus cloud metadata hosts by name.
// Limits: 3 s total deadline; MaxResponseHeaderBytes 16 KiB; body cap 512 KiB measured AFTER decompression
// (io.LimitReader on the decoded stream); Content-Type text/html only.
// Deploy docs (deploy/runbooks/egress.md, P0-W09) add an egress firewall for the service uid as defense in depth.
func (c *Client) GetHTML(ctx context.Context, u *url.URL) (*Response, error)
func (c *Client) GetImage(ctx context.Context, u *url.URL, max int64) (*Response, error) // P1 preview images
var ErrBlocked, ErrTooLarge, ErrType, ErrRedirects error
func newForTest(allowLoopback bool) *Client // unexported; tests in-package only
```

**`platform/urlx`** (P0-W09): `Canonicalize(raw string) (*url.URL, error)` (lowercase
host, IDNA→ASCII, drop fragment, strip `utm_*`, `fbclid`, `gclid`, `dclid`, `mc_eid`,
`igshid`, `si`, `ref_src`…; the list lives in `testdata/tracking-params.json`, shared with
the client so hrefs shown in posts are stripped too), `DisplayHost(u) (unicode string,
lookalike bool)` (mixed-script detection only — no UTS #39 confusables table is available
in Go's x/text, and the docs/UI say "mixed scripts" rather than promising full confusable
detection), `CleanText(s string, max int) string` (NFC, strip Cc/Cf/bidi controls/zero-width,
collapse whitespace, truncate by runes — used for preview titles/descriptions and display
names).

**`platform/mdscan`** (P0-W09) — server's view of the Markdown subset; must agree with
the client renderer on shared fixtures `testdata/markdown/*.json`.
```go
type Link struct{ Raw string; NoPreview bool /* written as <url> */ ; Offset int /* code points */ }
type Mention struct{ Handle string /* canon */ ; Ord int; Offset int /* code points */ }
type Result struct{ Links []Link; Mentions []Mention }
func Scan(body string) Result
// goldmark (CommonMark + Strikethrough) with autolinking restricted to SCHEME-ONLY URLs (http:// https://);
// no "fuzzy" www./bare-domain linkify on either side (goldmark Linkify and linkify-it disagree there).
// html disabled; ignores code spans/blocks. Offsets and every length (20,000 cap, 280/500 counter) are in
// Unicode code points on both sides (JS uses [...s].length / codePointAt, never .length).
```

**P1 platform packages:**
- **`platform/blob`** (P1-W04): `type Store interface{ Put(ctx, key string, r io.Reader, size int64, mime string) error; Get(ctx, key) (io.ReadCloser, int64, string, error); Delete(ctx, key) error; URL(key string) string }`; drivers `fs.New(dir)` and `s3.New(cfg)` (minio-go, R2-compatible; the bucket is private and `r2.dev` access is off — documented in `deploy/runbooks/media.md`). `New(cfg BlobConfig, env Env) (Store, error)` — `prod` + `fs` requires `PCC_ALLOW_FS_BLOB=1` **and** a real scanner, and logs a WARN on every start.
- **`platform/scan`** (P1-W04): `type Scanner interface{ Name() string; Scan(ctx, img []byte, mime string) (Verdict, error) }`, `Verdict{Match bool; Info map[string]string}`. Drivers: `dev` (matches SHA-256 against `testdata/scan/blocklist.txt`; **constructible only when `cfg.DevLike()`**), `photodna` (HTTP client to the PhotoDNA Cloud Matching API, key from config), `none` (constructed only to report "disabled"). There is **no `cloudflare` driver**: Cloudflare's CSAM tool scans at the CDN after storage and cannot satisfy §6.1 "before storage". `New(cfg, env) (Scanner, error)`; whenever `cfg.MediaEnabled()` is false, uploads return 409 `{"error":"feature_disabled","feature":"media"}` and preview images are skipped. **A scanner error or timeout at upload time rejects the upload (fail closed); nothing is written to any store.**
- **`platform/imageproc`** (P1-W04): `Decode(b []byte) (image.Image, Format, error)` (jpeg/png/webp/gif first frame; runs `image.DecodeConfig` first and refuses > 40 MP or any side > 12,000 px **before** allocating — decompression bombs), `Resize(img, maxEdge int) image.Image`, `EncodeWebP(img, q int) ([]byte, error)`, `EncodeAVIF(...)` (behind a flag; WebP is the shipped default). Re-encoding strips all metadata.
- **`platform/webpush`** (P1-W05): `type Sender interface{ Send(ctx, sub Subscription, payload []byte, ttl time.Duration) (gone bool, err error) }`; `vapid` driver (webpush-go), `log` driver (dev; records to an in-memory ring readable by tests). `New(cfg) (Sender, error)` returns `ErrDisabled` when VAPID is missing in prod.
- **`platform/i18n`** (P1-W05): server catalogs `catalogs/<locale>/server.json` (embedded), `Format(locale, key string, args map[string]any) (string, error)` via `kaptinlin/messageformat-go`, fallback `en`; pseudo-locale `en-XA` generated in tests. Plus `i18n_test.go` "no prose" check: AST scan of `internal/...` forbidding string literals containing a space and a letter in calls to `webpush`/`Notify` payload builders.
- **`platform/dbsc`** (P1-W07): Device Bound Session Credentials protocol helpers: `RegistrationHeader(challenge) string` (`Sec-Session-Registration`), `ParseRegistrationJWT(b []byte, challenge []byte) (PublicJWK, error)`, `VerifyRefreshJWT(jwk, b, challenge, aud string) error` (checks `aud` equals the refresh URL, `jti` = the single-use challenge). ES256 **and** RS256 (many Windows TPMs are RSA-only).

### 4.3 `internal/store` (SCAFFOLD, complete)

```go
package store
type DB struct{ Pool *pgxpool.Pool }
func Open(ctx context.Context, url string, opts ...Option) (*DB, error) // WithMaxConns(n); serve default 20, tests 4
func (db *DB) Close()
type Q interface { // satisfied by *pgxpool.Pool, pgx.Tx
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
func (db *DB) Q() Q
func (db *DB) Tx(ctx context.Context, fn func(tx pgx.Tx) error) error          // READ COMMITTED, retries 40001/40P01 ×3
func (db *DB) TxSerializable(ctx context.Context, fn func(tx pgx.Tx) error) error // invite/budget/edge writes
func Migrate(ctx context.Context, db *DB) error
func IsUniqueViolation(err error) bool
```
`store/jobq`:
```go
type Queue struct{ /* db, handlers */ }
func New(db *store.DB, clk clock.Clock, log *slog.Logger) *Queue
func (q *Queue) Enqueue(ctx context.Context, tx store.Q, kind string, payload any, runAt time.Time, dedupe string) error
func (q *Queue) Handle(kind string, fn func(ctx context.Context, payload json.RawMessage) error)
func (q *Queue) Every(kind string, period time.Duration)  // periodic job, self-rescheduling, dedupe=kind
func (q *Queue) Run(ctx context.Context, workers int) error // SELECT ... FOR UPDATE SKIP LOCKED; backoff 2^n s
func (q *Queue) Drain(ctx context.Context) error            // tests: run all due jobs synchronously
```
Job kinds (registry, the WP that registers the handler): `preview.fetch` (W09),
`notify.flush` (W10, hourly batches), `metrics.flush` (W10, every minute), `seen.purge`,
`engage.hides_purge` (W06; hides kept one year, D31), `graph.edge_events_purge` and `friends.requests_scrub` (W03;
nulls declined-request timestamps after 30 d), `records.purge` (W11; deletes
`signed_records` past `purge_after`) and `records.audit_edges` (W11, weekly),
`postindex.trim` (W05), `probation.evaluate` (W04), `account.delete` and
`recovery.release` (W11), `limits.purge` (W12), `sessions.purge` (W01). P1:
`media.process` (P1-W04), `push.deliver` (P1-W05), `signals.nightly` and `signals.purge`
(P1-W06), `invites.diversity` (P1-W02). (W-numbers are Phase 0 WPs unless prefixed P1.)

`store/storetest`:
```go
func NewDB(t testing.TB) *store.DB // template DB pcc_tpl_<sha256(all migration files)[:8]>; each test: CREATE DATABASE pcc_t_<rand> TEMPLATE it;
                                   // pool MaxConns=4; dropped in t.Cleanup
func URL() string                  // PCC_TEST_DATABASE_URL, defaulting to the dev pcc_test URL unless PCC_ENV=prod
// There is no Skip helper: an unreachable DB fails the test when PCC_REQUIRE_DB=1 (scripts, CI) and
// skips with a loud message otherwise.
```
Connection budget (buildability A6): `pcc-postgres` runs with `max_connections=400`
(SCAFFOLD-0a runs `ALTER SYSTEM SET max_connections=400` and restarts the container once;
`docker-compose.yml` passes `-c max_connections=400`); scripts run `go test -p 4`; test pools
use `MaxConns=4`. That bounds one agent at ~16 test connections + 1 dev server, so 12 agents
stay well under 400.
Template creation is **cross-process safe** (`go test ./...` runs one binary per package
in parallel): connect to `pcc_test`, take `pg_advisory_lock(7314)`, create + migrate the
hash-named template only if it does not exist, close every connection to it, unlock.
Templates are never dropped by tests (a migration change yields a new name).
`scripts/db-reset.sh` drops only `pcc_t_*`/`pcc_e2e_*` databases **older than 6 hours with
zero connections** (`pg_stat_activity`) and `pcc_tpl_*` templates not referenced by any
current migration hash; it never uses `DROP DATABASE … WITH (FORCE)`, so it cannot kill
another agent's tests. The `pcc` role is a superuser in the dev/CI container, so
`CREATE DATABASE` is allowed.

### 4.4 `internal/gate` root (SCAFFOLD, contract) — what handlers can see

```go
package gate

type Ref string // opaque per-viewer reference (platform/refs)

// ---- failures (§13) ----
var ErrNotFound = errors.New("not_found")          // the ONE failure for missing / invisible / foreign / unauthenticated
var ErrRateLimited = errors.New("rate_limited")    // uniform 429, charged regardless of outcome
var ErrStepUp = errors.New("step_up_required")     // 401, client runs passkey step-up. P0: sensitive actions need auth_at ≤ 5 min;
                                                   // P1: also when automation signals set stepup_required
var ErrDisabled = errors.New("feature_disabled")   // fail-closed feature (media, push) — 409
type Problem struct{ Code string; Field string; Status int } // 400/409/422 about the caller's OWN input only
func NewProblem(status int, code string, field ...string) *Problem
func (p *Problem) Error() string

// ---- views ----
type InviterLabel struct {
    Kind   string `json:"kind"`             // "user" | "genesis" | "deleted"
    Handle string `json:"handle,omitempty"` // display-only; deliberately no ref (§3)
}
type ChainHop struct{ Ref Ref `json:"ref"`; Handle string `json:"handle"` }
type ChainView struct {
    Dist         int        `json:"dist"`               // 0 self, 1 friend, k ≥ 2
    FirstHop     *ChainHop  `json:"first_hop,omitempty"`// nil when dist ≤ 1
    Middle       []ChainHop `json:"middle,omitempty"`   // only when every named edge's endpoints opted in (§4.5). Inline by
                                                         // design; volume is bounded by feed_page/post_read budgets (fidelity #25)
    HiddenMiddle int        `json:"hidden_middle"`      // collapsed count ("2 others")
    FirstHopPlus bool       `json:"first_hop_plus"`     // P1: first edge is friend+
}
type PersonView struct {
    Ref         Ref          `json:"ref"`
    Handle      string       `json:"handle"`
    DisplayName string       `json:"display_name"`
    Inviter     InviterLabel `json:"inviter"`
    Relation    string       `json:"relation"`          // "self" | "friend" | "friend_plus" | "connected"
    Request     string       `json:"request,omitempty"` // "outgoing" | "incoming" (a declined request still reads "outgoing")
    Chain       ChainView    `json:"chain"`
    Blocked     bool         `json:"blocked,omitempty"` // only ever true on the BLOCKER's own view of someone they blocked
    Muted       bool         `json:"muted,omitempty"`
    HiddenVia   bool         `json:"hidden_via,omitempty"` // P1
}
type AuthorView struct { // compact person for cards
    Ref Ref `json:"ref"`; Handle string `json:"handle"`; DisplayName string `json:"display_name"`
    Inviter InviterLabel `json:"inviter"`; Chain ChainView `json:"chain"`
}
type ContextRow struct { // visible parent link (§10.1.2)
    Ref Ref `json:"ref"`; Kind string `json:"kind"` /* "ref" | "cont" */; AuthorHandle string `json:"author_handle"`; Excerpt string `json:"excerpt"` /* ≤80 runes */
}
type ThreadPos struct{ Index int `json:"index"`; Total int `json:"total"` } // over VISIBLE parts
type PreviewView struct {
    URL string `json:"url"`; Host string `json:"host"`; HostUnicode string `json:"host_unicode"`; Lookalike bool `json:"lookalike"`
    Title string `json:"title,omitempty"`; Description string `json:"description,omitempty"`
    Image *MediaView `json:"image,omitempty"` // P1
}
type MentionView struct{ Ord int `json:"ord"`; Ref Ref `json:"ref,omitempty"` } // no ref ⇒ render as plain text
type LikeSummary struct {
    InNetwork int `json:"in_network"`            // likers within 2 hops of the viewer
    Names []ChainHop `json:"names,omitempty"`    // viewer's friends who allow naming (≤3)
    Others int `json:"others"`; ByMe bool `json:"by_me"`
}
type MediaView struct{ Ref Ref `json:"ref"`; W int `json:"w"`; H int `json:"h"`; Alt string `json:"alt,omitempty"` } // P1; URL fetched separately
type PostView struct {
    Ref         Ref           `json:"ref"`
    Author      AuthorView    `json:"author"`
    Tier        int           `json:"tier"`                 // = Author.Chain.Dist
    Body        string        `json:"body"`                 // Markdown source; "" when not loaded (tree overflow)
    Lang        string        `json:"lang"`
    CreatedAt   time.Time     `json:"created_at"`
    EditedAt    *time.Time    `json:"edited_at,omitempty"`
    Audience    string        `json:"audience"`             // "connected" | "d1" | "d2" | "d3" | "friend_plus"(P1); a probation-forced
                                                            // post reads "d1" to others — byte-identical to a voluntary d1
    AudienceForced bool       `json:"audience_forced,omitempty"` // set ONLY on the author's own view
    RefPolicy   string        `json:"ref_policy"`           // "anyone" | "two_hops" | "friends" | "nobody"
    CanReference bool         `json:"can_reference"`
    Mine        bool          `json:"mine"`
    Context     []ContextRow  `json:"context,omitempty"`
    Thread      *ThreadPos    `json:"thread,omitempty"`
    Previews    []PreviewView `json:"previews,omitempty"`
    Mentions    []MentionView `json:"mentions,omitempty"`
    Likes       LikeSummary   `json:"likes"`
    SavedByMe   bool          `json:"saved_by_me,omitempty"` // P1
    ReplyCount  int           `json:"reply_count"`          // visible links only
    Media       []MediaView   `json:"media,omitempty"`      // P1
}
type FeedItem struct {
    Type      string     `json:"type"` // "tier_start" | "post" | "thread" | "collapsed" | "tier_end" | "caught_up"
    Tier      int        `json:"tier,omitempty"`
    Post      *PostView  `json:"post,omitempty"`
    Thread    []PostView `json:"thread,omitempty"`   // earlier visible parts (collapsed context) …, last = ranked part
    Collapsed *Collapsed `json:"collapsed,omitempty"`
}
type Collapsed struct{ Count int `json:"count"`; Token string `json:"token"`; Reason string `json:"reason"` /* "low_trust_distant" */ }
type FeedPage struct {
    Session  string     `json:"session"` // random 128-bit, stored keyed by (viewer, session); useless to anyone else
    Preset   string     `json:"preset"`  // "latest" | "balanced" | "top"
    Items    []FeedItem `json:"items"`
    Next     string     `json:"next,omitempty"` // empty ⇔ caught up
}
type WhyView struct {
    Tier int `json:"tier"`; Via ChainView `json:"via"`; PostedAt time.Time `json:"posted_at"`; Preset string `json:"preset"`
    ShiftMinutes int `json:"shift_minutes"`; Reasons []string `json:"reasons"` // network_likes, below_author_usual, reply_penalty, good_replies, cold_start, show_less, chronological
}
type TreeItem struct {
    Post        PostView   `json:"post"`
    TierDivider bool       `json:"tier_divider,omitempty"` // first item of a new tier inside the row
    ShownAbove  bool       `json:"shown_above,omitempty"`  // diamond: appears in a row above
    AlsoRepliesTo []ChainHop `json:"also_replies_to,omitempty"` // extra visible parents' authors; chip refocuses via Post refs below
    AlsoParents []Ref      `json:"also_parents,omitempty"`
}
type TreeRow struct {
    Gen    int        `json:"gen"`    // −k … +k relative to the focal row (0)
    Label  string     `json:"label"`  // "earlier" | "replying_to" | "this_post" | "replies" | "replies_to_replies" | "generations_down" | "generations_up"
    Center Ref        `json:"center"` // ref of the post whose children/parents this row lists
    Items  []TreeItem `json:"items"`  // ranked; Items[0] is the centered post (focal row: focal is centered)
    More   int        `json:"more"`   // visible items beyond those returned
    Cursor string     `json:"cursor,omitempty"`
    Beyond bool       `json:"beyond,omitempty"` // Row (gen ≠ 0): Items[0] has a further visible row away from the focal row (INT-0)
}
type TreeView struct {
    Snapshot string    `json:"snapshot"`
    Focal    Ref       `json:"focal"`
    Rows     []TreeRow `json:"rows"`  // 2 above + focal + 2 below when present
    HasEarlier bool    `json:"has_earlier"`; HasLater bool `json:"has_later"`
}
type MeView struct {
    Ref Ref `json:"ref"`; Handle string `json:"handle"`; DisplayName string `json:"display_name"`
    Role string `json:"role"` // "member" | "moderator" | "admin"
    Inviter InviterLabel `json:"inviter"`; Settings SettingsView `json:"settings"`
}
type SettingsView struct {
    UILocale string `json:"ui_locale"`; Languages []string `json:"languages"`; TranslateTo string `json:"translate_to,omitempty"`
    Autotranslate bool `json:"autotranslate"`; RankPreset string `json:"rank_preset"`
    NameInChains bool `json:"name_in_chains"`; Suggestible bool `json:"suggestible"`; NamedInLikes bool `json:"named_in_likes"`
    SlipperyDefault bool `json:"slippery_default"`; UIPrefs json.RawMessage `json:"ui_prefs"`
}
type SessionView struct {
    Authenticated bool `json:"authenticated"`; Kind string `json:"kind"` /* "none"|"guest"|"user"|"enrollment" */
    CSRF string `json:"csrf,omitempty"`; Me *MeView `json:"me,omitempty"`; StepUp bool `json:"step_up,omitempty"`
}
type NotificationView struct {
    ID Ref `json:"id"`; Kind string `json:"kind"`; Count int `json:"count"`; Person *ChainHop `json:"person,omitempty"`
    Post *Ref `json:"post,omitempty"`; Data map[string]any `json:"data,omitempty"` // codes/numbers only
    At time.Time `json:"at"`; Read bool `json:"read"`
}
type BudgetView struct {
    Available int `json:"available"`; Cap int `json:"cap"`; NextAt *time.Time `json:"next_at,omitempty"`
    Probation *ProbationView `json:"probation,omitempty"`; PausedUntil *time.Time `json:"paused_until,omitempty"`; Revoked bool `json:"revoked,omitempty"`
}
type ProbationView struct{ Until time.Time `json:"until"`; NeedOutsideFriends int `json:"need_outside_friends"` }
type HandshakeView struct { // invites (P0), friend+ upgrade + recovery (P1) all return this
    HS Ref `json:"hs"`; Mode string `json:"mode"` /* "qr" | "qr_audio" | "dual_qr" */; FrameTTLms int `json:"frame_ttl_ms"`
    FrameKey string `json:"frame_key,omitempty"` // P1: base64url key for client-rendered 250 ms frames
}
type HandshakeStatus struct { // inviter side (GET …/handshakes/{hs}) and invitee side (GET /join/status)
    State string `json:"state"`           // "open" | "claimed" | "confirmed" | "done" | "cancelled" | "expired"
    SAS   string `json:"sas,omitempty"`   // 6 digits = HMAC(secret, claimed_session_hash) mod 10^6, shown on BOTH screens once claimed
}
type FrameView struct{ Payload string `json:"payload"`; ValidUntil time.Time `json:"valid_until"` }
type InviteeView struct { // the inviter's own invitee list (§8: probation visible to the inviter)
    Person PersonView `json:"person"`; JoinedAt time.Time `json:"joined_at"`
    OnProbation bool `json:"on_probation"`; ProbationUntil *time.Time `json:"probation_until,omitempty"`
}
type Page[T any] struct{ Items []T `json:"items"`; Next string `json:"next,omitempty"` }
type Class struct{ Kind byte /* 'r' read, 'w' write, '-' free */; Name string } // request charge class (plan §5.1)

// ---- service DTOs used across the API (contract; buildability B2) ----
type SettingsPatch struct { // PATCH /api/me/settings — nil pointer = unchanged
    UILocale *string `json:"ui_locale,omitempty"`; Languages *[]string `json:"languages,omitempty"`
    TranslateTo *string `json:"translate_to,omitempty"`; Autotranslate *bool `json:"autotranslate,omitempty"`
    RankPreset *string `json:"rank_preset,omitempty"`; NameInChains *bool `json:"name_in_chains,omitempty"`
    Suggestible *bool `json:"suggestible,omitempty"`; NamedInLikes *bool `json:"named_in_likes,omitempty"`
    SlipperyDefault *bool `json:"slippery_default,omitempty"`; UIPrefs *json.RawMessage `json:"ui_prefs,omitempty"`
    DisplayName *string `json:"display_name,omitempty"`
}
type KeyView struct {
    KeyID string `json:"key_id"`; Alg string `json:"alg"`; PublicKey string `json:"public_key"` /* base64url */
    Fingerprint string `json:"fingerprint"` /* hex sha256(pubkey) */; ValidFrom time.Time `json:"valid_from"`; ValidUntil *time.Time `json:"valid_until,omitempty"`
}
type VerifyReport struct { // records.VerifyExport → printed by `pcc export-verify`
    Records int `json:"records"`; Valid int `json:"valid"`; Failures []VerifyFailure `json:"failures,omitempty"`
    KeysPinned bool `json:"keys_pinned"`; BodiesChecked int `json:"bodies_checked"`
}
type VerifyFailure struct{ Line int `json:"line"`; Code string `json:"code"` /* bad_sig, kind_mismatch, not_canonical, unknown_key, key_window, commit_mismatch, bad_assertion */ }
type PushSub struct { // P1
    Endpoint string `json:"endpoint"`; Keys struct{ P256dh string `json:"p256dh"`; Auth string `json:"auth"` } `json:"keys"`
    HideContent bool `json:"hide_content"`
}
type ModItem struct { // moderators only
    ID Ref `json:"id"`; Source string `json:"source"` /* report | signal | csam | stepup | invite_cluster */; Priority int `json:"priority"`
    Reason string `json:"reason"`; Subject *ChainHop `json:"subject,omitempty"` /* moderator-scoped ref */
    SubjectInviter *InviterLabel `json:"subject_inviter,omitempty"`; SubjectProbation bool `json:"subject_probation"`
    Post *PostView `json:"post,omitempty"` /* reported content only; CSAM items NEVER carry content */
    Summary map[string]int `json:"summary,omitempty"`; At time.Time `json:"at"`; ClaimedByMe bool `json:"claimed_by_me"`
    AwaitingSecond bool `json:"awaiting_second"`
}
type BlameItem struct {
    ID Ref `json:"id"`; Action string `json:"action"`; Reason string `json:"reason"`; InviteeHandle string `json:"invitee_handle"`
    Post *PostView `json:"post,omitempty"` /* only if the inviter could already see it */; At time.Time `json:"at"`; Read bool `json:"read"`
}
type LogItem struct {
    Day string `json:"day"`; Action string `json:"action"`; Reason string `json:"reason"`; Count int `json:"count"`
    Subject *LogSubject `json:"subject,omitempty"`
}
type LogSubject struct{ Pseudonym string `json:"pseudonym,omitempty"`; Ref Ref `json:"ref,omitempty"`; Handle string `json:"handle,omitempty"` }
```

### 4.5 `internal/gate/internal/core` (SCAFFOLD, contract) — what gate modules share

```go
package core

type UserID uint32           // < 2^32 (DB CHECK); roaring-friendly
type PostID = uuid.UUID
type Role uint8              // RoleMember, RoleModerator, RoleAdmin
type SessionKind uint8       // SessGuest, SessUser, SessEnrollment
type EdgeLevel uint8         // LevelFriend=1, LevelFriendPlus=2
type Audience uint8          // AudConnected=0, AudD1=1, AudD2=2, AudD3=3, AudFriendPlus=10
type RefPolicy uint8         // PolAnyone, PolTwoHops, PolFriends, PolNobody
type LinkKind uint8          // LinkRef=0, LinkCont=1
type Preset uint8            // PresetLatest, PresetBalanced, PresetTop (P1)
type ModReason string        // closed enum: spam, bot, harassment, csam, illegal, impersonation, misleading_preview, ban_evasion, other
type ReportReason string     // closed enum: the same minus ban_evasion

type Viewer struct {
    ID UserID; Session [32]byte; Kind SessionKind; Role Role; AuthAt time.Time; StepUp bool; Credential []byte
}
func WithViewer(ctx context.Context, v Viewer) context.Context
func ViewerFrom(ctx context.Context) (Viewer, bool)
func RequireUser(ctx context.Context) (Viewer, error)      // gate.ErrNotFound unless Kind==SessUser; gate.ErrStepUp if StepUp pending
func RequireFresh(ctx context.Context, now time.Time) (Viewer, error) // RequireUser + AuthAt within 5 min, else gate.ErrStepUp
func RequireModerator(ctx context.Context) (Viewer, error)

// ---- refs (wraps platform/refs with typed helpers; never mints for viewer 0) ----
type Refs struct{ C *refs.Codec }
func (r Refs) User(v, target UserID) gate.Ref
func (r Refs) Post(v UserID, p PostID) gate.Ref
func (r Refs) OpenUser(v UserID, ref gate.Ref) (UserID, bool)
func (r Refs) OpenPost(v UserID, ref gate.Ref) (PostID, bool)
// …Media, Notif, Report, Cred, Handshake, ModItem likewise

// ---- users (impl: gate/users — written complete by SCAFFOLD-0a, owned by P0-W11 afterwards) ----
type UserMeta struct {
    ID UserID; ExtID uuid.UUID; Handle, DisplayName string; InvitedBy UserID /*0 = none*/; Genesis bool
    Status uint8; Role Role; CreatedAt time.Time; ProbationUntil *time.Time; ProbationCleared bool
    NameInChains, Suggestible, NamedInLikes, SlipperyDefault bool; Preset Preset; Locale string; Languages []string
}
func (u UserMeta) OnProbation(now time.Time) bool
type NewUser struct {
    Handle, DisplayName, Locale string; Languages []string; InvitedBy UserID; Genesis bool
    ProbationUntil *time.Time; TOSVersion int; AgeAttested bool; WebAuthnID []byte; Status uint8
}
type Users interface {
    Get(ctx context.Context, id UserID) (UserMeta, error)              // ErrNotFound if absent
    GetMany(ctx context.Context, ids []UserID) (map[UserID]UserMeta, error)
    InviterLabel(ctx context.Context, id UserID) (gate.InviterLabel, error)
    InviterOf(id UserID) (UserID, bool)                                // in-memory, for rank caps
    ByHandle(ctx context.Context, canon string) (UserID, bool)         // INTERNAL ONLY (mentions within horizon, CLI)
    Create(ctx context.Context, tx store.Q, u NewUser) (UserID, error) // Problem handle_taken on unique violation
    HandleAvailable(ctx context.Context, canon string) bool            // join flow only (see D21)
}

// ---- graph (impl: gate/graph) ----
type Edge struct{ To UserID; Level EdgeLevel; Since time.Time }
type Exclusions struct{ Blocked *roaring.Bitmap; HiddenVia *roaring.Bitmap } // the READER's own blocks / hide-vias only (D7)
type Search interface {
    Viewer() UserID
    Epoch() uint64
    Dist(u UserID) (int, bool)              // exact shortest distance avoiding the reader's exclusions; (0,true) for self
    FirstHop(u UserID) UserID               // chain's first hop (0 if dist ≤ 1), chosen by the D22 rule
    FirstHopPlus(u UserID) bool             // P1: the reader's edge to FirstHop(u) is friend+
    Path(u UserID) []UserID                 // a shortest path viewer→u (inclusive) consistent with FirstHop
    Layer(k int) []UserID                   // exact set at distance k (sorted)
    LayerBitmap(k int) *roaring.Bitmap
    MaxLayer() int                          // deepest non-empty layer
}
type Graph interface {
    Epoch() uint64
    Friends(u UserID) []Edge                // sorted by (level desc, since asc, id asc)
    Level(a, b UserID) (EdgeLevel, bool)
    Degree(u UserID) int
    Search(ctx context.Context, v UserID, ex Exclusions) (Search, error) // P0: full BFS each call
    // mutations: called AFTER commit by the table owner (plan §3.4)
    AddEdge(a, b UserID, lvl EdgeLevel, since time.Time)
    RemoveEdge(a, b UserID)                 // bumps epoch
    SetBlock(blocker, blocked UserID, on bool) // bumps epoch on add
    SetHideVia(user, via UserID, on bool)   // P1; bumps epoch on add
    Exclusions(v UserID) Exclusions         // v's blocks (+ hide-via) from memory
    Blocks(a, b UserID) bool                // a has blocked b (one direction) — for shadow links, requests, notifications
    Load(ctx context.Context) error         // from edges / blocks / hide_via
}
type Edges interface { // impl: gate/friends (sole writer of edges/edge_events)
    Create(ctx context.Context, tx store.Q, a, b UserID, lvl EdgeLevel, recordHash []byte) error
    Revoke(ctx context.Context, tx store.Q, a, b UserID, by UserID) error // writes Revocation record + purge_after on both records
    Upgrade(ctx context.Context, tx store.Q, a, b UserID, recordHash []byte) error // P1 friend → friend+
    Count(ctx context.Context, u UserID) (edges, outgoingPending int, err error)   // cap accounting (D23)
}
type Mutes interface{ MutedAuthors(ctx context.Context, v UserID) map[UserID]struct{} } // impl: gate/friends

// ---- posts index (impl: gate/postindex) ----
type PostMeta struct {
    ID PostID; Author UserID; CreatedAt time.Time; Audience Audience; AudienceForced bool; RefPolicy RefPolicy
    IsReply bool        // has a ref parent (thread: head part has one)
    ContParent PostID   // uuid.Nil if none
    ContChild  PostID
    Deleted bool; Version int; EditedAt *time.Time
}
type LinkMeta struct{ Child, Parent PostID; Kind LinkKind; ChildAuthor, ParentAuthor UserID; Detached, Shadow bool; CreatedAt time.Time }
type PostIndex interface {
    Meta(ctx context.Context, id PostID) (PostMeta, bool)              // memory for window posts, LRU+DB otherwise
    MetaMany(ctx context.Context, ids []PostID) map[PostID]PostMeta
    Recent(author UserID) []PostID                                      // window posts, newest first
    RecentLen(author UserID) int
    WindowScan(fn func(PostMeta) bool)                                  // newest first over all window posts (time scan)
    WindowSize() int
    Children(ctx context.Context, parent PostID, fn func(LinkMeta) bool) // newest first, ALL links; RAW — A10 restricts callers
    Parents(ctx context.Context, child PostID) []LinkMeta               // RAW — A10 restricts callers
    Bodies(ctx context.Context, ids []PostID) map[PostID]string
    // mutations (posts module only)
    Put(m PostMeta); Delete(id PostID); PutLink(l LinkMeta); UpdateLink(l LinkMeta)
}

// ---- visibility (impl: gate/visibility) — §2.2, §4, §10.1.3 ----
// Checked is a capability: only visibility (and coreref) may call MintChecked (archtest A10).
type Checked struct{ meta PostMeta; viewer UserID; epoch uint64 }
func MintChecked(m PostMeta, v UserID, epoch uint64) Checked
func (c Checked) Meta() PostMeta
type VisibleLink struct{ Link LinkMeta; Other Checked } // Other = the child (VisibleChildren) or parent (VisibleParents)
type Visibility interface {
    SearchFor(ctx context.Context, v UserID) (Search, error)            // Graph.Search with v's own exclusions
    CanSeeUser(s Search, u UserID) bool                                  // finite dist in s; nothing else (D7)
    CheckPost(ctx context.Context, s Search, p PostMeta) (Checked, bool) // §4 rules 1, 4, 5 + deleted + suspended author
    CheckPosts(ctx context.Context, s Search, ps []PostMeta) []Checked   // order-preserving filter
    CanSeeLink(ctx context.Context, s Search, l LinkMeta, child, parent PostMeta) bool // §10.1.3 exactly
    VisibleChildren(ctx context.Context, s Search, parent PostMeta, f ChildFilter) []VisibleLink
    VisibleParents(ctx context.Context, s Search, child PostMeta) []VisibleLink
    CanReference(ctx context.Context, s Search, parent PostMeta, kind LinkKind) bool
}
type ChildFilter struct {
    RefOnly bool        // kind == ref
    NotSelf bool        // child author ≠ parent author (self-replies)
    Limit   int         // newest-first, applied AFTER every visibility/kind/self filter (fidelity #2)
}
type Chains interface { // impl: gate/visibility (P0-W02 → P1-W03)
    Chain(ctx context.Context, s Search, u UserID) (gate.ChainView, error) // §4.5 naming rule; first-hop per D22
}

// ---- engagement (impl: gate/engage) ----
type Engagement interface {
    Likers(ctx context.Context, p PostID) *roaring.Bitmap              // read-only snapshot
    Savers(ctx context.Context, p PostID) *roaring.Bitmap              // P1
    LikedByMe(ctx context.Context, v UserID, ps []PostID) map[PostID]bool
    SeenSet(ctx context.Context, v UserID, since time.Time) map[PostID]struct{}
    HiddenSet(ctx context.Context, v UserID) map[PostID]struct{}
    ShowLess(ctx context.Context, v UserID) map[UserID]int
}

// ---- preferences / ranking context (impl: gate/engage, consumed by feed+tree) ----
type Prefer interface {
    // n for post p as seen from s (§5.2 / §5.5), memoized per (snapshot, post)
    N(ctx context.Context, s Search, p Checked, memo *Memo) float64
    // n_kid per child for upward credit: children from Visibility.VisibleChildren(RefOnly, NotSelf, Limit: K=20),
    // non-shadow (shadow links are invisible to non-authors anyway); gens=2 adds β·Σ grandchildren (P1, K² cap).
    // A parent with RefPolicy == PolNobody returns nil (slippery ⇒ no reply credit, fidelity #3).
    KidsN(ctx context.Context, s Search, p Checked, memo *Memo, gens int) []float64
}
type MemoKey struct{ Kind uint8; Post PostID }
type Memo struct{ /* map + mutex */ }  // per-snapshot cache with an exported API (buildability B1)
func NewMemo() *Memo
func (m *Memo) Get(k MemoKey) (any, bool)
func (m *Memo) Put(k MemoKey, v any)

// ---- views (impl: gate/views) ----
type ViewOpts struct{ WithBody bool; WithContext bool }
type Views interface {
    Posts(ctx context.Context, s Search, ps []Checked, o ViewOpts) ([]gate.PostView, error) // only Checked input (A10)
    Person(ctx context.Context, s Search, u UserID) (gate.PersonView, error)                // ErrNotFound unless CanSeeUser
    Author(ctx context.Context, s Search, u UserID) (gate.AuthorView, error)
}

// ---- records (impl: gate/records) ----
type RecordKind uint8 // RecInvite=1, RecFriendship=2, RecRevocation=3, RecPost=4, RecTombstone=5, RecBlock=6, RecReport=7, RecPasskeyChange=8, RecModAction=9
type Signer interface {
    // P0: server key. P1: author's custodial key + server countersign; assertions attached when given.
    // body must follow the sigs value grammar; Sign injects "kind" and "schema_version".
    Sign(ctx context.Context, tx store.Q, kind RecordKind, author UserID, body any, parties []UserID, a ...Assertion) (hash []byte, err error)
    Draft(kind RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) (canon []byte, challenge [32]byte, err error) // P1 co-sign drafts
    SetPurgeAfter(ctx context.Context, tx store.Q, hash []byte, at time.Time) error // §2.3 revoked-edge retention
    Delete(ctx context.Context, tx store.Q, hash []byte) error                       // Block record on unblock
}
type Assertion struct{ CredentialID, AuthenticatorData, ClientDataJSON, Signature []byte; User UserID }

// ---- passkeys / sessions (impl: gate/auth) ----
type Credential struct { // buildability B2
    ID, PublicKey []byte; Alg int; AAGUID []byte; SignCount uint32; Transports []string
    BackupEligible, BackupState, UV bool; AttestationFmt string
}
type PendingUser struct{ WebAuthnID []byte; Handle, DisplayName string; Exclude [][]byte }
type Passkeys interface {
    BeginRegistration(ctx context.Context, sess [32]byte, u PendingUser) (json.RawMessage, error)
    FinishRegistration(ctx context.Context, sess [32]byte, body []byte) (Credential, error) // not yet bound to a user row
    BindCredential(ctx context.Context, tx store.Q, user UserID, c Credential) error         // also writes the PasskeyChange record
    Upgrade(ctx context.Context, tx store.Q, sess [32]byte, user UserID, cred []byte) (newToken string, err error) // guest→user, rotates token + csrf seed
    // Assertions over a server challenge, restricted to the VIEWER's own credentials (security #9):
    // allowCredentials = viewer's credentials; Finish checks credential.user == viewer, UP=1, UV=1,
    // type "webauthn.get", origin == cfg.Origin, crossOrigin false/absent, no topOrigin, rpIdHash, BE flag unchanged,
    // sign_count rule; the ceremony is session-bound and cleared on any finish attempt. Used by step-up (P0)
    // and by co-signing (P1).
    BeginAssertion(ctx context.Context, v Viewer, challenge []byte, purpose string) (json.RawMessage, error)
    VerifyAssertion(ctx context.Context, v Viewer, purpose string, body []byte) (Assertion, error)
    IssueEnrollment(ctx context.Context, tx store.Q, user UserID, purpose uint8, usableFrom time.Time) (token string, err error)
    RevokeAll(ctx context.Context, tx store.Q, user UserID) error
}

// ---- invites (impl: gate/invites) ----
type Invites interface {
    Invitees(ctx context.Context, inviter UserID) ([]UserID, error)
    Subtree(ctx context.Context, root UserID) (*roaring.Bitmap, error)       // quarantine (P1), probation outside-subtree count
}
type Probation interface { // impl: gate/invites
    Reset(ctx context.Context, tx store.Q, users []UserID, until time.Time) error // P1 quarantine
}

// ---- notifications (impl: gate/notify) ----
type Event struct {
    To UserID; Kind string; GroupKey string; SubjectUser UserID; SubjectPost PostID; Data map[string]any
}
type Notifier interface {
    Notify(ctx context.Context, tx store.Q, e Event) error    // persists; batches on GroupKey; SSE ping after commit;
                                                              // DROPS the event if e.To has blocked e.SubjectUser (D7)
    Live(to UserID, topic string, data any)                    // ephemeral SSE event (handshake progress, step-up)
}

// ---- metrics (impl: gate/metrics) — aggregates only, no IDs (fidelity #6) ----
type Metrics interface {
    Inc(name, bucket string, n int64)              // name/bucket from the closed list in gate/metrics/names.go
    ObserveMs(name string, ms int64)               // fixed latency buckets; p95 computed in the report
}

// ---- limits (impl: gate/limits) ----
type ReadClass string  // "feed_page","tree_view","graph_read","post_read","media_full"(P1)
type WriteKind string  // "post","like","friend_request","report","reference","invite_handshake","social","account","save"(P1),"media"(P1)
type Limiter interface {
    ChargeRead(ctx context.Context, v Viewer, c ReadClass) error          // ErrRateLimited
    ChargeWrite(ctx context.Context, v Viewer, k WriteKind, n int) error  // ErrRateLimited; spacing + hourly + daily
    ChargeAnon(ip netip.Prefix, bucket string) error                      // unauthenticated surface (memory only)
}

// ---- in-process bus (SCAFFOLD, complete) ----
type Bus struct{ /* typed fan-out, synchronous after commit */ }
func (b *Bus) Publish(ev any)
func Subscribe[T any](b *Bus, fn func(T))
```
Bus event types (`core/events.go`, contract; all fields raw IDs; adding a type or field is an
additive steward change):
```go
type EdgeAdded       struct{ A, B UserID; Level EdgeLevel; At time.Time }
type EdgeRemoved     struct{ A, B UserID; By UserID; At time.Time }
type EdgeUpgraded    struct{ A, B UserID; At time.Time }                     // P1
type BlockChanged    struct{ Blocker, Blocked UserID; On bool }
type HideViaChanged  struct{ User, Via UserID; On bool }                     // P1
type PostCreated     struct{ Post PostMeta; Links []LinkMeta }
type PostEdited      struct{ ID PostID; Version int }
type PostDeleted     struct{ ID PostID; Author UserID }
type LinkChanged     struct{ Link LinkMeta }                                 // detach / ref-policy driven changes
type UserChanged     struct{ ID UserID }                                     // settings, display name, status
type ProbationCleared struct{ ID UserID; At time.Time }
type Liked           struct{ User UserID; Post PostID; On bool }
type SessionEnded    struct{ User UserID; Session [32]byte }                 // logout/expiry/revocation/deletion: SSE closes,
                                                                             // P1 push unsubscribes that session
type AccountDeleted  struct{ ID UserID }
type SessionStepUp   struct{ User UserID; Session [32]byte }                 // P1
```
Dependency container:
```go
type Deps struct { // late-bound; populated in gate/wire before Init
    DB *store.DB; Jobs *jobq.Queue; Clock clock.Clock; IDs ids.Gen; Refs Refs; Cfg *config.Config; Log *slog.Logger; Bus *Bus
    Keys DerivedKeys // HKDF outputs (plan §4.2 config): Cursor, Pseudonym, MediaURL, LogSalt(day), MetricsSalt(day)
    Users Users; Graph Graph; Edges Edges; Mutes Mutes; Posts PostIndex; Vis Visibility; Chains Chains
    Engage Engagement; Prefer Prefer; Views Views; Signer Signer; Passkeys Passkeys; Invites Invites; Probation Probation
    Notify Notifier; Limits Limiter; Metrics Metrics
}
```

**`gate/internal/coreref`** (SCAFFOLD-0a, production-safe, no `testing` import): correct
reference implementations of every interface above — `MemGraph` (plain BFS with the D22
first-hop rule), `MemPosts`, `MemVisibility` (the full §4/§10.1.3 rules, D7 one-directional
blocks), `MemChains`, `MemEngage`, `MemPrefer` (§5.5 n, inviter cap, K-after-filter credit),
`MemViews` (minimal but correct PostView), `MemUsers`, `ServerSigner` (real Ed25519,
in-memory key), `RecordingNotifier`, `PassLimiter` (unlimited), `MemMetrics`. Every domain
stub created by the scaffold **delegates to its coreref counterpart** until its WP replaces
it, so the whole app runs end-to-end from the scaffold commit (buildability A2). P1 adds
`MemCosign`, `MemHandshakes`, `MemMedia`, `MemModQueue`.

**`gate/internal/coretest`** (SCAFFOLD): `FakePasskeys` (software authenticator accepting a
deterministic ES256 key per handle), `World` builder (`w.User("a")`, `w.Friends("a","b")`,
`w.Block("a","b")`, `w.Post("a", "p1", …)`, `w.Like("b","p1")`) that writes the **same**
facts both to a `storetest` DB (SQL fixtures, explicit high-range IDs, `ids.Named` post IDs,
then `setval`) and to the coreref in-memory implementations, so DB-backed and memory-backed
tests agree (buildability B3); `FixedKeys()`; `Clock` at `2026-01-01T00:00:00Z`.

### 4.6 `internal/gate/wire` (SCAFFOLD)

```go
type App struct {
    Auth *auth.Service; Users *users.Service; Graph *graph.Graph; Visibility *visibility.Service; Views *views.Service
    Friends *friends.Service; Invites *invites.Service; Posts *posts.Service; PostIndex *postindex.Index
    Engage *engage.Service; Feed *feed.Service; Tree *tree.Service; Previews *previews.Service; Notify *notify.Service
    Metrics *metrics.Service; Records *records.Service; Account *account.Service; Limits *limits.Service
    Moderation *moderation.Service; DevSeed *devseed.Service
    // P1 (added by SCAFFOLD-1): Cosign *cosign.Service; Media *media.Service; Themes *themes.Service;
    //   Behavior *behavior.Service; Automation *automation.Service
}
func Build(ctx context.Context, cfg *config.Config, db *store.DB, log *slog.Logger) (*App, error)
// Order: core.Deps base → New() every service with *core.Deps → fill d.Users/d.Graph/... → each svc.Init(ctx)
// (register jobs, subscribe Bus, warm caches; Graph.Load; PostIndex.Load) → return.
func (a *App) Start(ctx context.Context) error   // starts job workers + SSE hub + metrics flusher
// Runtime test hooks (non-nil only when cfg.Env == "test" && cfg.TestHooks; Build then uses a clock.Fake):
func (a *App) Hooks() *Hooks
// Hooks: Reset(ctx), AdvanceClock(d), Seed(ctx, fixture string) (loads testdata/fixtures/<name>.json),
//        Login(ctx, handle) (cookie, csrf string), GenesisToken(ctx, handle) (string, error)
```
Go-test support lives in a separate package so the production binary never links
`testing`: **`internal/gate/wire/wiretest`** (SCAFFOLD) —
```go
func NewApp(t testing.TB, opts ...Option) *wire.App // storetest DB, fake clock, Named ids, fixed keys, coretest.FakePasskeys
func Login(t testing.TB, a *wire.App, handle string) (cookie, csrf string) // creates user + session directly (scaffold-owned session code)
func World(t testing.TB, a *wire.App) *TestWorld   // exported wrapper over coretest.World (handles in, refs out)
```
`internal/api/apitest` (SCAFFOLD) offers two harnesses (buildability A1):
`apitest.Service(t, register func(chi.Router, *api.Deps), deps)` mounts **one** module's
routes over coreref-backed deps (what a WP's handler tests use — they compile only that
module), and `apitest.App(t)` over `wiretest.NewApp` (INT tests). Both validate every request
and response against `_common.yaml` + the module's own fragment, merged in Go with
kin-openapi (no Node step, buildability B7). `internal/api/testhooks` (e2e) uses
`App.Hooks()`.

Every domain package exposes exactly `func New(d *core.Deps) *Service` and
`func (s *Service) Init(ctx context.Context) error` (stubs exist from scaffold).

### 4.7 Domain packages under `internal/gate/` (WP-owned)

Each block: responsibility · ARCHITECTURE refs · exported API used by handlers (signatures
obey A5). Imports besides `gate`, `gate/internal/core`, `store`, `store/jobq`,
`platform/{clock,ids,config,logx}` are listed where relevant.

**`gate/auth`** (P0-W01 → P1-W07) — §17.2, §10.3, §13. Implements `core.Passkeys`.
The scaffold writes a **working** session-lookup `Middleware`, `CSRF` check and guest
session creation (buildability A2); W01 owns and completes it.
```go
func New(d *core.Deps) *Service
func (s *Service) Middleware(next http.Handler) http.Handler // ALL routes: loads session → core.WithViewer; slides expiry. No origin checks here.
func (s *Service) APIGuard(next http.Handler) http.Handler   // /api/* only: Sec-Fetch-Site ∈ {same-origin, absent} on every request;
                                                             // unsafe methods also need Origin == cfg.Origin exactly — including the
                                                             // unauthenticated POSTs (login, join, enroll) — and X-PCC-CSRF ==
                                                             // base64url(HMAC(CSRFKey, id_hash ‖ csrf_seed)[:16]). Failure ⇒ uniform 404.
                                                             // SPA navigations (Sec-Fetch-Site: none / cross-site) are never checked.
func (s *Service) Session(ctx context.Context) (gate.SessionView, error)
func (s *Service) StartGuest(ctx context.Context) (token string, err error)            // /api/join/start (P1 /api/recovery/claim)
func (s *Service) LoginBegin(ctx context.Context) (token string, opts json.RawMessage, err error)
//   creates a guest session holding the challenge (purpose=login, 2 min TTL, single use) — security #11
func (s *Service) LoginFinish(ctx context.Context, body []byte) (token string, me gate.MeView, err error)
//   clears the ceremony first (any outcome); deletes the guest session and issues a NEW user token (no fixation);
//   users with status suspended/deleted/pending_enrollment ⇒ the same uniform 404 as a bad assertion
func (s *Service) Logout(ctx context.Context) error                                    // publishes SessionEnded
func (s *Service) StepUpBegin(ctx context.Context) (json.RawMessage, error)            // UV assertion over the viewer's OWN credentials
func (s *Service) StepUpFinish(ctx context.Context, body []byte) error                 // sets auth_at; clears stepup_required (P1)
func (s *Service) Passkeys(ctx context.Context) ([]PasskeyView, error)                 // PasskeyView{ID gate.Ref; Name string; CreatedAt, LastUsedAt time.Time; Current bool}
func (s *Service) AddPasskeyBegin(ctx context.Context) (json.RawMessage, error)        // RequireFresh; excludeCredentials = user's credentials
func (s *Service) AddPasskeyFinish(ctx context.Context, body []byte, name string) error // RequireFresh; PasskeyChange record; friends notified (friend_security)
func (s *Service) RemovePasskey(ctx context.Context, id gate.Ref) error                 // RequireFresh; never the last one (Problem last_passkey);
                                                                                        // revokes every session that credential created
func (s *Service) EnrollBegin(ctx context.Context, token string) (EnrollView, error)    // token from the URL FRAGMENT (POSTed by the SPA);
                                                                                        // binds the token to this session; EnrollView{Options; Purpose;
                                                                                        // ExistingPasskeys []PasskeyView /* recovery: shown with a "remove old" option */}
func (s *Service) EnrollFinish(ctx context.Context, body []byte, removeOld bool, tosVersion int, ageAttested bool) (sessToken string, err error)
//   marks the token used atomically; recovery: revokes all sessions, optionally removes old passkeys, notifies friends;
//   genesis: records ToS + age attestation (Problems tos_required, age_required)
func (s *Service) SeedGenesis(ctx context.Context, handle, name string) (enrollURL string, err error) // CLI; URL uses "#<token>"
```
Session lifetimes P0: user 30 d absolute / 14 d idle; guest 30 min; enrollment 30 min; a
ceremony 2 min. Step-up freshness: 5 min (`core.RequireFresh`) for passkey add/remove,
account delete, export, and moderator two-person approvals. Unauthenticated `/api/*`
(except `/api/session`, `/api/auth/login/*`, `/api/join/*`, `/api/enroll/*`,
`/api/csp-report`, P1 `/api/recovery/claim*`, `/api/auth/dbsc/*`) ⇒ `ErrNotFound`.
Request logging redacts nothing because nothing secret is ever in a path: enrollment
tokens travel in the fragment and request bodies only (security #13).

**`gate/users`** (SCAFFOLD-0a complete → P0-W11 → P1-W10) — implements `core.Users` (cached,
invalidated via `UserChanged`), inviter labels (`user` / `genesis` / `deleted`),
`Create` (the only `INSERT INTO users`), `HandleAvailable`. Display names pass
`urlx.CleanText` and may not contain the glyphs reserved for integrity elements
(✓ ✔ ☑ 🛡 and the app's badge code points; list in `gate/users/reserved.go`).

**`gate/graph`** (P0-W02 → P1-W03) — §4.1, §4.5, §4.6, §12. Implements `core.Graph`.
In-memory adjacency `[][]uint32` + parallel level/since arrays, `sync.RWMutex`, copy-on-write
rebuild of the touched rows (P0: full rebuild acceptable ≤5k users). Global `epoch` (atomic)
bumped on edge removal, block add and hide-via add. BFS: layered, dense `[]uint16 dist` +
`[]uint32 firstHop` from a `sync.Pool`, skipping the reader's **own** blocked users entirely and
(P1) the reader's hide-via users as transit only (D7).
**First-hop rule (D22, fidelity #10).** `rank(f)` for a first hop f is the tuple
`(level(V,f) == friend+ ? 0 : 1 /*P1; constant in P0*/, since(V,f), f)` — the reader's own
edge only. Layer 1 nodes label themselves. When relaxing u→w with `w` in `L_{k+1}`:
if `w` is unvisited, set `firstHop[w] = firstHop[u]`, `parent[w] = u`; if `w` is already in
`L_{k+1}` and `rank(firstHop[u]) < rank(firstHop[w])`, replace both; ties between parents with
the same first hop keep the lower parent ID. This is order-independent, so a plain FIFO
BFS yields the same labels. `Path` follows `parent`. Also exported for tests:
`ReferenceBFS(adj, v, ex) map[UserID]int`, `ReferenceFirstHop(adj, v, ex) map[UserID]UserID`
(enumerates all shortest paths, picks the best first hop — the rapid oracle), and
`Synth(n int, model string, seed int64) [][]uint32` (Barabási–Albert / Watts–Strogatz).
P1: lazy layered search + 30-min per-reader cache keyed by `(viewer, epoch)` +
one-deep-search-per-day, behind `PCC_LAZY_BFS=1`.

**`gate/visibility`** (P0-W02 → P1-W03) — §2.2, §4 rules, §10.1.3. Implements
`core.Visibility` and `core.Chains`. **Blocks are one-directional (D7, fidelity #1):** the
reader's search excludes only people the reader blocked (and hid-via), exactly §4 rule 2.
Being blocked by an author never hides that author's posts from you — that is what lets a
blocked person's reference become a shadow link and keeps the block invisible to them.
- `CanSeeUser(s, u)` ⇔ `s.Dist(u)` finite. Nothing else.
- `CheckPost(s, p)` ⇔ not deleted ∧ author status active ∧ `dist(V, author)` finite ∧
  audience allows it (`d1/d2/d3` ⇒ `dist ≤ limit`; `friend_plus` ⇒ `dist == 1 ∧ level ==
  friend+`; forced-probation posts are stored as `d1`, D6). Fails closed on any lookup error.
- `CanSeeLink` — §10.1.3 exactly: V sees C and P; not detached; not shadow unless V is C's
  author; and (P has no audience limit — `audience_forced` counts as one — **and** the default
  `ref_policy`) or C's author is V, P's author, or one of V's friends.
- `VisibleChildren(s, parent, f)` walks `PostIndex.Children` newest-first, keeps a link only
  if `CanSeeLink` and the filter hold, and stops after `f.Limit` **kept** links (so invisible
  children can never push visible ones out of the K window — fidelity #2).
- `CanReference(s, parent, kind)`: `cont` ⇒ only the parent's author, whatever the policy.
  `ref` ⇒ the parent's author may always reference their own post; otherwise `anyone` ⇒
  visible; `two_hops` ⇒ `dist ≤ 2`; `friends` ⇒ `dist ≤ 1`; `nobody` ⇒ false (fidelity #27).
- `Chain(s, u)`: first hop + author always; middle hops named only if every named edge has
  both endpoints `name_in_chains`, else `HiddenMiddle = dist − 2`.

**`gate/views`** (P0-W02 → P1-W12) — builds every `PostView`/`PersonView`/`AuthorView` from
`core.Checked` input only. Chains via `core.Chains`. Context rows: `VisibleParents` only,
excerpt = first 80 runes of the parent body with Markdown stripped (simple stripper).
Likes summary: `InNetwork` = |likers ∩ (L1 ∪ L2)| (includes the viewer; the author sees the
same rule), `Names` = viewer's friends among likers with `named_in_likes` (max 3, by edge
age). `ReplyCount` = `len(VisibleChildren(…, no limit))`. Previews shown only when
`status=ok ∧ now ≥ ready_at`. Mentions: `Ref` only if `CanSeeUser(target)`.
`AudienceForced` only on the author's own view. Display name always rendered with
`@handle` (the client never shows a display name alone).

**`gate/friends`** (P0-W03 → P1-W03) — §3, §4.6, §10.4, §10.5. Implements `core.Edges`, `core.Mutes`.
```go
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.PersonView], error)
func (s *Service) Requests(ctx context.Context) (incoming, outgoing []gate.PersonView, err error)
func (s *Service) Request(ctx context.Context, target gate.Ref) error
func (s *Service) Accept(ctx context.Context, from gate.Ref) error       // Friendship record, edges, edge_events, Graph.AddEdge, notify
func (s *Service) Decline(ctx context.Context, from gate.Ref) error
func (s *Service) Withdraw(ctx context.Context, to gate.Ref) error
func (s *Service) Unfriend(ctx context.Context, who gate.Ref) error     // Revocation record; purge_after on Friendship+Revocation; Invite untouched
func (s *Service) Block(ctx context.Context, who gate.Ref) error        // also unfriends; Block record; never revealed
func (s *Service) Unblock(ctx context.Context, who gate.Ref) error      // deletes the Block record and row (security #16)
func (s *Service) Blocks(ctx context.Context) ([]gate.PersonView, error) // blocked people stay addressable to the blocker only
func (s *Service) Mute(ctx context.Context, who gate.Ref, on bool) error
func (s *Service) Person(ctx context.Context, who gate.Ref) (gate.PersonView, error)
func (s *Service) PersonPosts(ctx context.Context, who gate.Ref, cursor string) (gate.Page[gate.PostView], error) // any age, visible only
func (s *Service) Suggestions(ctx context.Context) ([]Suggestion, error) // Suggestion{Person gate.PersonView; Mutual int}
func (s *Service) HideVia(ctx context.Context, who gate.Ref, on bool) error   // P1
func (s *Service) UpgradeBegin(ctx context.Context, who gate.Ref) (gate.HandshakeView, error) // P1 friend+ upgrade via core.Handshakes
```
Request semantics (fidelity #11, security #20): resolve the ref first (`ErrNotFound`); a
request to someone who has blocked the requester is stored as `declined` and returns exactly
what a fresh pending request returns (same bytes, same code path, same timing class); the
target is never notified. **Cap (D23):** a user's `edges + outgoing pending requests` may not
exceed 1,000 — checked on the caller's own counts only, at request time (`friend_cap`
Problem). Accept checks only the accepter's own edge count; the requester's slot was
reserved when they asked, so accepting can never push them over and never leaks their
degree. Requests/day: 20 (5 on probation). Suggestions: dist 2, ≥2 mutual friends through
edges ≥7 d old, `suggestible`, not blocked by the viewer, mutual *count* only.
Retention: job `friends.requests_scrub` nulls declined requests' timestamps after 30 d;
`graph.edge_events_purge` drops revoke rows after 30 d; `records.purge` removes the revoked
Friendship/Revocation records (`purge_after`).

**`gate/invites`** (P0-W04 → P1-W02) — §7, §8, §10.8. Implements `core.Invites`, `core.Probation`.
```go
func (s *Service) Budget(ctx context.Context) (gate.BudgetView, error)
func (s *Service) Invitees(ctx context.Context) ([]gate.InviteeView, error) // the inviter's own invitees + probation (§8)
func (s *Service) Open(ctx context.Context, mode string, quiet bool) (gate.HandshakeView, error)
//   Problems no_invites | on_probation | invites_paused | friend_cap (own edges+pending ≥ 1000); P0 accepts mode "qr" only
func (s *Service) Frame(ctx context.Context, hs gate.Ref) (gate.FrameView, error)
func (s *Service) Status(ctx context.Context, hs gate.Ref) (gate.HandshakeStatus, error)
func (s *Service) Confirm(ctx context.Context, hs gate.Ref) error            // inviter: "codes match" ⇒ state confirmed
func (s *Service) Cancel(ctx context.Context, hs gate.Ref) error             // inviter: "codes don't match" also cancels
func (s *Service) Claim(ctx context.Context, payload string) error           // guest session; every failure ⇒ ErrNotFound
func (s *Service) JoinStatus(ctx context.Context) (gate.HandshakeStatus, error) // invitee side, by guest session
func (s *Service) RegisterBegin(ctx context.Context, r Registration) (json.RawMessage, error)
//   Registration{Handle, DisplayName, Locale string; Languages []string; TOSVersion int; AgeAttested bool}
//   requires state == confirmed; Problems handle_invalid, handle_taken (≤10 per handshake, D21), tos_required, age_required
func (s *Service) RegisterFinish(ctx context.Context, body []byte) (sessToken string, me gate.MeView, err error)
```
**SAS confirmation (security #6).** After a claim, both screens show
`HandshakeStatus.SAS` (6 digits from `HMAC(secret, claimed_session_hash)`); the inviter taps
"codes match" (`Confirm`) before `RegisterBegin` is allowed, or "don't match" (`Cancel`). A
bystander who photographs the QR and claims first produces a code that differs from the one
on the real invitee's screen, so the inviter cancels and opens a new handshake.
Probation evaluation is internal: job `probation.evaluate` (daily) + a
`core.Subscribe[core.EdgeAdded]` hook call the unexported `evaluate(ctx, core.UserID)`; on
clearance it publishes `core.ProbationCleared`.
P0 frame: `pcc1.<hsid b64url>.<window>.<mac>` with `window = floor(unix/10)` and
`mac = HMAC-SHA256(secret, hsid‖window)[:10]` (base64url); accepted for the current and
previous window. Handshake sessions expire after 10 min; claimed sessions must finish
registration within 15 min.
Budget: token bucket `tokens += elapsed/24h` (× `diversity`, P1), capped at `cap` (3; genesis
5); countdown = time to next whole token. Probation P0: 7 days **and** ≥3 edges to users
outside the user's own invite subtree (subtree via recursive CTE over `users.invited_by`);
P1: 30 days and ≥3 **friend+** edges outside the subtree; accounts already mid-probation
at the P1 cut-over finish under the P0 rule (fidelity #32). Clearance is monotonic
(P1 quarantine can reset it through `core.Probation.Reset`). On clearance: `tokens = 1`.
Probation also forces post audience `d1` (D6) and engagement weight 0 (prefer reads it).
RegisterFinish transaction (serializable): re-check inviter budget and cap ⇒
`Users.Create` (`probation_until = now+7d`, ToS version, age attestation) ⇒ bind credential
⇒ `Invite` + `Friendship` records ⇒ `invites` row ⇒ `Edges.Create` ⇒ charge 1 token ⇒ hs
done ⇒ upgrade guest session (rotated token) ⇒ after commit: `Graph.AddEdge`, notify inviter
`invite_redeemed`, metric `invites.redeemed`. The client then offers an optional "add a
second passkey" step (§10.3, fidelity #34) using `AddPasskey*` (fresh by construction).
Handle rules: NFKC + lowercase → `^[a-z0-9_]{3,20}$`, reserved list (`admin`, `mod`,
`genesis`, `pcc`, …).

**`gate/posts`** (P0-W05 → P1-W12) — §10.1.1, §10.1.4 (`cont`), §10.1.5, §10.2, §10.6 mentions
```go
type Draft struct{ Body, Lang string; Audience, RefPolicy string; NoPreview bool; Parents []ParentIn; Media []gate.Ref /*P1*/ }
type ParentIn struct{ Ref gate.Ref; Kind string /* "ref"|"cont" */ }
func (s *Service) Create(ctx context.Context, d Draft) (gate.PostView, error)
func (s *Service) CreateThread(ctx context.Context, parts []Draft) ([]gate.PostView, error) // ≤25 parts; one write charge; part i+1 conts part i
func (s *Service) Get(ctx context.Context, p gate.Ref) (gate.PostView, error)
func (s *Service) Edit(ctx context.Context, p gate.Ref, body, lang string) (gate.PostView, error) // new version + record; previews/mentions rebuilt
func (s *Service) Delete(ctx context.Context, p gate.Ref) error         // Tombstone record; body + salt NULL; PostDeleted
func (s *Service) SetRefPolicy(ctx context.Context, p gate.Ref, pol string) (offerDetachAll bool, err error)
func (s *Service) Detach(ctx context.Context, parent, child gate.Ref) error
func (s *Service) DetachAll(ctx context.Context, parent gate.Ref) error
```
Order (plan §0.8): the write was already charged by `apix`; resolve every parent ref
(`ErrNotFound` if it does not open or `CheckPost` fails); then Problems: `body_empty`,
`body_too_long` (>20,000 code points), `lang_invalid`, `too_many_parents` (>3),
`duplicate_parent`, `cont_not_own`, `cont_taken`, `ref_not_allowed`, `ref_cap` (>2 refs to
the same post per rolling 24 h), `too_many_parts`. **Shadow links:** if the parent's author
has blocked the child's author (`Graph.Blocks(parentAuthor, childAuthor)`), the link is
accepted and stored `shadow=true`: no notification, no credit, visible only to the child's
author. (The reverse direction cannot occur: a reader never sees posts of people they
blocked.) Probation author ⇒ `audience=1, audience_forced=true`. Body NFC-normalized; each
version gets a fresh salt and signs `body_commit` (D3). After commit: `PostIndex.Put/PutLink`,
Bus `PostCreated`, enqueue `preview.fetch` (unless `no_preview`), notify parent authors (only
if they can see the child, child author within 2 hops of them by default, group key
`post_referenced:<parent>`, unless muted; `Notifier` drops it when the parent author blocked
the child author) and mentioned users (only if they can see the post).
Mentions: resolve each `@handle` with `Users.ByHandle` **only if the target is in the
author's horizon** (finite dist from the author's Search, or self); otherwise stored nowhere.

**`gate/postindex`** (P0-W05 → P1-W12) — §4.2 index. Implements `core.PostIndex`.
`recent[author]` slices (window = `cfg.FeedWindow`), meta map for window posts, LRU (50k) for
older metas, links loaded from `post_parents` on demand with LRU. `Load` on Init reads the
window; nightly `postindex.trim`.

**`gate/engage`** (P0-W06 → P1-W08) — §4.3 seen, §5.2 signals, §5.3
```go
func (s *Service) Like(ctx context.Context, p gate.Ref, on bool) error
func (s *Service) Save(ctx context.Context, p gate.Ref, on bool) error     // P1
func (s *Service) Saves(ctx context.Context, cursor string) (gate.Page[gate.PostView], error) // P1
func (s *Service) MarkSeen(ctx context.Context, ps []gate.Ref) error      // ≤50; invalid/invisible refs silently ignored; always nil
func (s *Service) RecentlySeen(ctx context.Context, cursor string) (gate.Page[gate.PostView], error)
func (s *Service) Hide(ctx context.Context, p gate.Ref) error
func (s *Service) ShowLess(ctx context.Context, p gate.Ref) error          // hides post + bumps show_less(author)
func (s *Service) Likers(ctx context.Context, p gate.Ref) (gate.LikeSummary, error)
```
Implements `core.Engagement` and `core.Prefer`. Likers kept as roaring bitmaps per post
(window posts in memory, LRU otherwise). **n (P0, §5.5):** Σ over likers u ∈ L1∪L2 of
`weight(u)·proximity(u)·min(4, signals(u))` with proximity 1.0 (L1) / 0.5 (L2), weight 0 for
the author and probation accounts, and all invitees of one inviter together capped at 4
units per post. P1 adds saves (+2) to `signals(u)`. **KidsN:** children from
`VisibleChildren(RefOnly, NotSelf, Limit 20)`; nil for slippery parents; P1 grandchildren
the same way with K² = 400 total. Seen events also feed the `feed.impressions_by_degree`
metric (tier of the post at the time it was seen; no IDs).

**`gate/rank`** (P0-W06 → P1-W08) — §5.1, §5.5, §5.6 — **pure** (no store import; scaffold writes the signatures)
```go
type Params struct{ Lambda, Beta float64; Tau, S, R, ShowLessStep time.Duration; ColdStart time.Duration; K int; AuthorCap bool }
func ParamsFor(p core.Preset) Params   // Latest{0}, Balanced{0.5}, Top{1}; τ=24h, S=λ·τ·ln4, β=0.3, R=12h, ShowLessStep=8h, cold 2h, K=20
type Candidate struct {
    Key [16]byte; Author uint32; CreatedAt time.Time; IsReply bool
    N float64; KidsN []float64 /* n_kid(C) per visible child, already including β·grandchildren in P1 */
    B float64 /* author baseline; 0 in P0/P1 */; FirstHopPlus bool; FirstHopSince time.Time; FirstHopID uint32
    ShowLess int /* the reader's show_less count for this author */
}
type Scored struct {
    Candidate; TEff time.Time; Shift, ReplyPenalty, ShowLessPenalty, Credit time.Duration; NEff float64; Reasons []string
}
func Score(p Params, now time.Time, c Candidate) Scored
// Less is THE comparator for feed tiers and tree rows (fidelity #15): TEff desc, then FirstHopPlus first,
// then older FirstHopSince, then lower FirstHopID, then Key asc.
func Less(a, b Scored) bool
// RankTier sorts with Less, then applies the author cap (≤2 per author in any 10 consecutive slots; off when
// Lambda==0). Output is a permutation of input.
func RankTier(p Params, now time.Time, cs []Candidate) []Scored
```
`t_eff = created + clamp(λ·τ·ln((1+n_eff)/(1+b)), −S, +S) − λ·R·is_reply − λ·min(ShowLess,3)·ShowLessStep`,
`n_eff = n + min(β·Σ KidsN, n)`; cold start floors the clamp term at 0 for posts younger
than 2 h. The show-less penalty (§5.2 "lowers that author *for you*", fidelity #14) sits
outside the clamp like the reply penalty, vanishes at λ=0, and is personal (the reader's own
signal), so it never leaks anything. Reasons: `chronological` (λ=0), `network_likes`
(shift>0), `below_author_usual` (shift<0), `reply_penalty`, `good_replies` (credit>0),
`cold_start`, `show_less`. Every component is logged at debug with `logx.UID` (§5.5).

**`gate/feed`** (P0-W07 → P1-W08) — §4.2, §4.3, §4.4, §5.6, §10.1.2, §10.1.7
```go
func (s *Service) Page(ctx context.Context, preset string, cursor string) (gate.FeedPage, error) // cursor "" ⇒ new session
func (s *Service) Expand(ctx context.Context, token string) ([]gate.FeedItem, error)            // collapsed group
func (s *Service) NewAbove(ctx context.Context, session string) (count int, tier int, err error)
func (s *Service) Why(ctx context.Context, p gate.Ref) (gate.WhyView, error)
```
Preset `top` in P0 ⇒ Problem `preset_unavailable` (§5.5; P1-W08 enables it).
Algorithm per session: `SearchFor(V)` (P0: full BFS) → for tier k = 1, 2, … *when first
reached*: `P_k = Σ_{a∈L_k} RecentLen(a)`; **author scan** (k-way merge of `Recent(a)`) if
`|L_k| + P_k ≤ W` else **time scan** (`WindowScan` filtered by `Dist(author)==k`); record
the choice (`feed.planner` metric + test hook). Filter: author ≠ V (D16), `CheckPost`, not
seen (14-day seen set), not hidden, author not muted; threads collapse to one candidate =
newest unseen visible part (earlier visible parts as context, no reply penalty for `cont`);
collapse at k ≥ 3 for authors younger than 60 days into one `collapsed` item per tier (§4.4).
Rank **all** candidates of the tier (`rank.RankTier`) → snapshot `[]PostID`.
**Snapshot store (security #2).** In-memory LRU keyed by `(viewer, session id)` where the
session id is 128 random bits; 30 min TTL, 5 sessions per user. Cursor =
base64url(`session|tier|offset` ‖ HMAC(CursorKey, viewer ‖ "feed" ‖ session ‖ tier ‖ offset)).
A cursor, session or collapse token that fails the HMAC or belongs to another viewer ⇒
`ErrNotFound`; the viewer's own expired session ⇒ `feed_session_expired` (409).
**Freshness (fidelity #4).** Every page re-runs `SearchFor(V)` and `CheckPost` (and, for
context rows and chips, `CanSeeLink`) on the snapshot slice it is about to serve and silently
drops failures, so unfriend, block, deletion and audience changes take effect on the next
request (§4.6). Ordering is not recomputed; only items disappear. `More`/counts are computed
after the re-check.
Page = 20 posts; emits `tier_start` before the first item of a tier, `tier_end` after its
last, `caught_up` at the very end; empty tiers emit nothing. Read budget `feed_page` per call.
Metrics: `feed.tier_reached` (deepest tier served per session), `feed.latency_ms`,
`feed.preset_switch`, `feed.planner`. No `feed_new` SSE event (fidelity #24): the client
polls `NewAbove` every 60 s while the feed is visible. p95 target: < 50 ms at 10k synthetic users.

**`gate/tree`** (P0-W08 → P1-W08) — §10.1.4, §10.1.3
```go
func (s *Service) Open(ctx context.Context, focal gate.Ref, preset string) (gate.TreeView, error)
func (s *Service) Row(ctx context.Context, snapshot string, gen int, center gate.Ref) (gate.TreeRow, error) // swipe re-center / load more rows
func (s *Service) More(ctx context.Context, snapshot string, gen int, center gate.Ref, cursor string) (gate.TreeRow, error)
```
Rows: −1 = `VisibleParents` of focal (ranked); 0 = siblings under the *primary parent*
(highest-ranked visible parent) with focal centered (no sibling row if no visible parent);
+1 = `VisibleChildren` of focal; +2 = children of row +1's center; −2 = parents of row −1's
center. Each row ranks every visible candidate by (tier asc, then `rank.Less` under the
reader's preset — the same tie-breaks as the feed), loads bodies for the top 10, `More` =
remaining visible count. Each post appears once (later spots get `ShownAbove`). Traversal
never passes through invisible posts; a visible post whose parents are all invisible looks
like a root. Snapshots are stored and keyed exactly like feed snapshots (`(viewer, id)`,
HMAC'd cursors) and re-checked on every `Row`/`More`. Read budget `tree_view` per call.

**`gate/previews`** (P0-W09 → P1-W11) — §10.1.6
Job `preview.fetch{post}`: for each `mdscan` link (max 3 previews, skip `<url>`):
`urlx.Canonicalize` → cache hit (`link_fetch_cache`, TTL 1 h) or per-host limiter
(1 req / 2 s / host, burst 3) → `safefetch.GetHTML` → parse `og:title|twitter:title|<title>`,
`og:description|meta description` → `urlx.CleanText` (title ≤ 200 runes, description ≤ 200
runes) → write `link_previews`. **Timing (fidelity #23):** `ready_at` is always a fixed bucket:
`created_at + PreviewDelay`; if the fetch has not finished by then, `ready_at` moves once to
`created_at + 2·PreviewDelay`; if it is still not finished, the row becomes `status=2` and is
never shown. The completion time itself is never observable. Failures ⇒ `status=2`, never
retried more than 2×. Client-supplied preview data is never accepted (no API exists for it).
Subscribes `PostDeleted` to drop its rows.

**`gate/notify`** (P0-W10 → P1-W05) — §10.6. Implements `core.Notifier`.
```go
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.NotificationView], error)
func (s *Service) MarkRead(ctx context.Context, upTo gate.Ref) error
func (s *Service) UnreadCount(ctx context.Context) (int, error)
func (s *Service) Stream(ctx context.Context) (<-chan StreamEvent, func(), error) // StreamEvent{Type string; Data any}
func (s *Service) MutePost(ctx context.Context, p gate.Ref, on bool) error
func (s *Service) PushKey(ctx context.Context) (string, bool)                      // P1
func (s *Service) Subscribe(ctx context.Context, sub gate.PushSub) error           // P1; existing endpoint ⇒ reassigned to this user/session
func (s *Service) Unsubscribe(ctx context.Context, endpoint string) error          // P1
```
Batching: `group_key` rows upsert `count = count+1`; batched kinds (`post_referenced`) emit at
most one SSE ping per group per hour (`notify.flush`). At read time subjects are re-checked
for visibility and encoded as refs for the recipient; invisible subjects are dropped from the
view (row kept). Events whose recipient has blocked the subject user are dropped at
`Notify` time. The SSE hub closes a stream on `SessionEnded` for that session (security #12).

**`gate/metrics`** (P0-W10) — §13 product metrics, §18 DoD measurement (fidelity #6).
Implements `core.Metrics`: in-memory counters per `(day, name, bucket)`, flushed to
`metrics_daily` every minute by `metrics.flush`. Closed name list (`names.go`):
`feed.tier_reached{1..6,7+}`, `feed.impressions_by_degree{1..6,7+}` (from seen events),
`invites.redeemed`, `feed.preset_switch{latest,balanced,top}`, `feed.latency_ms{bucket}`,
`feed.planner{author,time}`, `graph.deep_search{count}` (P1), `tree.opened`. A name or bucket
outside the list panics in tests and is dropped in prod. No user ID, ref, edge or IP can be
recorded — the API takes only names, buckets and numbers. `Report(ctx, days int)` backs
`pcc metrics` (moderator/admin CLI; p95 latency computed from the buckets).

**`gate/records`** (P0-W11 → P1-W01) — §3 signed records, §10.2 export. Implements `core.Signer`.
```go
func (s *Service) Keys(ctx context.Context) ([]gate.KeyView, error)            // public keys + validity + fingerprints
func (s *Service) RotateServerKey(ctx context.Context) (string, error)         // CLI
func (s *Service) AuditEdges(ctx context.Context) (bad int, err error)         // CLI + weekly job (§4.5, fidelity #31)
func VerifyExport(r io.ReaderAt, size int64, pinned map[string]string) (gate.VerifyReport, error) // offline verifier (also CLI)
```
Record bodies (schema_version 1; value grammar of plan §4.2 sigs; users are `ext_id` UUIDs;
every body carries `kind` and `schema_version`):
`Invite{inviter, invitee, issued_at, handshake, invitee_credential?}`,
`Friendship{a, b, level, created_at, handshake?}` (a<b),
`Revocation{edge:{a,b}, by, at}`,
`Post{id, version, author, body_commit, lang, media:[], audience?, parents:[{id,kind}], ref_policy?, no_preview?, created_at}`,
`Tombstone{target, by, at}`, `Block{blocker, blocked, at}` (party: blocker only; deleted on unblock),
`Report{…}` (moderation-only, never exported to the target),
`PasskeyChange{user, op: "add"|"remove", credential_id, public_key (COSE), alg, at}` — the public
key is inside the signed body so exported assertions verify offline (security #5); from P1
every `add` after the first is co-signed by an existing credential, giving a chain back to
the first credential, which is itself named in the `Invite` (or genesis enrollment) record.
`ModAction{action, reason, at, second?}` (P1 two-person draft).
Jobs: `records.purge` (rows past `purge_after`), `records.audit_edges` (weekly).

**`gate/account`** (P0-W11 → P1-W10) — §10.2, §10.3, §10.7 settings, §2.3 retention.
```go
func (s *Service) Me(ctx context.Context) (gate.MeView, error)
func (s *Service) UpdateSettings(ctx context.Context, p gate.SettingsPatch) (gate.SettingsView, error)
//   Problems: locale_invalid, lang_invalid, translate_to_not_in_languages, ui_prefs_invalid, preset_unavailable (top in P0),
//   display_name_invalid
func (s *Service) Export(ctx context.Context, w io.Writer) error   // RequireFresh; zip: records.jsonl, keys.json, posts.jsonl
                                                                   // ({id, version, body, salt}), credentials.jsonl, manifest.json
func (s *Service) Delete(ctx context.Context, confirmHandle string) error // RequireFresh; enqueues account.delete; logs out everywhere
func (s *Service) RecoveryRequest(ctx context.Context, subject gate.Ref) (id gate.Ref, err error) // moderator 1 (moderator-scoped ref)
func (s *Service) RecoveryApprove(ctx context.Context, id gate.Ref) error                         // moderator 2 ≠ 1, RequireFresh; starts 24 h hold
func (s *Service) RecoveryLink(ctx context.Context, id gate.Ref) (enrollURL string, err error)    // moderator 2 only, after release
func (s *Service) RecoveryCancel(ctx context.Context) error                                       // any live session of the account
func (s *Service) RecoveryPending(ctx context.Context) (*time.Time, error)                        // the account's own banner
```
Manual recovery (security #14): two distinct moderators, each with a fresh step-up; on
approval the account's live sessions show a banner with **Cancel**, friends get
`friend_security`, and the enrollment token becomes usable only at `release_at = approval +
24 h` (`recovery.release`). On enrollment all sessions are revoked, existing passkeys are
listed and can be removed in the same flow. Both moderators are recorded in `mod_actions`
(`recovery_approve`, with `second_auth`).
`account.delete` job: tombstone every post, revoke every edge (`core.Edges.Revoke`), publish
`AccountDeleted` (owners delete engagements/seen/hides/sessions/credentials/push subs), status
3, keep `handle_canon` reserved, write `account_stubs`, notify friends `friend_security`.
Invitees keep their label (`kind: "deleted"`).

**`gate/limits`** (P0-W12 → P1-W07) — §17.2(3), §17.4. Implements `core.Limiter`.
Defaults (configurable): reads/day — `feed_page` 400, `tree_view` 400, `post_read` 1500,
`graph_read` 200, P1 `media_full` 60; writes — `post` 30/day & 10/h, `like` 100/h,
`friend_request` 20/day (5 probation), `report` 20/day, `social` (block/mute/detach/hide/
show-less/notify-mute/ref-policy) 200/day, `account` (passkeys/settings/export/delete) 50/day,
`invite_handshake` 10/day; min spacing 2 s between writes of one kind. `reference` (2 per
parent per 24 h) is enforced in posts after ref resolution — it is a Problem about the
caller's own repeated action on a post it can already see. Anonymous: 30/min per /24 (v4) or
/56 (v6) for login/join/enroll/csp-report. **Every charge happens in `apix.Route` before the
handler runs and before any ref is resolved, for reads and writes alike** (fidelity #7,
security #17); exhaustion ⇒ `ErrRateLimited` (uniform 429 with `retry_after_s` derived only
from the viewer's own bucket). Storage: `rate_counters` upsert; in-memory write-through
cache keyed `(user,bucket,window)`. Anonymous counters are **memory only** (no IPs at rest).
Handler-facing API (A5-clean; `apix` calls it through `apix.Charger`):
```go
func (s *Service) ChargeRequest(ctx context.Context, class gate.Class) error // read or write class; viewer from ctx; unauthenticated ⇒ ChargeAnon by RealIP
```

**`gate/moderation`** (P0-W12 → P1-W06) — §8 quarantine, §10.4
```go
func (s *Service) Report(ctx context.Context, kind string, target gate.Ref, reason, note string) error // kind: post|person|inviter|preview
func (s *Service) Queue(ctx context.Context, cursor string) (gate.Page[gate.ModItem], error)   // moderators
func (s *Service) Resolve(ctx context.Context, item gate.Ref, action, reason string) error
//   remove_post | dismiss: one moderator. suspend_user (and P1 quarantine): creates a state-0 mod_action that a
//   SECOND moderator must confirm within 24 h via Confirm, each with RequireFresh (P1: assertion over the JCS draft).
func (s *Service) Confirm(ctx context.Context, item gate.Ref) error
func (s *Service) BlameInbox(ctx context.Context, cursor string) (gate.Page[gate.BlameItem], error)
func (s *Service) Log(ctx context.Context, cursor string) (gate.Page[gate.LogItem], error)  // pseudonym = HMAC(PseudonymKey, user)[:6] unless in viewer's horizon
func (s *Service) GrantModerator(ctx context.Context, handle, by string, bootstrap bool) error // CLI
func (s *Service) CSPReport(ctx context.Context, directive string) error                   // aggregate count per directive only
```
`reason` and `action` are closed enums (`core.ModReason`, DB CHECKs, fidelity #18).
Moderators see reported content and the involved handles (moderator-scoped refs), the
subject's inviter label and probation flag (§8: probation visible to moderators), never graph
data beyond that. After a confirmed action, the author's inviter gets a `blame_inbox` row +
`invitee_action` notification; the post is included only if the inviter could already see it.

**`gate/devseed`** (P0-W02) — synthetic graphs/posts for dev + perf tests and the named e2e
fixtures `testdata/fixtures/<wp-id>.json` (each WP owns its own fixture file; plan §7);
refuses unless `cfg.DevLike()`.

**P1 domain packages**
- **`gate/cosign`** (P1-W01): implements `core.Cosign`: intents for co-signed records (§3
  Phase 1). `Begin(ctx, purpose string, target gate.Ref) (Intent, error)` →
  `Intent{ID gate.Ref; Record json.RawMessage /* shown to user */; Options json.RawMessage /*
  WebAuthn get via Passkeys.BeginAssertion, challenge = sigs.Challenge over a draft containing
  nonce + issued_at */}`; `Require(ctx, v, purpose, kind, body, intent, assertion)` verifies
  through `Passkeys.VerifyAssertion` (viewer's own credential, exact origin, UV) and consumes
  the intent **in the caller's transaction**. Per-user custodial Ed25519 keys created at
  signup, wrapped with AES-256-GCM under `KEK` with AAD `key_id ‖ owner_id` (security #29);
  `records` signs with the author key + server countersign. **Single owner of the rule
  "graph-changing writes need an assertion" (buildability C4):** callers are friends
  (P1-W03: request/accept/unfriend/block), invites (P1-W02: both sides at signup), auth
  (P1-W07: passkey add/remove), account (P1-W10: delete, recovery approve), moderation
  (P1-W06: second-moderator approvals).
- **`gate/media`** (P1-W04): `Upload(ctx, r io.Reader, size int64, mime, alt string)
  (gate.MediaView, error)` — limit 15 MB, write class `media` 50/day; `image.DecodeConfig`
  bound check; scan **before** any storage write; match ⇒ evidence store (1-year retention) +
  `csam_holds` + `mod_queue` priority 0 + account held for review (uploads blocked) + uniform
  `media_rejected`; else decode → thumb 320 / display 1080 / original re-encode (WebP), random
  128-bit keys. `Ingest(ctx, img, sourcePost, sourceURL)` (preview images): the **same** match
  path — `csam_holds` with `source_url`, priority-0 item, the linking post held — never
  "dropped silently" (security #7). `URL(ctx, m gate.Ref, variant string) (url string, exp
  time.Time, err error)` — gate checks the viewer can see a post containing it (or owns it);
  URL = `MediaBase/<key>?e=<unix>&v=<viewerTag>&s=<sig>`, `sig = HMAC(MediaURLKey, key(26 B
  ascii) ‖ e(8 B big-endian) ‖ v(6 B))` (fixed-width input, security #27), 1 h expiry,
  `viewerTag = HMAC(k, viewer)[:6]`. Dev/test: Go serves `/m/<key>` and verifies the HMAC;
  prod: Cloudflare Worker (`deploy/cloudflare/media-worker.js`) validates the HMAC **before**
  the cache lookup, cache key excludes `s`,`v`; the media origin sends `nosniff`,
  `Content-Security-Policy: sandbox; default-src 'none'`, `Content-Disposition: inline`.
  Deleting media purges the CDN key. `original` charges `media_full`. Moderators see only
  `match_info`; viewing held content is a two-person `break_glass` action.
- **`gate/signals/behavior`** (P1-W06): nightly cadence / similarity (MinHash of post bodies
  within invite subtrees; bodies never stored) / invite-cluster jobs ⇒ `mod_queue`;
  cross-account correlation on `net_h` only (HMAC of the /24 or /56 under a daily salt,
  30-day retention — no IPs at rest).
- **`gate/signals/automation`** (P1-W07): `Client(ctx, report ClientSignals) error`
  (webdriver, cdp hint, input-event counts per minute — counts only); request-pattern
  scorer fed by an `apix` hook (sustained rate without idle, round-the-clock, reads without
  seen, BFS-like tree walks, chain expansion speed). Threshold crossing ⇒
  `sessions.stepup_required` ⇒ `ErrStepUp` on reads. Signals never ban; screen-reader /
  keyboard-only profiles are explicitly never scored (input events of any kind count as
  input; no mouse-movement signals).
- **`gate/themes`** (P1-W09): token-only themes in `user_themes`, validated server-side
  (WCAG AA for integrity elements, size floor, font allowlist).

---
## 5. Internal HTTP API (§16.1 — first-party only, unpublished)

### 5.1 Conventions

- Base path `/api`. JSON only (`Content-Type: application/json`). Request decoding uses
  `DisallowUnknownFields`. **Per-route body limits** (security, low): default 16 KiB;
  `POST /posts` and `PATCH /posts/{ref}` 96 KiB (20,000 code points can be ~80 KB);
  `POST /posts/thread` 2.5 MiB; WebAuthn finishes 64 KiB; P1 `POST /media` 15 MiB multipart.
- `/api/*` requests need `Sec-Fetch-Site` ∈ {`same-origin`, absent}; unsafe methods also need
  `Origin == cfg.Origin` (authenticated or not) and, when a session exists, `X-PCC-CSRF`.
  Otherwise: uniform not-found. These checks never apply to SPA navigations. There is no
  CORS, no bearer auth, no webhooks. Exception (INT-0): `POST /api/csp-report` is sent by the
  browser's Reporting API, so it needs no `X-PCC-CSRF` and accepts an absent `Origin` (a
  present one must match; `Sec-Fetch-Site` as above).
- Responses (all via `apix`):

| Gate result | HTTP | Body |
|---|---|---|
| view | 200 (201 for creates, 204 for empty) | the view |
| `gate.ErrNotFound` (and unauthenticated, wrong origin, bad CSRF, bad ref, invisible target) | **404** | `{"error":"not_found"}` |
| `*gate.Problem` | `p.Status` (400/409/422) | `{"error":"<code>","field":"<name>"}` |
| `gate.ErrRateLimited` | 429 | `{"error":"rate_limited","retry_after_s":N}` |
| `gate.ErrStepUp` | 401 | `{"error":"step_up_required"}` |
| `gate.ErrDisabled` | 409 | `{"error":"feature_disabled","feature":"media"}` |
| anything else | 500 | `{"error":"internal"}` (logged with request id) |

- **Charging (D19, extended).** Every route declares one class (archtest A12). Read classes
  **F** feed_page, **T** tree_view, **G** graph_read, **R** post_read, **M** media_full (P1);
  write classes **Wp** post, **Wl** like, **Wf** friend_request, **Wr** report, **Ws** social,
  **Wa** account, **Wi** invite_handshake, **Wv** save (P1), **Wm** media (P1). `apix.Route`
  charges before the handler and before any ref is resolved, so a valid ref and garbage cost
  exactly the same (fidelity #7). `—` marks the reviewed free routes.
- **P1 timing padding (fidelity #8, security #19).** The padded set is not hand-picked:
  `apix` pads **every** route whose pattern has a `{ref}`/`{hs}`/`{id}`/`{snapshot}` param
  or whose request schema contains a ref field (computed from the route catalogue +
  OpenAPI at startup; listed by `router_test`). Padding rounds the total handler time **up to
  the next multiple** of the route's bucket (150 ms default; 600 ms for deep-tier feed/tree
  work), for success, Problem and 404 alike, inside the same middleware. KS test plus a
  server-side histogram assertion (found vs not-found land in the same bucket) in INT-1.
- Pagination: `?cursor=`; responses `{items, next}`.

### 5.2 Route registration (no shared router edits)

```go
// internal/api/apix (SCAFFOLD)
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error
type Class = gate.Class   // gate.Class{Kind byte /* 'r','w','-' */; Name string}, defined in gate (plan §4.4) so limits needn't import api
func Read(name string) Class; func Write(name string) Class; var Free Class   // apix.Read("feed_page"), apix.Write("like"), apix.Free
type Charger interface{ ChargeRequest(ctx context.Context, c gate.Class) error } // = *limits.Service
func WithCharger(c Charger) func(http.Handler) http.Handler     // router installs it once; Route reads it from ctx
func Route(r chi.Router, method, pattern string, c Class, h HandlerFunc) // pattern RELATIVE to /api, e.g. "/feed"
func Decode[T any](r *http.Request, max int64) (T, error)       // → *gate.Problem{400,"bad_request"}
func JSON(w http.ResponseWriter, status int, v any) error
func Pad(bucket time.Duration) func(http.Handler) http.Handler  // P1; installed by Route for padded routes
func Ref(r *http.Request, name string) gate.Ref                 // chi URL param as gate.Ref

// internal/api/deps.go (SCAFFOLD)
type Deps struct {
    Cfg *config.Config; Log *slog.Logger
    Auth *auth.Service; Friends *friends.Service; Invites *invites.Service; Posts *posts.Service
    Engage *engage.Service; Feed *feed.Service; Tree *tree.Service; Notify *notify.Service
    Records *records.Service; Account *account.Service; Limits *limits.Service; Moderation *moderation.Service
    // P1: Media *media.Service; Automation *automation.Service; Cosign *cosign.Service; Themes *themes.Service
}

// each module: internal/api/<mod>api/routes.go — owned by the module's WP
func Register(r chi.Router, d *api.Deps)

// internal/api/router/router.go (SCAFFOLD; edited once more by SCAFFOLD-1)
func New(d *api.Deps, static fs.FS) http.Handler {
    r := chi.NewRouter()
    r.Use(httpx.RequestID, httpx.AccessLog, httpx.Recover, secheaders.Middleware(...), d.Auth.Middleware)
    staticapi.Register(r, d)                // /robots.txt, /ai.txt, /.well-known/tdmrep.json, SPA fallback
    r.Route("/api", func(r chi.Router) {
        r.Use(d.Auth.APIGuard, apix.NoStore, apix.WithCharger(d.Limits))
        authapi.Register(r, d); invitesapi.Register(r, d); friendsapi.Register(r, d)
        postsapi.Register(r, d); engageapi.Register(r, d); feedapi.Register(r, d)
        treeapi.Register(r, d); notifyapi.Register(r, d); accountapi.Register(r, d); modapi.Register(r, d)
        testhooks.Register(r, d)            // no-op unless Env == test && TestHooks
        // P1 (SCAFFOLD-1): mediaapi, pushapi, signalsapi, cosignapi, themeapi
        r.NotFound(apix.NotFound)           // unknown /api path ⇒ same uniform 404
    })
    return r
}
```
The scaffold writes every `Register` as an empty function (except the scaffold-owned working
`/session`, `/auth/login/*` stubs that W01 then owns), so the tree compiles from minute one;
each WP fills its own `routes.go` + `handlers_*.go`.

Registration rules (several modules share prefixes such as `/posts/{ref}/…`):
- Register **flat patterns relative to the `/api` router you are given** (`"/feed"`,
  `"/posts/{ref}/like"`) with `apix.Route`. The catalogue below shows full paths for
  readability. Never call `r.Route`, `r.Mount` or `r.Group` with a shared prefix (chi panics
  on duplicate mounts; two WPs would collide at runtime).
- Path-parameter names are fixed per position so chi's tree never sees two names for one
  segment: `{ref}` (any person/post ref), `{hs}` (handshake), `{id}` (notification,
  passkey, mod item, recovery request), `{snapshot}`, `{key}` (media blob). The
  catalogue below is the complete list; a new path is a steward request only if it breaks
  these rules.
- `internal/api/router/router_test.go` (SCAFFOLD) walks `chi.Walk` and asserts that every
  route in the catalogue is registered exactly once and has a class (INT-0 flips it from
  "skip missing" to "require all"). OpenAPI: each WP owns `api/openapi/<module>.yaml`
  (paths + module-prefixed schemas; `$ref`s into `_common.yaml`), validated on its own by
  `apitest` so one WP's broken fragment cannot break another WP's tests.

### 5.3 Endpoint catalogue

Shapes use the view types from plan §4.4. Class column per §5.1. ⏱ = padded in P1 (derived
automatically, listed here for review). `P1` marks Phase 1 additions.

**authapi** (P0-W01 → P1-W07)
| Method & path | Class | Request | Response |
|---|---|---|---|
| GET `/api/session` | — | — | `SessionView` (works unauthenticated: `{authenticated:false, kind:"none"}`) |
| POST `/api/auth/login/begin` | anon | `{}` | `{options}` + guest cookie holding the ceremony |
| POST `/api/auth/login/finish` | anon | `{credential}` | `SessionView` + new cookie (failure ⇒ 404 uniform) |
| POST `/api/auth/logout` | — | — | 204 |
| POST `/api/auth/stepup/begin` | Wa | `{}` | `{options}` (allowCredentials = own credentials) |
| POST `/api/auth/stepup/finish` | Wa | `{credential}` | 204 |
| GET `/api/auth/passkeys` | G | — | `{items:[{id,name,created_at,last_used_at,current}]}` |
| POST `/api/auth/passkeys/begin` | Wa | `{}` | `{options}` · `step_up_required` (P1: needs `cosign` intent `passkey_add`) |
| POST `/api/auth/passkeys/finish` | Wa | `{credential, name}` | 204 · `step_up_required` |
| DELETE `/api/auth/passkeys/{id}` ⏱ | Wa | — | 204 · `last_passkey` · `step_up_required` |
| POST `/api/enroll/begin` | anon | `{token}` (from the `#fragment`) | `{options, purpose, existing_passkeys?}` (token invalid ⇒ 404) |
| POST `/api/enroll/finish` | anon | `{credential, remove_old?, tos_version?, age_attested?}` | `SessionView` + cookie |
| P1 POST `/api/auth/dbsc/register` | anon | DBSC JWT (header `Sec-Session-Response`) | 200 + session config JSON per DBSC spec |
| P1 POST `/api/auth/dbsc/refresh` | anon | DBSC JWT | 200 + new short-lived bound cookie |

**invitesapi** (P0-W04 → P1-W02)
| Method & path | Class | Request | Response |
|---|---|---|---|
| GET `/api/invites/budget` | G | — | `BudgetView` |
| GET `/api/invites/invitees` | G | — | `{items:[InviteeView]}` |
| POST `/api/invites/handshakes` | Wi | `{mode?:"qr"\|"qr_audio"\|"dual_qr", quiet?:bool}` | 201 `HandshakeView` · `no_invites`, `on_probation`, `invites_paused`, `friend_cap` |
| GET `/api/invites/handshakes/{hs}/frame` ⏱ | — | — | `FrameView` (P0 rotating 10 s) |
| GET `/api/invites/handshakes/{hs}` ⏱ | — | — | `HandshakeStatus` (also pushed over SSE `handshake`) |
| POST `/api/invites/handshakes/{hs}/confirm` ⏱ | Wi | — | 204 (SAS matched) |
| DELETE `/api/invites/handshakes/{hs}` ⏱ | Wi | — | 204 |
| POST `/api/join/start` | anon | — | `SessionView` (guest session cookie) |
| POST `/api/join/claim` ⏱ | anon | `{payload}` (+P1 `e_v`, `sig_pub`, `proof`) | 204 · any failure ⇒ 404 |
| GET `/api/join/status` | anon | — | `HandshakeStatus` (invitee side: SAS + state) |
| POST `/api/join/register/begin` | anon | `{handle, display_name, locale, languages, tos_version, age_attested}` | `{options}` · `handle_invalid`, `handle_taken`, `tos_required`, `age_required` |
| POST `/api/join/register/finish` | anon | `{credential}` | 201 `SessionView` + rotated cookie |
| P1 POST `/api/invites/handshakes/{hs}/epub` ⏱ | Wi | `{e_i}` (inviter device) | 204 |
| P1 GET `/api/invites/handshakes/{hs}/peer` ⏱ | — | — | `{e_v}` once claimed |
| P1 POST `/api/join/audio/start` | anon | `{kmac, sig}` (invitee: about to play; signed over (hs, challenge)) | `{play_at, challenge}` |
| P1 POST `/api/invites/handshakes/{hs}/report` ⏱ | Wi | `{ultra_bits?, jingle_bits?, kmac_commit, t_ultra_ms?, t_jingle_ms?, offset_ms?}` | `{outcome:"verified"\|"accepted"\|"retry", hint?:"volume_up"\|"try_jingle"}` |
| P1 POST `/api/join/dual/frame` | anon | `{payload, sig}` (accessibility dual-QR) | `{outcome}` |

**friendsapi** (P0-W03 → P1-W03)
| Method & path | Class | Request → Response |
|---|---|---|
| GET `/api/friends` | G | → `Page<PersonView>` |
| GET `/api/friends/requests` | G | → `{incoming:[PersonView], outgoing:[PersonView]}` |
| POST `/api/people/{ref}/request` ⏱ | Wf | → 202 `{state:"outgoing"}` · `friend_cap` (own count only) |
| POST `/api/friends/requests/{ref}/accept` ⏱ | Wf | → 204 · `friend_cap` (own) (P1 `{intent, assertion}`) |
| POST `/api/friends/requests/{ref}/decline` ⏱ | Ws | → 204 |
| DELETE `/api/people/{ref}/request` ⏱ | Ws | → 204 (withdraw) |
| DELETE `/api/friends/{ref}` ⏱ | Ws | → 204 (unfriend; P1 `{intent, assertion}`) |
| PUT / DELETE `/api/people/{ref}/block` ⏱ | Ws | → 204 (P1 `{intent, assertion}`) |
| GET `/api/blocks` | G | → `{items:[PersonView]}` |
| PUT / DELETE `/api/people/{ref}/mute` ⏱ | Ws | → 204 |
| GET `/api/people/{ref}` ⏱ | G | → `PersonView` |
| GET `/api/people/{ref}/posts` ⏱ | R | → `Page<PostView>` |
| GET `/api/suggestions` | G | → `{items:[{person:PersonView, mutual:int}]}` |
| P1 PUT / DELETE `/api/people/{ref}/hide-via` ⏱ | Ws | → 204 |
| P1 POST `/api/friends/{ref}/upgrade` ⏱ | Wi | → 201 `HandshakeView` (friend+ handshake; same protocol as invites) |

**postsapi** (P0-W05 → P1-W12)
| Method & path | Class | Request → Response |
|---|---|---|
| POST `/api/posts` ⏱ | Wp | `{body, lang, audience?, ref_policy?, no_preview?, parents?:[{ref,kind}], media?:[ref](P1)}` → 201 `PostView` |
| POST `/api/posts/thread` ⏱ | Wp | `{parts:[{body, lang}], audience?, ref_policy?, no_preview?, parents?}` → 201 `{items:[PostView]}` |
| GET `/api/posts/{ref}` ⏱ | R | → `PostView` |
| PATCH `/api/posts/{ref}` ⏱ | Wp | `{body, lang?}` → `PostView` |
| DELETE `/api/posts/{ref}` ⏱ | Wp | → 204 |
| PUT `/api/posts/{ref}/ref-policy` ⏱ | Ws | `{ref_policy}` → **200** `{offer_detach_all: bool}` (true when switching to `nobody` with existing children) |
| POST `/api/posts/{ref}/detach` ⏱ | Ws | `{child}` → 204 |
| POST `/api/posts/{ref}/detach-all` ⏱ | Ws | → 204 |

**engageapi** (P0-W06 → P1-W08)
| Method & path | Class | Request → Response |
|---|---|---|
| PUT / DELETE `/api/posts/{ref}/like` ⏱ | Wl | → `{likes: LikeSummary}` |
| GET `/api/posts/{ref}/likes` ⏱ | R | → `LikeSummary` |
| POST `/api/seen` ⏱ | — | `{posts:[ref]}` (≤50) → 204 always |
| GET `/api/seen/recent` | F | → `Page<PostView>` |
| POST `/api/posts/{ref}/hide` ⏱ | Ws | → 204 |
| POST `/api/posts/{ref}/show-less` ⏱ | Ws | → 204 |
| P1 PUT / DELETE `/api/posts/{ref}/save` ⏱ | Wv | → 204 |
| P1 GET `/api/saves` | F | → `Page<PostView>` |

**feedapi** (P0-W07 → P1-W08)
| Method & path | Class | Request → Response |
|---|---|---|
| GET `/api/feed?preset=&cursor=` ⏱ | F | → `FeedPage` · `feed_session_expired` (409, client restarts) · `preset_unavailable` (P0 `top`) |
| GET `/api/feed/expand?token=` ⏱ | F | → `{items:[FeedItem]}` |
| GET `/api/feed/new?session=` ⏱ | F | → `{count, tier}` (polled every 60 s while visible) |
| GET `/api/posts/{ref}/why` ⏱ | R | → `WhyView` |

**treeapi** (P0-W08 → P1-W08)
| Method & path | Class | Request → Response |
|---|---|---|
| GET `/api/tree/{ref}?preset=` ⏱ | T | → `TreeView` |
| GET `/api/tree/s/{snapshot}/row?gen=&center=` ⏱ | T | → `TreeRow` |
| GET `/api/tree/s/{snapshot}/more?gen=&center=&cursor=` ⏱ | T | → `TreeRow` |

**notifyapi** (P0-W10 → P1-W05)
| Method & path | Class | Request → Response |
|---|---|---|
| GET `/api/notifications?cursor=` | G | → `Page<NotificationView>` + `unread` |
| POST `/api/notifications/read` | — | `{up_to}` → 204 |
| PUT / DELETE `/api/posts/{ref}/notify-mute` ⏱ | Ws | → 204 |
| GET `/api/events` | — | `text/event-stream`; events `hello{unread}`, `notification{unread}`, `handshake{hs,state}`, `stepup{}`(P1); `: ping` every 25 s; closed on `SessionEnded` |
| P1 GET `/api/push/key` | — | → `{enabled, key?}` |
| P1 POST `/api/push/subscriptions` | Wa | `PushSub` → 204 · `feature_disabled` |
| P1 DELETE `/api/push/subscriptions` | Wa | `{endpoint}` → 204 |

**accountapi** (P0-W11 → P1-W10)
| Method & path | Class | Request → Response |
|---|---|---|
| GET `/api/me` | — | → `MeView` |
| PATCH `/api/me/settings` | Wa | `SettingsPatch` → `SettingsView` |
| POST `/api/me/export` | Wa | `{}` → `application/zip` (`Content-Disposition: attachment`) · `step_up_required` |
| POST `/api/me/delete` | Wa | `{confirm_handle}` (P1 + `{intent, assertion}`) → 202 · `step_up_required` |
| GET `/api/me/recovery` | — | → `{pending_until?}` (banner) |
| POST `/api/me/recovery/cancel` | Wa | → 204 |
| GET `/api/keys` | G | → `{server:[KeyView], mine?(P1)}` |
| POST `/api/mod/recovery` (moderators) | Wa | `{subject: ref}` → 201 `{id}` |
| POST `/api/mod/recovery/{id}/approve` ⏱ (2nd moderator) | Wa | → 204 · `step_up_required` |
| GET `/api/mod/recovery/{id}/link` ⏱ (2nd moderator, after release) | Wa | → `{enroll_url}` |
| P1 POST `/api/recovery/sessions` | Wa | `{friend: ref}` (guardian A starts, in person) → 201 `{hs}` |
| P1 POST `/api/recovery/claim` | anon | `{payload}` (recovering device, guest session) → 204 |
| P1 GET `/api/recovery/requests` | G | → guardian's pending approvals |
| P1 POST `/api/recovery/requests/{id}/approve` ⏱ | Wa | `{intent, assertion}` (guardian B) → 204 |
| P1 GET `/api/recovery/status` | anon | (recovering guest session) → `{state, release_at?}`; when released, `/api/enroll/*` accepts this session |

**modapi** (P0-W12 → P1-W06)
| Method & path | Class | Request → Response |
|---|---|---|
| POST `/api/reports` ⏱ | Wr | `{target:{kind:"post"\|"person"\|"inviter"\|"preview", ref}, reason, note?}` → 202 (always; invalid refs dropped silently) |
| GET `/api/blame` | G | → `Page<BlameItem>` |
| GET `/api/mod/queue` (moderators) | G | → `Page<ModItem>` |
| POST `/api/mod/items/{id}/claim` (P1) | Ws | → 204 |
| POST `/api/mod/items/{id}/resolve` | Ws | `{action, reason}` → 204 (two-person actions → state "awaiting second") |
| POST `/api/mod/items/{id}/confirm` (2nd moderator) | Ws | → 204 · `step_up_required` (P1 + `{intent, assertion}`) |
| GET `/api/mod/log` | G | → `Page<LogItem>` |
| POST `/api/csp-report` | anon | `application/reports+json` → 204 (aggregate count per directive; no URLs or user data stored) |
| P1 POST `/api/mod/quarantine` | Ws | `{subject_item}` → 204 (then two-person confirm) |
| P1 POST `/api/mod/items/{id}/csam-report` | Ws | `{report_ref}` → 204 (fills `csam_holds.report_ref`, two-person) |

**staticapi** (P0-W12): GET `/robots.txt`, `/ai.txt`, `/.well-known/tdmrep.json`, `/terms`
(SPA route), `/*` → SPA `index.html` (no-store) / hashed assets (immutable). The scaffold
ships a working SPA fallback over `web/dist` or `PCC_STATIC_DIR` (buildability A2/A4); W12
completes it. P1-W04 adds GET `/m/{key}` (dev/test signed-media server).

**themeapi** (P1-W09): PUT `/api/me/theme` (Wa) `{tokens:{…}}` → `{theme}` · `theme_contrast`,
`theme_font`, `theme_invalid`; DELETE `/api/me/theme` (Wa) → 204 (back to default).

**mediaapi** (P1-W04): POST `/api/media` (Wm; multipart `file`, `alt`) → 201 `MediaView` ·
409 `{"error":"feature_disabled","feature":"media"}`, `media_too_large`, `media_type`,
`media_rejected`; GET `/api/media/{ref}/url?variant=thumb|display|original` ⏱ (R; M for
original) → `{url, expires_at}`.

**signalsapi** (P1-W07): POST `/api/signals` (—) `{webdriver, cdp, inputs_per_min:[…], visible_ms}` → 204.

**cosignapi** (P1-W01): POST `/api/cosign` ⏱ (Wa) `{purpose, target?}` → `{intent, record, options}`.

**testhooks** (SCAFFOLD; registered only when `PCC_ENV=test` and `PCC_TEST_HOOKS=1`):
`POST /api/_test/reset`, `/api/_test/clock {advance}`, `/api/_test/seed {fixture}`,
`/api/_test/login {handle}` (creates a session directly), `/api/_test/genesis` (returns an
enrollment token).

---
## 6. Frontend architecture (`web/`)

### 6.1 Principles (§15.1)
- **One data layer, many layouts.** Features fetch through `web/src/api/client.ts`
  (openapi-fetch + generated `schema.gen.ts`) and map API JSON to **view-models**
  (`web/src/vm/types.ts`, contract). **Formats are layout-only**: they receive VMs and
  callbacks as props, never import `api/` (ESLint `no-restricted-imports` on
  `src/formats/**` and `src/integrity/**`).
- **Integrity elements (§15.5)** are components in `src/integrity/` (props contract in
  `src/integrity/types.ts`, SCAFFOLD) and every format must render them. A Vitest suite
  renders each format with a fixture VM and asserts all seven are **present**
  (`getByTestId('integrity-*')`); **visibility, size and computed contrast** are asserted in
  Playwright (`web/e2e/integrity.spec.ts`, INT-0), because jsdom has no layout and resolves
  CSS custom properties unreliably (buildability D4).
- Server state via `@tanstack/react-query`; query keys namespaced by feature
  (`['feed', session]`, `['tree', snapshot, gen]`). No global store library.
- No `dangerouslySetInnerHTML`, no `innerHTML`, no `eval`, no inline styles from user data
  (ESLint core `no-restricted-syntax` on `JSXAttribute[name.name='dangerouslySetInnerHTML']`,
  `no-restricted-properties` for innerHTML/outerHTML/insertAdjacentHTML/document.write).
  Markdown renders to React elements.
- **Links (security #24):** an href is rendered only if `new URL(href).protocol` is `http:`
  or `https:` (otherwise the text renders as plain text — markdown-it's `validateLink` is not
  relied on); Markdown link `title` attributes are dropped; tracking parameters are stripped
  from the href with the shared `testdata/tracking-params.json` list; mention links render
  only from server-issued `MentionView.ref`s, never from text. A display name is never shown
  without its `@handle` next to it.

### 6.2 View-models (`src/vm/types.ts`, SCAFFOLD contract)
```ts
export type Ref = string & { readonly __ref: unique symbol };
export interface ChainVM { dist: number; firstHop?: HopVM; middle: HopVM[]; hiddenMiddle: number; firstHopPlus: boolean }
export interface InviterVM { kind: 'user' | 'genesis' | 'deleted'; handle?: string }
export interface AuthorVM { ref: Ref; handle: string; displayName: string; inviter: InviterVM; chain: ChainVM }
export interface PostVM {
  ref: Ref; author: AuthorVM; tier: number; body: MarkdownDoc | null /* parsed once in data layer */; lang: string;
  createdAt: Date; editedAt?: Date; audience: 'connected'|'d1'|'d2'|'d3'|'friend_plus'; refPolicy: 'anyone'|'two_hops'|'friends'|'nobody';
  canReference: boolean; mine: boolean; context: ContextVM[]; thread?: { index: number; total: number };
  previews: PreviewVM[]; likes: LikesVM; savedByMe: boolean; replyCount: number; media: MediaVM[]; fold: 'none'|'card'|'tree';
}
export type FeedItemVM =
  | { type: 'tier_start'; tier: number } | { type: 'tier_end'; tier: number } | { type: 'caught_up' }
  | { type: 'post'; post: PostVM } | { type: 'thread'; parts: PostVM[] }
  | { type: 'collapsed'; tier: number; count: number; token: string };
export interface FeedPageVM { session: string; preset: Preset; items: FeedItemVM[]; next?: string }
export interface TreeRowVM { gen: number; label: TreeLabel; center: Ref; items: TreeItemVM[]; more: number; cursor?: string }
export interface TreeVM { snapshot: string; focal: Ref; rows: TreeRowVM[]; hasEarlier: boolean; hasLater: boolean }
export interface PersonVM { ref: Ref; handle: string; displayName: string; inviter: InviterVM; relation: Relation; request?: 'outgoing'|'incoming'; chain: ChainVM; blocked: boolean; muted: boolean }
```
Integrity component props (`src/integrity/types.ts`, SCAFFOLD contract; buildability B2):
```ts
export interface TierHeaderProps { tier: number; kind: 'start' | 'end' | 'caught_up' }
export interface ChainProps { chain: ChainVM; author: { ref: Ref; handle: string } }
export interface InviterLabelProps { inviter: InviterVM; invitee: Ref /* report routes via the invitee */; onReport(target: ReportTarget): void }
export interface WhyButtonProps { post: Ref }                 // fetches /posts/{ref}/why lazily through an injected loader
export interface AudienceBadgeProps { audience: PostVM['audience']; forced?: boolean /* own posts only */ }
export interface EditedMarkProps { editedAt?: Date }
export interface ReportActionProps { target: ReportTarget; onReport(target: ReportTarget): void }
export type ReportTarget = { kind: 'post' | 'person' | 'inviter' | 'preview'; ref: Ref }
```
Mappers live with the feature that owns the endpoint (`features/feed/vm.ts`, …); the shared
`toPostVM` is owned by P0-W05 in `features/post/vm.ts` (feed, tree, people import it).

### 6.3 Layout formats (`src/formats/`)
```ts
// formats/types.ts (SCAFFOLD contract)
export interface FormatModule {
  id: 'cards' | 'compact' | 'explorer';
  FeedLayout: ComponentType<{ page: FeedPageVM[]; actions: PostActions; onLoadMore(): void; onExpand(token: string): void }>;
  PostLayout: ComponentType<{ post: PostVM; actions: PostActions; variant: 'feed' | 'tree' | 'focal' }>;
  TreeLayout: ComponentType<{ tree: TreeVM; actions: TreeActions }>;
}
// formats/registry.ts (SCAFFOLD): lazy chunks
export const formats = { cards: () => import('./cards'), compact: () => import('./compact'), /* P1 */ explorer: () => import('./explorer') };
```
Cards = §10.1.7 single-column cards + §10.1.4 rows (mobile: right-side overflow; desktop:
alternating sides; "+N more" after 5). Compact = dense rows + collapsible indented outline.
Explorer (P1) = SVG pan/zoom canvas + an outline mirror (same component as Compact's
outline) for keyboard/screen readers, plus the desktop minimap. Accessibility settings
(contrast, text size, reduced motion, dyslexia font) are CSS custom-property switches on
`<html>`, applied to every format.

### 6.4 Feature directories and routes

| Route | Feature dir | Owner | Notes |
|---|---|---|---|
| `/` | `features/feed` | P0-W07 | landing (`features/landing`, W12) when logged out |
| `/login` | `features/auth` | P0-W01 | passkey autofill (`mediation:"conditional"`) |
| `/enroll` (token in `#fragment`) | `features/auth` | P0-W01 | genesis / recovery enrollment; ToS + age attestation (genesis); "remove old passkeys" (recovery); optional second passkey |
| `/join` | `features/join` | P0-W04 | camera scan → SAS code shown → ToS + age attestation → handle → passkey → optional second passkey |
| `/invite`, `/invite/people` | `features/invites` | P0-W04 | budget, countdown, rotating QR, SAS confirm; invitee list with probation state |
| `/compose` (modal route, `?parent=&kind=`) | `features/composer` | P0-W05 | counter 280/500/20k, split-into-thread, audience, ref policy, parent-limit warning |
| `/p/:ref` | `features/tree` | P0-W08 | family tree, focal post |
| `/p/:ref/likes` | `features/engage` | P0-W06 | one post with its in-network like summary and like / hide / show-less controls; added at INT-0 |
| `/people/:ref`, `/friends`, `/friends/requests`, `/suggestions`, `/settings/blocks` | `features/people` | P0-W03 | |
| `/notifications` | `features/notifications` | P0-W10 | SSE badge in the shell via `api/sse.ts` |
| `/seen` | `features/seen` | P0-W06 | recently seen |
| `/settings/*` (display, languages, privacy, export, delete) | `features/settings` | P0-W11 | passkeys page: P0-W01 `features/auth/PasskeysPage.tsx`, linked; recovery banner slot |
| `/mod/recovery` | `features/modrecovery` | P0-W11 | two-moderator manual recovery with step-up |
| `/blame`, `/mod`, `/mod/log`, `/report` (modal) | `features/moderation` | P0-W12 | |
| `/terms`, landing | `features/landing` | P0-W12 | |
| step-up modal (global) | `features/stepup` | P0-W01 | mounted by shell slot; opened by `api/client.ts` on 401 `step_up_required` |
| P1 `/recover`, `/settings/recovery` | `features/recovery` | P1-W10 | |
| P1 `/saves` | `features/saves` | P1-W08 | |
| P1 `/settings/theme` | `features/themes` | P1-W09 | |
| P1 `/settings/notifications` (push) | `features/push` | P1-W05 | |
| step-up modal (P1 additions: signal-triggered step-up copy) | `features/stepup` | P1-W07 | |

Two route conflicts are resolved by the scaffold, not by WPs:
- **`/` index** is owned by SCAFFOLD `app/routes.tsx`: `<AuthSwitch user={lazy(features/feed/FeedPage)} anon={lazy(features/landing/LandingPage)} />`
  (decided from the `/api/session` query). W07 owns `FeedPage.tsx`, W12 owns `LandingPage.tsx`;
  neither declares `/` in its own `routes.tsx`.
- **`/settings/*`**: no feature uses a splat. Each declares explicit paths:
  W11 `/settings`, `/settings/{display,languages,privacy,export,delete}`; W01
  `/settings/passkeys`; W03 `/settings/blocks`; P1 adds `/settings/{recovery,theme,notifications}`.
  The settings index page links to all of them from a SCAFFOLD-owned list
  (`app/settingsNav.ts`, pre-filled with every P0 and P1 entry, P1 entries hidden behind a feature flag until SCAFFOLD-1).

`src/app/routes.tsx` (SCAFFOLD) imports `routes` arrays from each
`features/<f>/routes.tsx` (each exports `export const routes: RouteObject[]`, lazily
loading pages), so features never edit the shared route table. Shell slots
(`app/Shell.tsx`) render `features/<f>/slots.tsx` exports (nav badge, global modals) by
the same pre-listed import pattern.

### 6.5 i18n (§15.4)
- Catalogs: `web/src/i18n/locales/<locale>/<namespace>.json`, ICU syntax, keys
  `area.thing` (e.g. `feed:tier.header` = `{tier, select, 1 {Friends} 2 {Friends of friends} other {{tier} steps away}}`).
- Namespaces (SCAFFOLD creates `{}` stubs; one owner each): `common` (SCAFFOLD),
  `errors` (SCAFFOLD; one key per API error code, owners append via their own namespace
  `<ns>:errors.<code>`, client falls back to `errors:<code>`), `auth` W01, `people` W03,
  `stepup` W01, `invites` + `join` W04, `post` + `composer` W05, `engage` + `seen` W06, `feed` W07,
  `tree` W08, `markdown` W09, `notifications` + `integrity` W10, `settings` + `account` +
  `modrecovery` W11, `moderation` + `landing` W12; P1: `handshake` P1-W13, `cosign` P1-W01,
  `media` P1-W04, `push` P1-W05, `signals` P1-W07 (`stepup` passes to P1-W07), `themes` +
  `explorer` P1-W09, `recovery` P1-W10, `saves` P1-W08.
- `i18n/setup.ts`: i18next + `i18next-icu` + `react-i18next`, namespaces lazy-loaded via
  `import.meta.glob('./locales/*/*.json')`, `fallbackLng: 'en'`, UI locale from
  `MeView.settings.ui_locale` else `navigator.languages`. (The **post language** guess is
  different: plan §4.7 posts / D15.)
- `i18n/resources.d.ts` (SCAFFOLD) declares `CustomTypeOptions.resources` from the `en`
  JSON files ⇒ **a missing key is a TypeScript error**.
- **Pseudo-locale `en-XA`**: `scripts/pseudo-locale.mjs` parses every `en` message with
  `@formatjs/icu-messageformat-parser`, transforms only literal text (accents, ~40%
  expansion, `[ … ]` brackets), reprints ICU; output gitignored; generated by
  `predev`/`prebuild`/`pretest` (written to a temp file and renamed, under
  `web/.gen.lock`, so concurrent runs never expose a partial file). The pseudo-locale pass
  also covers what `eslint-plugin-i18next` misses (it skips components named like
  all-caps constants). Playwright runs one full pass in `en-XA` and fails on any
  visible text node without the bracket markers (except user content containers marked
  `data-user-content`) and on any element whose `scrollWidth > clientWidth` inside
  integrity elements (truncation).
- Lint: `eslint-plugin-i18next` `no-literal-string` (mode `jsx-only` + attributes `alt`,
  `title`, `aria-label`, `placeholder`) on `src/**` except tests/e2e; `check-i18n.mjs`
  fails on unused keys, ICU parse errors, and keys present in `en-XA` but not `en`.
- Dates/numbers/relative time only through `src/lib/time.ts` (`Intl.DateTimeFormat`,
  `Intl.RelativeTimeFormat`, `Intl.NumberFormat`). CSS uses logical properties only
  (stylelint is overkill; ESLint rule on CSS Modules is not available, so a tiny
  `scripts/check-css-logical.mjs` greps for `margin-left|right`, `padding-left|right`,
  `left:`, `right:`, `text-align: left|right`).

### 6.6 Security: CSP and Trusted Types
Production CSP (set by `secheaders`; Vite build emits no inline script/style):
```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob: data: <MEDIA_ORIGIN>;
font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; media-src 'self' blob:;
frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none';
require-trusted-types-for 'script'; trusted-types pcc-forbid pcc-script-url; upgrade-insecure-requests;
report-to csp
```
plus `X-Frame-Options: DENY` and `Reporting-Endpoints: csp="/api/csp-report"` (first-party,
anonymous, rate-limited, stores only a count per violated directive).
`src/lib/trustedTypes.ts` (SCAFFOLD) registers two policies:
- `pcc-forbid`: `createHTML/createScript` throw — any HTML/script sink use is a bug surfaced
  in e2e (console error ⇒ test failure).
- `pcc-script-url`: `createScriptURL(u)` returns `u` only if `new URL(u, location.href)` is
  same-origin **and** its path is `/sw.js` or under `/assets/` (Vite's hashed output);
  otherwise throws. It is the only way to obtain the `TrustedScriptURL` that
  `new Worker(...)` (the `jsqr` scanner worker, P1 audio worklet) and
  `navigator.serviceWorker.register(...)` require under enforced Trusted Types. Exposed as
  `scriptURL(u: string): TrustedScriptURL | string` (falls back to the string where the
  browser lacks Trusted Types). The service worker is registered by our own code
  (`vite-plugin-pwa` with `injectRegister: false`; `src/sw/register.ts` calls
  `navigator.serviceWorker.register(scriptURL('/sw.js'))`), because the plugin's
  auto-register passes a plain string and fails under enforced Trusted Types. Dev server
  sends the same CSP via a Vite plugin (no `'unsafe-eval'`; React Refresh needs an inline
  preamble ⇒ dev only adds a nonce) and registers a **dev-only** script-URL policy that also
  allows `/src/` and `/@vite/` module workers; the prod policy never does. Links render with
`rel="noopener noreferrer nofollow ugc"` and `referrerPolicy="no-referrer"`; every link
shows its real host (`urlx`-equivalent in `src/markdown/host.ts` using `punycode`).

### 6.7 API client (`src/api/`, SCAFFOLD)
- `client.ts`: `openapi-fetch` `createClient<paths>({ baseUrl: '/api' })` with middleware
  adding `X-PCC-CSRF` (token from `/api/session`, kept in memory), `credentials:'same-origin'`;
  maps error bodies to `ApiError {status, code, field?, retryAfterS?}`; on 401
  `step_up_required` triggers the step-up slot (P1); on 409 `feed_session_expired` the feed
  restarts.
- `schema.gen.ts`: generated by `npm run gen:api` = `node scripts/openapi-merge.mjs`
  (merges `api/openapi/*.yaml` → `api/openapi.gen.yaml`, fails on duplicate paths/schemas)
  + `openapi-typescript api/openapi.gen.yaml -o src/api/schema.gen.ts`. Both outputs gitignored,
  written atomically (temp file + rename, under `web/.gen.lock`); `pretypecheck`/`prebuild`/
  `pretest` regenerate. Go tests never depend on these files (they merge the fragments
  themselves, plan §4.6). Each worktree has its own generated copies.
- `sse.ts` (P0-W10): one `EventSource('/api/events')` per tab with backoff; typed
  `subscribe(type, fn)`.

### 6.8 PWA
`vite-plugin-pwa` `injectManifest`, `injectRegister: false`, with `src/sw/sw.ts` (SCAFFOLD:
precache app shell only; **never caches `/api/*`**) and `src/sw/register.ts` (SCAFFOLD,
Trusted-Types-safe registration). P1-W05 adds `src/sw/push.ts` (imported by `sw.ts` via a
pre-listed import) to show push notifications rendered from the server's localized text
(or a generic "new activity" text when the subscription has `hide_content`).
Vite builds to `web/dist` in a worktree; e2e runs serve the SPA from a per-run `outDir`
via `PCC_STATIC_DIR`, so a concurrent build never empties the directory a running test
server reads. The committed `web/dist/.keep` placeholder keeps `//go:embed all:dist`
compiling; `emptyOutDir` only ever targets the per-run directory or a full `npm run build`.

---

## 7. Test strategy

| Layer | Tool | Where | Runs |
|---|---|---|---|
| Unit (pure) | `go test` | next to code (`rank`, `graph`, `refs`, `sigs`, `urlx`, `mdscan`, `safefetch` with `newForTest`, `config` matrix) | always |
| Postgres integration | `go test -p 4` + `storetest.NewDB` (template clone per test, pool of 4) | `*_db_test.go` in each gate pkg | always; `PCC_REQUIRE_DB=1` in scripts/CI turns an unreachable DB into a failure |
| HTTP contract | `httptest` + `kin-openapi/openapi3filter` validating every request/response against `_common.yaml` + the module's own fragment (merged in Go) | `internal/api/<mod>api/*_test.go` via `apitest.Service` (one module over coreref deps) | always (no Node step) |
| Arch rules | `internal/archtest` (A1–A12) | — | always |
| Horizon / noninterference | `pgregory.net/rapid` | `internal/gate/conformance` (INT-0; seeded by WPs' own per-module invariants) | CI + merge queue |
| Perf + simulations | `go test -tags perf -run 'TestPerf|TestSim'` | `internal/gate/feed/perf_test.go`, `graph/bench_test.go`, `conformance/sim_test.go` | CI nightly + on demand; **never** in `check-wp.sh` (agents share one machine — buildability D3) |
| Web unit | Vitest + jsdom + Testing Library | `web/src/**/*.test.ts(x)` | always |
| E2E (per WP) | Playwright (Chromium) against a built `pcc` binary (`PCC_ENV=test`, `PCC_TEST_HOOKS=1`) | `web/e2e/<wp-id>.spec.ts`, using only scaffold hooks + the WP's own seed fixture | in `check-wp.sh` |
| E2E (journeys) | Playwright | `web/e2e/journeys/**` (INT) | merge queue + CI |

`-race` runs **only in CI** (Linux, cgo available). The Windows dev machine has
`CGO_ENABLED=0` and no gcc, so local scripts never pass `-race`.

**Determinism.** Tests use `clock.Fake`, fixed keys, and `coretest.World`, which derives every
fixture post ID from its label (`ids.Named`) and inserts users with explicit high-range IDs
followed by `setval`. A fixture's IDs therefore never depend on how many other users or
posts exist.

**Noninterference suite (INT-0, `internal/gate/conformance`).** A generator builds world
W1 = component A (contains V) ∪ component B, and W2 = W1 with random mutations **outside
H(V)**: add/remove users, edges, posts, blocks, detaches, children of A-posts written by
authors V cannot see, audience-limited children, reports, invites in B. The generator
**holds fixed** everything §2.2 puts inside H(V) (fidelity #9): V's own edges/requests/
invite relations; the existence, distance and first hop of every user V can see; named
middle hops; the **inviter label (handle and deleted status) of every user V can see**; the
likes and probation status of users within 2 hops of V (they feed §2.2(6) counts); and links
visible under §10.1.3. It replays a random sequence of V's calls through the *gate services*
(feed pages incl. all tiers, tree open/row/more, person/posts, likes, why, notifications,
suggestions, budget, invitees, post creation with parents) and compares
`json(W1)` with `json(W2)` **after normalization** (buildability D1): every opaque token that
is legitimately random or sequence-based — feed session ids, cursors, tree snapshot ids,
collapse tokens, notification refs — is replaced by its first-seen index within that run
(`<session#1>`, `<notif#3>`). Refs for users and posts are compared raw (they are
deterministic per viewer and target, and fixture IDs are label-derived).
Additional properties:
- tier == `graph.ReferenceBFS` distance (with the reader's own blocks); `len(chain) == tier`;
  no chain contains two non-adjacent friends; **first hop == `graph.ReferenceFirstHop`**
  (brute-force "all shortest paths, best first hop", fidelity #10);
- no post reaches a viewer without a path / outside its audience / only via someone the
  *viewer* blocked; **a person who blocked V still sees nothing change in V's view of
  them** and V can still reference their posts, producing shadow links (fidelity #1);
- seeded zero-edge bot never appears;
- ranked tier is a permutation of unranked; λ=0 ⇒ exactly chronological; engagement from
  beyond 2 hops never changes order; **adding invisible children to a post never changes its
  `KidsN`** (fidelity #2); a slippery post has `KidsN == nil` (fidelity #3);
- **freshness:** after unfriend/block/delete/audience change, the next page served from an
  existing snapshot contains no newly invisible post (fidelity #4);
- **charging uniformity:** for each write route, the rate-counter deltas for a valid ref and
  a garbage ref are identical (fidelity #7);
- oracle uniformity (nonexistent vs other-component vs outside-horizon refs ⇒ identical 404
  bytes); requests to someone who blocked V ≡ requests to someone who declined;
- **component isolation (§13):** scan every response V receives in W1 for any identifier
  belonging to component B (handles, display names, post bodies, refs minted for B's
  users/posts under any viewer) — the only allowed hit is the inviter label of a user V can
  see;
- inviter label present on every person and never a ref;
- foreign session/cursor/snapshot/collapse token (another viewer's) ⇒ `ErrNotFound`
  (security #2);
- P1: timing KS test (two-sample, p > 0.01, 500 samples each) over the **whole padded set**
  of plan §5.1, plus a server-side assertion that found and not-found durations land in the
  same padding bucket (security #19).

**Synthetic 10k graph (Phase 0 DoD).** `graph.Synth(10000, "ba", seed)` with m=3 (mean
degree ≈ 6, diameter ≈ 6) + 7 days of posts (Poisson, 1/day/user). Assertions: tiers 3–6
equal reference BFS for 200 random readers; the planner picks the cheaper scan (computed
cost check per tier); p95 `feed.Page` (first page, cold session) < 50 ms over 200 readers on
CI hardware (in-memory post index; Postgres only for seen/hides).

**Bot-isolation simulation (§13, fidelity #33; `conformance/sim_test.go`, perf tag).** On the
10k graph, attach a 500-account bot subtree to one real user through a single accepted
edge; bots post 20/day and like each other. Assert: with probation (forced `d1`) and the
degree-≥3 collapse, bot posts' share of impressions at degree ≤ 3 for real readers is
≈ 0 (≤ 0.1%), and after the one real user unfriends the bridge, it is exactly 0.

**Playwright.** Chromium only (WebAuthn virtual authenticator via CDP
`WebAuthn.addVirtualAuthenticator {protocol:'ctap2', transport:'internal',
hasResidentKey:true, hasUserVerification:true, isUserVerified:true}`).
**Isolation per run (buildability A4):** `scripts/e2e.sh` builds `var/pcc$(go env GOEXE)`
once and runs the binary (never `go run`, whose child process outlives a killed parent on
Windows), on a free port chosen per run (`PCC_HTTP_ADDR=127.0.0.1:<port>`, Playwright
`baseURL` to match, `reuseExistingServer: false`), against a fresh database
`pcc_e2e_<rand>` cloned from the template, serving the SPA from a per-run `PCC_STATIC_DIR`.
`/api/_test/reset` therefore only ever wipes that run's own database.
**Per-WP specs** use only scaffold hooks (`/api/_test/login`, `/api/_test/seed`,
`/api/_test/clock`, `/api/_test/genesis`) plus the WP's own fixture file
`testdata/fixtures/<wp-id>.json`, so they pass before any other WP lands
(buildability A3). Cross-WP journeys live in `web/e2e/journeys/**` (INT).
Fake camera: `web/scripts/gen-y4m.mjs` (P0-W04) renders a QR payload into a Y4M file (raw
I420, `qrcode` module matrix → luma plane) and `web/e2e/fixtures/camera.ts` (P0-W04) launches
the invitee browser context with `--use-fake-device-for-media-stream
--use-file-for-fake-video-capture=<file>`; the test reads the inviter's current frame from
the DOM (`data-testid=qr-payload`, test builds only), writes the Y4M, then launches the
invitee (frame windows accept "previous", so the 10 s rotation is safe). P1 audio:
P1-W02's tests use the deterministic `AudioInput` seam (feeds pre-rendered PCM to the
decoder) so they never wait on P1-W13; the real fake-audio-file path
(`--use-file-for-fake-audio-capture=<wav>`, WAV produced by P1-W13's encoder through
`OfflineAudioContext`) runs in an INT-1 journey.

Required e2e journeys (INT-0): genesis enroll → invite → SAS confirm → join (fake camera +
virtual authenticator) → both see friendship + "invited by" label; post → friend sees tier
1, FoF sees tier 2 with chain, stranger in other component sees nothing; reply with parents
→ tree rows; split into thread; like → "in your network" count; unfriend → disappears on
the next request even mid-session; block → the blocked user can still reference and gets a
shadow link the blocker never sees; export downloads (after step-up) and `pcc export-verify`
passes; manual recovery with hold + cancel; pseudo-locale pass over all pages; integrity
elements visible with contrast ≥ AA in every format; CSP/Trusted Types violation listener
on every test.

---

## Work packages

Rules recap (plan §0): owned paths are exclusive within a phase and listed below (the same
data lives in `scripts/ownership.json`); each WP works in its own worktree, ends green on
`scripts/check-wp.sh <id>`, and is merged by the merge queue. Every WP owns its migration
range (plan §3.1), its OpenAPI fragment, its i18n namespaces, its e2e spec
`web/e2e/<id>.spec.ts` and its e2e seed fixture `testdata/fixtures/<id>.json`.
Cross-module behaviour is reached only through `core` interfaces; until the implementing WP
lands, every interface is served by its correct `coreref` reference implementation, so
module tests (and the running app) are meaningful from the scaffold commit on.

"Hand-off stubs" are files the scaffold creates so imports compile from minute one;
ownership passes to the named WP, which replaces their content.

### Phase 0

Order: SCAFFOLD-0a ∥ SCAFFOLD-0b → P0-W01…P0-W12 in parallel → INT-0. No P0 WP waits on
another WP.

#### SCAFFOLD-0a: Go scaffold, contracts and reference implementations
- **Owns (permanently, then the steward):** `go.mod`, `go.sum`, `.gitattributes`,
  `.gitignore`, `.golangci.yml`, `Makefile`, `docker-compose.yml`, `deploy/postgres/**`,
  `.github/workflows/ci.yml`, `scripts/**` (incl. `ownership.json`, `check.sh`,
  `check-wp.sh`, `check-ownership.sh`, `db-reset.sh`, `e2e.sh`, `gen.sh`, `dev.sh`,
  `test-go.sh`, `lint.sh`, `build.sh`), `cmd/pcc/**`, `internal/archtest/**`,
  `internal/deps/**`, `internal/store/**` (migrations `0001–0099`),
  `internal/platform/{config,clock,ids,refs,sigs,httpx,logx}/**`,
  `internal/gate/{gate.go,views.go,errors.go}`, `internal/gate/internal/{core,coreref,coretest}/**`,
  `internal/gate/wire/**`, `internal/api/{apix,router,apitest,testhooks}/**`,
  `internal/api/deps.go`, `api/openapi/_common.yaml`, `docs/contract-requests/.gitkeep`.
- **Hand-off stubs:** every P0 domain package under `internal/gate/` (`doc.go` naming the
  owner, `service.go` with `type Service struct{ d *core.Deps }`, `New`, `Init`, every
  exported method of plan §4.7 **delegating to the coreref implementation** or returning
  zero values; `var _ core.X = (*pkg.Service)(nil)` assertions so signature drift fails to
  compile); `gate/rank` with the full signatures and a chronological body (valid Latest);
  `gate/users` **complete** (passes to P0-W11); `gate/auth` with a **working** session
  lookup middleware, `APIGuard` (origin, fetch-metadata, CSRF from `csrf_seed`) and guest
  sessions (passes to P0-W01); `api/staticapi` with a working SPA fallback over
  `web/dist`/`PCC_STATIC_DIR` (passes to P0-W12); empty `Register` in every
  `internal/api/<mod>api/routes.go`; an empty-paths fragment `api/openapi/<module>.yaml`
  per module.
- **Deliverables:**
  1. Repo plumbing. `go.mod` (module path plan §0.9, `go 1.27`, `ignore ./web/node_modules`,
     every P0 library of plan §2.1 pinned and blank-imported in `internal/deps/deps.go`).
     `.gitattributes` `* text=auto eol=lf`. `docker-compose.yml` (service `postgres`, image
     `postgres:18`, container `pcc-postgres`, port `54329:5432`, user/pass/db `pcc`,
     `command: ["postgres","-c","max_connections=400"]`, init script creating `pcc_test`;
     compatible with the running container — the scaffold also runs `ALTER SYSTEM SET
     max_connections=400` + one restart on it). Installs `golangci-lint@v2.14.0` with
     `go install` into `~/go/bin`. Scripts are POSIX bash, `set -euo pipefail`, prepend
     `$HOME/.local/go/bin:$HOME/go/bin` to `PATH`, export
     `PCC_TEST_DATABASE_URL` (dev default) and `PCC_REQUIRE_DB=1`, run `go test -p 4`, never
     `-race` locally.
  2. `scripts/check-wp.sh <id>`: `check-ownership.sh <id>` (diff vs. the scaffold base must
     touch only owned paths), `go vet ./...`, `golangci-lint run`, `go test -p 4 ./...`,
     `npm --prefix web run lint|typecheck|test`, `check-i18n` scoped to the WP's namespaces,
     `scripts/e2e.sh web/e2e/<id>.spec.ts`. `scripts/check.sh` = the same without scoping,
     plus all journeys (merge queue, CI). `scripts/wt.sh new <id>` creates the worktree and
     runs `npm ci` once.
  3. CI `.github/workflows/ci.yml`, triggers `push` to `main`, `pull_request`, nightly
     `schedule` (03:00 UTC). Job `check` (ubuntu-latest): service `postgres:18` with
     `POSTGRES_USER/PASSWORD=pcc`, `POSTGRES_DB=pcc_test`, port `54329:5432`,
     `pg_isready` health check; env `PCC_ENV=test`,
     `PCC_TEST_DATABASE_URL=postgres://pcc:pcc@localhost:54329/pcc_test?sslmode=disable`,
     `PCC_REQUIRE_DB=1`; steps: checkout → setup-go (`1.27.x`, cache) → setup-node (`24`,
     npm cache on `web/package-lock.json`) → `npm ci --prefix web` → `bash scripts/gen.sh`
     → golangci-lint (`v2.14.0`) → `go vet ./...` → `go test -race -count=1 -p 4 ./...` →
     web lint/typecheck/unit → `npm --prefix web run build` + `go build -o var/pcc ./cmd/pcc`
     → `npx --prefix web playwright install --with-deps chromium` → `bash scripts/e2e.sh`
     (all specs + journeys) → upload the Playwright report on failure. Job `perf`
     (schedule, or `[perf]` in the head commit message): same Postgres service, setup-go,
     `go test -tags perf -count=1 -run 'TestPerf|TestSim' ./internal/gate/...` (no web
     build: Go only needs the committed `web/dist/.keep`). Actions are pinned by commit SHA
     to the current majors verified at scaffold time (as of 2026-10-01:
     `actions/checkout@v7`, `actions/setup-go@v7`, `actions/setup-node@v7`,
     `golangci/golangci-lint-action@v9.3.0`, current `actions/upload-artifact`).
     Locally, `scripts/dev.sh` and `scripts/test-go.sh` reuse the running `pcc-postgres`
     container; `docker compose up -d postgres` runs only when no container named
     `pcc-postgres` exists.
  4. Go infrastructure (complete): plan §4.2 platform packages (config with the fail-closed
     matrix test, refs with version byte, sigs with the value grammar and domain-separated
     signing, logx), plan §4.3 store/jobq/storetest, migrations `0001–0009` (plan §3.2) —
     verified by `pcc migrate` against `pcc` and a storetest smoke test.
  5. Gate contracts (plan §4.4–§4.6 complete), `coreref` reference implementations,
     `coretest` World (DB + memory), `wiretest`, `apitest.Service`/`apitest.App` (Go-side
     OpenAPI merge with kin-openapi), `testhooks` (`Env == test` only), router + router_test,
     `cmd/pcc` (plan §4.1).
  6. Archtest A1–A12 green on the scaffold.
  7. `scripts/ownership.json` = the "Owns" lists of this section, with a test that no path
     is owned twice within a phase.
- **Acceptance:** `bash scripts/check.sh` green; `PCC_ENV` unset ⇒ `pcc serve` exits
  non-zero; `pcc serve` (dev) boots, `/api/session` returns `{authenticated:false}`,
  `/robots.txt` 200 (placeholder); a coreref-backed end-to-end smoke (genesis → login →
  post → feed shows it to a friend) passes through the real HTTP stack; commit
  `scaffold: phase 0 contracts (go)`.

#### SCAFFOLD-0b: Web scaffold, i18n and test tooling
- **Owns (permanently, then the steward):** `web/package.json`, `web/package-lock.json`,
  `web/vite.config.ts`, `web/tsconfig*.json`, `web/eslint.config.js`, `web/index.html`,
  `web/vitest.config.ts`, `web/playwright.config.ts`, `web/embed.go`, `web/dist/.keep`,
  `web/scripts/{openapi-merge,pseudo-locale,check-i18n,check-css-logical}.mjs`,
  `web/e2e/fixtures/{auth,world,server}.ts`, `web/src/main.tsx`, `web/src/app/**`,
  `web/src/api/{client,errors}.ts`, `web/src/vm/types.ts`, `web/src/i18n/{setup,namespaces}.ts`,
  `web/src/i18n/resources.d.ts`, `web/src/i18n/locales/en/{common,errors}.json`,
  `web/src/formats/{types,registry}.ts`, `web/src/formats/{cards,compact}/index.ts`,
  `web/src/integrity/types.ts`, `web/src/integrity/index.ts`, `web/src/lib/**`,
  `web/src/sw/{sw,register}.ts`, `web/src/styles/**`.
- **Hand-off stubs:** `features/<f>/routes.tsx` + `slots.tsx` for every P0 feature (empty
  arrays); `features/feed/FeedPage.tsx` (W07), `features/landing/LandingPage.tsx` (W12),
  `features/post/vm.ts` with `export function toPostVM(p: components['schemas']['PostView']):
  PostVM` (W05), `src/markdown/index.ts` with `parse(body: string): MarkdownDoc` and
  `<Markdown doc={…} />` (W09), `src/api/sse.ts` with
  `subscribe<T>(type: string, fn: (d: T) => void): () => void` (W10), the seven integrity
  components rendering their test ids (W10), `formats/{cards,compact}/{FeedLayout,PostLayout,TreeLayout}.tsx`
  (W07/W08), `locales/en/<every P0 namespace>.json` as `{}` (each namespace's owner).
- **Deliverables:** plan §2.2 dependency set (with the `openapi-typescript` override,
  `@types/react`, `@types/react-dom`, `@types/node`; no `eslint-plugin-react`);
  `package-lock.json` committed; scripts `dev`, `build`, `preview`, `lint`, `typecheck`,
  `test`, `e2e`, `gen:api`, `i18n:pseudo`, `i18n:check` with atomic `pre*` generators;
  Vite config (React plugin, PWA `injectManifest` + `injectRegister: false`, dev proxy
  `/api` + `/m` → the dev server, dev CSP plugin with the dev-only script-URL policy);
  strict tsconfig (`noUncheckedIndexedAccess`); flat ESLint config (`@eslint/js`,
  typescript-eslint strict-type-checked, react-hooks, `i18next/no-literal-string`,
  `no-restricted-syntax` for `dangerouslySetInnerHTML`, `no-restricted-imports` for
  formats/integrity → api, `no-restricted-properties`); app shell, AuthSwitch,
  `app/session.ts` (`useSession()`/`useMe()`), `app/settingsNav.ts` (every P0 and P1 entry,
  P1 hidden until SCAFFOLD-1); Trusted Types policies (plan §6.6); i18n setup with
  type-checked keys; pseudo-locale; Playwright config with per-run port/DB/static dir
  (plan §7) and projects `en` / `en-XA`; `fixtures/auth.ts` (virtual authenticator),
  `fixtures/world.ts` (test-hook client), `fixtures/server.ts` (builds and starts the binary
  per run).
- **Acceptance:** `npm ci` succeeds from a clean clone (no `ERESOLVE`); `npm run
  lint|typecheck|test|build` green; SPA shell renders in `en` and `en-XA` against the
  SCAFFOLD-0a binary; a TT violation listener reports zero violations, including service
  worker registration; commit `scaffold: phase 0 contracts (web)`.

#### P0-W01: Auth, sessions, CSRF, passkeys, step-up, genesis enrollment
- **Owns:** `internal/gate/auth/**`, `internal/api/authapi/**`, `api/openapi/auth.yaml`,
  `web/src/features/{auth,stepup}/**`, `web/src/i18n/locales/en/{auth,stepup}.json`,
  `web/e2e/p0-w01.spec.ts`, `testdata/fixtures/p0-w01.json`, migrations `1010–1019`.
- **Implements:** `core.Passkeys` (incl. `BeginAssertion`/`VerifyAssertion`,
  `IssueEnrollment`); `core.Viewer` in ctx. **Consumes:** `Users`, `Signer`
  (PasskeyChange), `Notifier`, `Limiter.ChargeAnon`.
- **Deliverables:** session store (hashed tokens, guest/user/enrollment kinds, sliding
  expiry, rotation of token **and** `csrf_seed` on every privilege change,
  `credential_id`); `Middleware` + `APIGuard` exactly as plan §4.7 (checks on `/api/*`
  only; unauthenticated POSTs need same-origin + exact Origin); login ceremony stored in a
  guest session (2 min, single use, cleared on any finish) and a fresh token on success;
  status refusal with the uniform 404; step-up (`/auth/stepup/*`, allowCredentials = own
  credentials, the full assertion check list of plan §4.5 `Passkeys`); passkey add (fresh +
  `excludeCredentials`), list, remove (fresh, never the last, revokes that credential's
  sessions) with `friend_security` notifications and `PasskeyChange` records carrying the
  COSE public key; enrollment by fragment token (bound to the session, atomic use), genesis
  ToS + age attestation, recovery enrollment listing existing passkeys with "remove old";
  `SeedGenesis`; `authtest` software authenticator (ES256, none attestation) for Go tests;
  `SessionEnded` on logout/expiry/revocation; login page (conditional mediation), enroll
  page, passkeys page, the global step-up modal, and an optional "add a second passkey"
  prompt reused by join and enroll (§10.3). `SameSite=Strict` cookie test.
- **Acceptance:** CSRF missing/wrong ⇒ 404 bytes identical to an unknown route;
  `Sec-Fetch-Site: cross-site` on `/api/*` ⇒ 404 while a cross-site navigation to `/` still
  gets the SPA; login POST without exact Origin ⇒ 404; the login ceremony cannot be replayed
  (second finish with the same assertion ⇒ 404); session id differs before/after login
  (no fixation); a suspended user's valid assertion ⇒ the same 404; step-up with a passkey
  belonging to another account ⇒ rejected; passkey add without fresh auth ⇒
  `step_up_required`; removing a credential kills its sessions; session token never stored
  in clear; enrollment token never appears in any request line or access log (log capture
  test); genesis → enroll → `/api/session` authenticated; last passkey cannot be removed;
  Playwright login + step-up with the virtual authenticator.

#### P0-W02: Graph engine, visibility, chains, views, synthetic data
- **Owns:** `internal/gate/{graph,visibility,views,devseed}/**`, migrations `1020–1029`.
- **Implements:** `core.Graph`, `core.Visibility`, `core.Chains`, `core.Views`.
  **Consumes:** `Users`, `PostIndex`, `Engagement` (likes summary), `Bus`.
- **Deliverables:** plan §4.7 graph (load from `edges`/`blocks`/`hide_via`, mutations,
  epoch, layered BFS with the reader's own exclusions, the D22 first-hop rule, `Path`,
  `Layer`, bitmaps), `ReferenceBFS`, `ReferenceFirstHop`, `Synth`; visibility with
  one-directional blocks (D7), `CheckPost`/`Checked`, `CanSeeLink`, `VisibleChildren`/
  `VisibleParents` (limit after filtering), `CanReference`; chains (§4.5 naming); views
  (every `PostView`/`PersonView`/`AuthorView` field of plan §4.4); `devseed.Synthetic`
  and the fixture loader for `testdata/fixtures/*.json` (refuses unless `DevLike`).
- **Acceptance (rapid, unit-level, no timing assertions):** `Dist == ReferenceBFS` on random
  graphs with random blocks; `FirstHop == ReferenceFirstHop` (fidelity #10) and is
  independent of adjacency order; `len(Path(u))-1 == Dist(u)`; consecutive path members
  adjacent; no path through a user the **reader** blocked; a user who blocked the reader is
  still reachable and their posts still visible (D7); epoch increments on every removal /
  block; `CheckPost` truth table (audience × dist × reader-blocks × author-blocks-reader ×
  probation-forced × deleted × suspended) exhaustive; `CanSeeLink` table for §10.1.3;
  `VisibleChildren(Limit 20)` returns the 20 newest *visible* children even when 50 newer
  invisible ones exist; `CanReference` table incl. `cont` on another's post ⇒ false and the
  author's own `ref` on a slippery post ⇒ true; `Views.Posts` refuses anything but
  `Checked` (compile-time). BFS < 5 ms p95 and 10k load < 200 ms are **perf-tagged**
  benchmarks, not unit acceptance.

#### P0-W03: Friendships, blocks, mutes, people, suggestions
- **Owns:** `internal/gate/friends/**`, `internal/api/friendsapi/**`,
  `api/openapi/friends.yaml`, `web/src/features/people/**`,
  `web/src/i18n/locales/en/people.json`, `web/e2e/p0-w03.spec.ts`,
  `testdata/fixtures/p0-w03.json`, migrations `1030–1039`.
- **Implements:** `core.Edges`, `core.Mutes`. **Consumes:** `Graph`, `Visibility`,
  `Views`, `Signer`, `Notifier`, `Users`.
- **Deliverables:** plan §4.7 friends API; Friendship/Revocation/Block records via
  `core.Signer` (purge_after on revocation; Block record deleted on unblock); `edge_events`;
  jobs `graph.edge_events_purge`, `friends.requests_scrub`; request caps (20/day, 5 on
  probation); the D23 cap rule; requests to a blocker silently declined; declined looks
  pending; block ⇒ unfriend + never revealed; suggestions; person page, friends list,
  requests inbox, blocks list, suggestion cards; "report inviter" via the `ReportAction`
  integrity component (props only).
- **Acceptance:** request→accept creates exactly one Friendship record and two `edges` rows;
  requester view byte-identical for declined vs pending **vs blocked-by-target**; the
  blocker receives no notification of any kind from the blocked user; accept never fails
  because of the *requester's* count; `friend_cap` only from the caller's own count;
  unfriend keeps `invites` row and label; 30 days after unfriend the Friendship/Revocation
  records are gone (fake clock); suggestions never include people with <2 mutual,
  opted-out or viewer-blocked; person endpoint for an other-component ref ⇒ uniform 404.

#### P0-W04: Invites, budget, probation, QR handshake with SAS, join flow
- **Owns:** `internal/gate/invites/**`, `internal/api/invitesapi/**`,
  `api/openapi/invites.yaml`, `web/src/features/{invites,join}/**`,
  `web/src/i18n/locales/en/{invites,join}.json`, `web/scripts/gen-y4m.mjs`,
  `web/e2e/fixtures/camera.ts`, `web/e2e/p0-w04.spec.ts`, `testdata/fixtures/p0-w04.json`,
  migrations `1040–1049`.
- **Implements:** `core.Invites`, `core.Probation`. **Consumes:** `Passkeys`, `Signer`,
  `Graph`, `Edges`, `Users`, `Notifier`, `Metrics`, `Bus`.
- **Deliverables:** token bucket + countdown (`BudgetView`); invitee list with probation
  state (§8); probation evaluation; rotating QR frames (10 s); claim bound to the guest
  session; **SAS confirm** step on both screens; registration (handle rules, ToS + age
  attestation, passkey via `core.Passkeys`, `handle_taken` capped at 10 per handshake —
  D21), the RegisterFinish transaction (plan §4.7), Invite + Friendship records, inviter
  notification `invite_redeemed`, `invites.redeemed` metric; web: invite screen (saved
  invites, countdown, probation countdown + "N more friends outside your invite tree"),
  invitee list, QR display via `qrcode` canvas, SAS display + "codes match / don't match",
  invitee scanner (`getUserMedia` + `jsqr` in a module worker through `scriptURL`), SAS
  display, ToS/age, handle, passkey, optional second passkey.
- **Acceptance:** probation account cannot open a handshake (`on_probation`); inviter at
  `edges+pending = 1000` ⇒ `friend_cap`; budget never exceeds cap; concurrent double-redeem
  of one handshake creates one user (serializable test with 10 goroutines);
  `RegisterBegin` before SAS confirm ⇒ 404; a second claimer's SAS differs from the first's;
  stale frame (2 windows old), forged MAC, reused handshake, wrong session ⇒ all
  byte-identical 404; 11th `handle_taken` probe on one handshake ⇒ 404; probation clears
  exactly when both conditions hold (fake clock); invitee list shows probation only to the
  inviter (another user's request ⇒ 404); Playwright: inviter + invitee contexts with fake
  camera Y4M and virtual authenticators → SAS confirm → friendship + "invited by @x".

#### P0-W05: Posts, post index, composer
- **Owns:** `internal/gate/{posts,postindex}/**`, `internal/api/postsapi/**`,
  `api/openapi/posts.yaml`, `web/src/features/{post,composer}/**` (incl. the shared
  `features/post/vm.ts` `toPostVM`), `web/src/i18n/locales/en/{post,composer}.json`,
  `web/e2e/p0-w05.spec.ts`, `testdata/fixtures/p0-w05.json`, migrations `1050–1059`.
- **Implements:** `core.PostIndex`. **Consumes:** `Visibility`, `Graph`, `Users`, `Signer`,
  `Notifier`, `Views`.
- **Deliverables:** create/thread/get/edit/delete/ref-policy/detach(+all) with the plan §4.7
  check order and validations; Post/Tombstone records with salted `body_commit`; NFC bodies;
  shadow links when the parent's author blocked the child's author; mentions resolved within
  the author's horizon; notifications to parent authors (batched key) and mentioned users;
  `core.PostIndex` (recent index, window scan, LRU, nightly trim); composer: counter appears
  at 280, amber at 500, hard stop 20,000 — **code points**, not UTF-16 units;
  split-into-thread at paragraph→sentence boundaries (≤25 parts, editable preview);
  audience + ref policy pickers (slippery default from settings); warning when a parent has
  an audience limit; language picker defaulting to the **first entry of the user's
  `languages[]`** (then the UI locale's primary subtag), with `und` and manual choice (D15,
  fidelity #28); ref-policy change to `nobody` shows the "detach all" offer from the 200
  response.
- **Acceptance:** referencing an invisible/foreign post ⇒ uniform 404 **and** the same rate
  counter delta as a valid reference; 3rd reference of the same post within 24 h ⇒
  `ref_cap`; second `cont` on a post ⇒ `cont_taken`; `cont` on another's post ⇒
  `cont_not_own`; a reference from a user the parent's author blocked ⇒ 201, shadow link
  visible only to the child's author, no notification; edit keeps id, bumps version, new
  salt, parents immutable; delete nulls body **and** salt and leaves children rendering as
  roots for others; probation author's post has `audience=d1, audience_forced` and renders
  to friends byte-identically to a voluntary `d1`; thread publish charges one write; split
  algorithm unit tests (CJK, emoji, code blocks never split); a 20,000-code-point emoji
  post is accepted (body limit 96 KiB) and 20,001 is rejected on both client and server.

#### P0-W06: Engagement, seen state, network preference, ranking
- **Owns:** `internal/gate/{engage,rank}/**`, `internal/api/engageapi/**`,
  `api/openapi/engage.yaml`, `web/src/features/{engage,seen}/**` (`usePostActions`
  like/hide/show-less, `useSeenTracker` IntersectionObserver ≥50% for ≥1 s, batched POST
  every 5 s / on hide), `web/src/i18n/locales/en/{engage,seen}.json`,
  `web/e2e/p0-w06.spec.ts`, `testdata/fixtures/p0-w06.json`, migrations `1060–1069`.
- **Implements:** `core.Engagement`, `core.Prefer`; the `rank` package. **Consumes:**
  `Graph`/`Search`, `Visibility` (`VisibleChildren`), `PostIndex`, `Users.InviterOf`,
  `Mutes`, `Views`, `Metrics`.
- **Deliverables:** likes (bitmaps), seen (14-day, purge job), hides, show-less, likers
  summary, `core.Engagement`, `core.Prefer` (§5.5 n, inviter cap, probation/author weight 0,
  direct-reply credit from `VisibleChildren(RefOnly, NotSelf, Limit 20)`, nil for slippery
  parents), `rank.Score/Less/RankTier/ParamsFor` with the show-less penalty and reasons;
  `feed.impressions_by_degree` from seen events; recently-seen page. The seen tracker
  observes only the element carrying `data-seen-ref` (the card's own post); context rows and
  collapsed thread parts never carry it (§10.1.2).
- **Acceptance:** rank property tests — permutation; λ=0 ⇒ strictly chronological (ties by
  `Less`) **including with show-less counts present**; author cap holds under Balanced and is
  off under Latest; clamp bounds `±S`; cold start never negative; credit ≤ n; reply penalty
  exactly 6 h under Balanced; show-less penalty = 8 h·min(n,3)·λ; adding likes from users at
  distance ≥3 never changes order; adding invisible / detached / self children never changes
  `KidsN`; slippery ⇒ no credit; `MarkSeen` with garbage refs returns 204 and writes nothing.

#### P0-W07: Feed and Cards/Compact feed and post layouts
- **Owns:** `internal/gate/feed/**`, `internal/api/feedapi/**`, `api/openapi/feed.yaml`,
  `web/src/features/feed/**`, `web/src/formats/cards/{FeedLayout,PostLayout}.tsx`,
  `web/src/formats/cards/feed/**`, `web/src/formats/compact/{FeedLayout,PostLayout}.tsx`,
  `web/src/formats/compact/feed/**`, `web/src/i18n/locales/en/feed.json`,
  `web/e2e/p0-w07.spec.ts`, `testdata/fixtures/p0-w07.json`, migrations `1070–1079`.
- **Consumes:** `Visibility`, `PostIndex`, `Engagement`, `Prefer`, `Views`, `rank`,
  `Metrics`, the integrity component props contract.
- **Deliverables:** plan §4.7 feed algorithm (planner with recorded choice, lazy per-tier
  snapshots keyed by `(viewer, id)`, viewer-bound HMAC cursors, per-page re-check, threads
  as one item, low-trust collapse at k≥3, `tier_start/tier_end/caught_up`, new-above
  polling, why, `preset_unavailable` for `top`, feed metrics); feed page with sticky tier
  headers from markers only, "N new posts above" banner, preset switch Latest/Balanced,
  caught-up end; Cards + Compact feed and post layouts rendering all seven integrity
  components; card folding (>6 lines or 400 chars, keyboard/screen-reader accessible).
- **Acceptance:** planner picks author scan for tier 1–2 and time scan when
  `|L_k|+P_k > W` (unit with synthetic counts); unseen-first strictness (a seen tier-1 post
  never precedes an unseen tier-2 one, and vice versa); every candidate of a tier is in the
  snapshot (no prefix ranking); pages never reshuffle while new posts arrive; **after an
  unfriend/block/delete mid-session, the next page omits the affected posts**; another
  viewer's cursor/session/token ⇒ 404 while one's own expired session ⇒
  `feed_session_expired`; header strings come only from catalogs; integrity presence suite
  passes for both formats.

#### P0-W08: Family tree
- **Owns:** `internal/gate/tree/**`, `internal/api/treeapi/**`, `api/openapi/tree.yaml`,
  `web/src/features/tree/**`, `web/src/formats/cards/TreeLayout.tsx`,
  `web/src/formats/cards/tree/**`, `web/src/formats/compact/TreeLayout.tsx`,
  `web/src/formats/compact/tree/**`, `web/src/i18n/locales/en/tree.json`,
  `web/e2e/p0-w08.spec.ts`, `testdata/fixtures/p0-w08.json`, migrations `1080–1089`.
- **Consumes:** same as W07.
- **Deliverables:** plan §4.7 tree service (rows −2…+2, primary parent, focal-centered
  sibling row, ranking per row with `rank.Less`, bodies for top 10, `More`, `ShownAbove`,
  `AlsoParents` chips, viewer-keyed snapshot cache with per-request re-check, re-center via
  `Row`); Cards rows layout (mobile right-side overflow, desktop alternating, "+N more"
  after 5, section header per row, hairline tier dividers, swipe = horizontal scroll-snap +
  buttons for keyboard), **tree folding at 10 lines** (keyboard + screen reader, §10.1.5),
  and the Compact collapsible outline.
- **Acceptance:** tree noninterference unit (add/delete/detach an invisible child ⇒
  identical JSON); no placeholder for hidden parents; diamond shows once + "shown above";
  row order = (tier, `rank.Less`) under the reader's preset, with equal `t_eff` broken by
  friend+ first hop then edge age then ID; traversal never routes through an invisible
  post; another viewer's snapshot id ⇒ 404; posts longer than 10 lines fold in the tree
  and expand by keyboard.

#### P0-W09: Markdown policy and link previews (server + client)
- **Owns:** `internal/platform/{safefetch,urlx,mdscan}/**`, `internal/gate/previews/**`,
  `testdata/markdown/**`, `testdata/tracking-params.json`, `web/src/markdown/**`
  (incl. `LinkPreview.tsx`, `host.ts`), `web/src/i18n/locales/en/markdown.json`,
  `deploy/runbooks/egress.md`, migrations `1090–1099`.
- **Consumes:** `PostIndex.Bodies`, `jobq`, `config.PreviewDelay`, `Bus` (`PostDeleted`).
- **Deliverables:** SSRF-guarded fetcher (plan §4.2 address policy); URL canonicalization +
  shared tracking-param list + IDN / mixed-script flag; `CleanText`; `mdscan` with
  scheme-only autolinks and code-point offsets (shared fixture corpus ≥60 cases, identical
  link/mention extraction in Go and TS); `preview.fetch` job with 1 h cache, per-host
  limiter, bucketed `ready_at`; client markdown-it config (`html:false`, scheme-only
  linkify, node allowlist → React elements, headings at body size semibold, `![]()` →
  link, fenced code collapsed past 10 lines, nesting ≤2, `<url>` no-preview, mention tokens
  only from server refs, protocol-checked hrefs, no titles, stripped tracking params).
- **Acceptance:** SSRF table — `127.0.0.1`, `[::1]`, `10/8`, `172.16/12`, `192.168/16`,
  `100.64/10`, `169.254.169.254`, `fd00::/8`, `0.0.0.0`, `198.18.0.1`, `240.0.0.1`,
  `[::ffff:127.0.0.1]`, `[64:ff9b::7f00:1]`, `[2002:7f00:1::]`, `[fe80::1]`, decimal/octal IP
  forms, DNS rebinding (resolver returns public then private), redirect to private, port
  8080, `http://user@host/`, `file:`/`gopher:`, `HTTP_PROXY` set in the environment (ignored)
  — all blocked; 512 KiB cap measured after gzip decompression (a 10 KiB gzip bomb is
  cut off); 3 s cap; non-HTML refused; `utm_*`/`fbclid`/`gclid` stripped on both sides;
  `xn--` hosts shown decoded with the mixed-script flag; bidi/zero-width characters removed
  from titles; a slow fetch (> 1 bucket) shows at exactly bucket 2 and a fetch slower than
  2 buckets never shows; heading-only post renders as a paragraph-sized line; raw HTML
  shows literally; `javascript:` and `data:` hrefs render as text; no `innerHTML` anywhere
  in `src/markdown`.

#### P0-W10: Notifications, SSE, metrics, integrity components
- **Owns:** `internal/gate/{notify,metrics}/**`, `internal/api/notifyapi/**`,
  `api/openapi/notify.yaml`, `web/src/api/sse.ts`, `web/src/features/notifications/**`,
  `web/src/integrity/**` except `types.ts` and `index.ts`,
  `web/src/i18n/locales/en/{notifications,integrity}.json`, `web/e2e/p0-w10.spec.ts`,
  `testdata/fixtures/p0-w10.json`, migrations `1100–1109`.
- **Implements:** `core.Notifier`, `core.Metrics`. **Consumes:** `Users`, `Visibility`,
  `Views`, `Graph.Blocks`, `jobq`, `Bus` (`SessionEnded`).
- **Deliverables:** `core.Notifier` (persist, batch by group key, hourly flush, SSE hub with
  per-user fan-out, 25 s pings, `Live` events, block-drop rule, stream close on
  `SessionEnded`), list/read/mute-post; `core.Metrics` + `metrics.flush` + `Report` behind
  `pcc metrics`; client SSE singleton with backoff, unread badge slot, notifications page
  rendering every kind from catalogs with ICU plurals; the seven integrity components
  (TierHeader, Chain, InviterLabel, WhyButton, AudienceBadge, EditedMark, ReportAction)
  against the `integrity/types.ts` props, with reserved styles/icons not available to
  user content.
- **Acceptance:** a declined friend request produces no notification for the requester;
  a mention or reference by someone the recipient blocked produces nothing; batched kind
  upserts one unread row with incrementing count; subjects no longer visible are omitted
  from the view; SSE delivers within 1 s in an integration test and the stream closes
  within 1 s of logout; at most one ping/hour per batch group; metrics API rejects names
  outside the closed list (test) and `metrics_daily` never contains anything but
  `(day, name, bucket, value)`; integrity components render from catalogs only.

#### P0-W11: Signed records, keys, users, account, settings, export, deletion, manual recovery
- **Owns:** `internal/gate/{records,account,users}/**`, `internal/api/accountapi/**`,
  `api/openapi/account.yaml`, `web/src/features/{settings,modrecovery}/**`,
  `web/src/i18n/locales/en/{settings,account,modrecovery}.json`, `web/e2e/p0-w11.spec.ts`,
  `testdata/fixtures/p0-w11.json`, migrations `1110–1119`.
- **Implements:** `core.Signer`, `core.Users` (takes over the scaffold's complete version).
  **Consumes:** `Edges.Revoke`, `Passkeys.{RevokeAll,IssueEnrollment}`, `Notifier`, `jobq`.
- **Deliverables:** `core.Signer` (server key, `signing_keys`, `record_parties`, value
  grammar, domain-separated signatures, `purge_after`, delete), `RotateServerKey`, key
  fingerprints, `VerifyExport` (+ CLI, optional pinned fingerprints), `AuditEdges` (+ CLI,
  weekly job), `records.purge`; `Me`, settings validation (BCP-47, `translate_to ∈
  languages`, `ui_prefs` JSON schema: `format ∈ {cards, compact}`, `density`,
  `a11y{contrast,text_scale,reduced_motion,dyslexia_font}`, `rank_preset ∈ {latest,
  balanced}` in P0, display name via `CleanText` + reserved glyphs); export (POST, fresh
  step-up; zip with records, keys + fingerprints, posts with salts, credentials); account
  deletion job (fresh step-up); manual recovery in the moderator UI (two moderators,
  each fresh; 24 h hold; cancel banner; release; session revocation; friends notified);
  settings pages (display/format, languages, privacy toggles, export, delete); the
  moderator recovery page.
- **Acceptance:** export of two friends contains the byte-identical Friendship record and
  both verify against `keys.json`; tampering one byte, changing the envelope `kind`, or
  swapping `key_id` fails `VerifyExport`; a non-canonical body fails; the value grammar
  rejects floats, non-UTC times and nulls; a deleted post's exported `body_commit` cannot be
  matched without the salt; `AuditEdges` flags an `edges` row whose record is missing or
  revoked; deleted account: posts tombstoned, edges revoked, invitees show `deleted` label,
  handle not reusable; recovery requires two different moderators with fresh step-ups, the
  link is only available after 24 h, and a cancel from the account's live session voids
  it; export/delete without fresh auth ⇒ `step_up_required`; settings reject `translate_to`
  not in `languages` and `rank_preset: top`.

#### P0-W12: Read and write budgets, moderation, security headers, static and landing
- **Owns:** `internal/gate/{limits,moderation}/**`, `internal/platform/secheaders/**`,
  `internal/api/{modapi,staticapi}/**`, `api/openapi/mod.yaml`,
  `web/src/features/{moderation,landing}/**`,
  `web/src/i18n/locales/en/{moderation,landing}.json`, `web/e2e/p0-w12.spec.ts`,
  `testdata/fixtures/p0-w12.json`, migrations `1120–1129`.
- **Implements:** `core.Limiter` (+ `apix.Charger`). **Consumes:** `Users`, `Views`,
  `Notifier`, `Visibility`, `Passkeys` (fresh check via `core.RequireFresh`).
- **Deliverables:** `core.Limiter` with read **and write** classes charged by `apix` (+
  purge job), anonymous per-prefix limiter (memory only, `RealIP` from trusted proxies),
  uniform 429; reports (incl. report-inviter routed through the invitee,
  preview-misleading) with closed reason codes; moderator queue showing subject inviter
  label and probation flag; resolve with two-person `suspend_user` (second moderator
  confirms with fresh step-up); blame inbox + `invitee_action` notification; public
  moderation log with pseudonyms; `GrantModerator` CLI; CSP report endpoint (aggregate
  counts); secheaders (CSP incl. `report-to`, `X-Frame-Options`, TDM, robots, ai.txt,
  tdmrep, `X-Robots-Tag`); landing + terms pages (no app data; terms include the published
  server key fingerprints section).
- **Acceptance:** for every route class, a request with a valid ref and one with a garbage
  ref change the counters identically (fidelity #7); 429 body identical regardless of
  target; spoofed `X-Forwarded-For` from an untrusted peer does not change the anonymous
  bucket; every response (HTML, JSON, SSE, assets, 404) carries `tdm-reservation: 1`, the
  CSP and `X-Frame-Options: DENY`; `robots.txt` disallows `/` for `*` and names GPTBot,
  ClaudeBot, CCBot, Google-Extended, PerplexityBot, Bytespider, Applebot-Extended,
  meta-externalagent; an invalid reason code ⇒ `reason_invalid`; blame entry created only
  after moderator confirmation and includes the post only if the inviter can see it;
  `suspend_user` by one moderator stays pending.

#### INT-0: Phase 0 integration
- **Owns:** `internal/gate/conformance/**`, `web/e2e/journeys/**`,
  `web/e2e/integrity.spec.ts`; may edit any file to fix integration bugs (through the merge
  queue); applies remaining `docs/contract-requests/*`; flips `router_test` to
  "require all".
- **Deliverables:** plan §7 noninterference suite (normalized comparison, H(V) held fixed,
  component isolation scan, chain oracle, freshness, charging uniformity), 10k perf test,
  bot-isolation simulation, end-to-end journeys, integrity visibility/contrast pass,
  pseudo-locale pass, Phase 0 DoD checklist (§18) ticked with evidence (test names), and
  the metrics needed for the 20-person trial wired (`pcc metrics` prints invites/day, feed
  depth, impressions by degree, preset switching).

### Phase 1

Order: SCAFFOLD-1 → P1-W01…P1-W13 in parallel → INT-1. Ownership is re-assigned for
Phase 1 (listed per package); files not listed stay with the steward. External
dependencies (R2/S3, PhotoDNA, Web Push VAPID, DBSC) sit behind Go interfaces with local dev
drivers and **fail closed** in production config (plan §4.2 config, D20).

#### SCAFFOLD-1: Phase 1 contracts
- **Owns (then the steward):** migrations `2001–2099`; `core` additions; coreref/coretest
  additions; `internal/deps/deps.go` P1 imports; `config` P1 fields; router P1 lines;
  `wire.App` P1 fields; `apix` changes calling `apix/pad`; `web/src/features/handshake-audio/index.ts`
  (contract), `web/src/features/media/index.ts` (contract); P1 namespaces' stub JSON; P1
  feature dirs' stub `routes.tsx`/`slots.tsx`; `formats/explorer/index.ts` + stubs;
  `app/settingsNav.ts` P1 entries enabled.
- **Deliverables:**
  1. Pin P1 libraries (webpush-go, minio-go, x/image, gen2brain/webp, gen2brain/avif,
     kaptinlin/messageformat-go) and blank-import them in `internal/deps/deps.go`.
  2. Migrations `2001–2007` (plan §3.3).
  3. `config` P1 fields + `MediaEnabled/PushEnabled/DBSCEnabled` + the fail-closed matrix test.
  4. `core` additions (contract):
     `Cosign` (`Begin(ctx, v, purpose, target) (Intent, error)`;
     `Require(ctx, tx, v, purpose, kind RecordKind, body any, intent gate.Ref, assertion []byte) (Assertion, []byte, error)`),
     `Handshakes` (`OpenUpgrade(ctx, v, peer UserID) (gate.HandshakeView, error)`,
     `OpenRecovery(ctx, guardian, account UserID) (gate.HandshakeView, error)`,
     `ClaimRecovery(ctx, sess [32]byte, payload string) (account UserID, err error)`,
     `Result(ctx, hs uuid.UUID) (Outcome, error)`),
     `Media` (`Attach(ctx, tx, owner, refs) ([]uuid.UUID, error)`,
     `Views(ctx, s Search, ids []uuid.UUID) ([]gate.MediaView, error)`,
     `Ingest(ctx, img []byte, sourcePost PostID, sourceURL string) (uuid.UUID, error)`),
     `ModQueue` (`Enqueue(ctx, tx store.Q, item ModQueueItem) error`),
     `StepUp` (`Require(ctx, user UserID, session [32]byte, reason string) error`),
     `Prefer.KidsN` gens=2 semantics, `Graph.SetHideVia`, `Exclusions.HiddenVia`, `Audience`
     friend+, Bus events `SessionStepUp`, `EdgeUpgraded`, `HideViaChanged`; `core.Deps` gains
     `Cosign`, `Handshakes`, `Media`, `ModQueue`, `StepUp`; coreref implementations for each.
  5. Stub packages: `gate/{cosign,media,themes}`, `gate/signals/{behavior,automation}`,
     `api/{cosignapi,mediaapi,pushapi,signalsapi,themeapi}` with empty `Register`
     (+ `mediaapi.RegisterPublic` for `/m/{key}`), `internal/api/apix/pad` stub (passes to
     P1-W07), router updated once, `wire.App` P1 fields.
  6. Web: P1 namespaces (stub JSON), feature dirs with stub `routes.tsx`/`slots.tsx`
     (`recovery`, `saves`, `themes`, `push`, `media`, `cosign`, `handshake-audio`),
     `formats/explorer/index.ts` + stubs, the `HandshakeAudio` contract:
     ```ts
     export interface PlayBits { ultra: Uint8Array /* 4 B */; jingle: Uint8Array /* 3 B */ }
     export interface DecodeReport { ultraBits?: Uint8Array; jingleBits?: Uint8Array; tUltraMs?: number; tJingleMs?: number; offsetMs?: number; motifLevelDb: number }
     export interface AudioInput { read(frames: number): Promise<Float32Array> } // deterministic test seam
     export interface HandshakeAudio {
       play(bits: PlayBits, opts: { quiet: boolean }): Promise<{ startedAt: number }>;
       listen(opts: { timeoutMs: number; input?: AudioInput }): Promise<DecodeReport>;
     }
     export declare function createHandshakeAudio(ctx: AudioContext): HandshakeAudio;
     ```
     and the `features/media/index.ts` contract (`MediaPicker`, `useUpload`, `MediaImage`
     with URL refresh on 403).
- **Acceptance:** `check.sh` green; prod config with `PCC_SCANNER=dev` starts with media
  disabled; `PCC_SCANNER=cloudflare` refuses to start; commit `scaffold: phase 1 contracts`.

#### P1-W01: Custodial keys and passkey co-signing
- **Owns:** `internal/gate/{cosign,records}/**`, `internal/api/cosignapi/**`,
  `api/openapi/cosign.yaml`, `web/src/features/cosign/**`,
  `web/src/i18n/locales/en/cosign.json`, `web/e2e/p1-w01.spec.ts`,
  `testdata/fixtures/p1-w01.json`, migrations `3010–3019`.
- **Implements:** `core.Cosign`; `Signer` v2 (author key + server countersign).
  **Consumes:** `Passkeys.{BeginAssertion,VerifyAssertion}` (W07-owned auth, implemented
  since P0), `config.KEK`.
- **Deliverables:** per-user Ed25519 keys created at signup, AES-256-GCM-wrapped under KEK
  with AAD `key_id ‖ owner_id`; author-signed records + server countersign; intents whose
  drafts contain `nonce` (= intent id) and `issued_at`, consumed with
  `UPDATE … WHERE used_at IS NULL RETURNING` in the caller's transaction; assertion
  verification (exact origin, UV, credential ownership, counter, BE flag); `PasskeyChange`
  chain (each add after the first co-signed by an existing credential); export includes
  user public keys, assertions and the credential chain; the client co-sign dialog
  (shows the record being signed); the **signup ceremony** for invitees (fidelity #21):
  create the credential, then — in the same guest session, before the RegisterFinish
  transaction commits — run a `get` assertion with that new credential over the `Invite`
  and `Friendship` drafts; if it fails, the transaction aborts and nothing is created.
- **Acceptance:** forged edge without assertion detected by `VerifyExport`; assertion for
  another record/challenge rejected; replaying an assertion on an identical second draft
  rejected (different nonce ⇒ different challenge; used intent refused); origin mismatch,
  `crossOrigin: true`, `topOrigin` present, UV=0, another user's credential — each rejected;
  wrapped key moved to another user's row fails to unwrap; KEK missing in prod ⇒ startup
  refused; a passkey add not co-signed by an existing credential fails verification.

#### P1-W02: Handshake protocol v2, friend+ upgrade, invite diversity decay, P1 probation
- **Owns:** `internal/gate/invites/**`, `internal/api/invitesapi/**`,
  `api/openapi/invites.yaml`, `web/src/features/{invites,join}/**`,
  `web/src/i18n/locales/en/{invites,join}.json`, `web/scripts/gen-y4m.mjs`,
  `web/e2e/fixtures/camera.ts`, `web/e2e/p1-w02.spec.ts`, `testdata/fixtures/p1-w02.json`,
  migrations `3020–3029`.
- **Implements:** `core.Handshakes`, `core.Invites`, `core.Probation`. **Consumes:**
  `Cosign` (both sides of an invite), `HandshakeAudio` TS contract (via `AudioInput` in
  tests), `Passkeys`, `Signer`, `Edges`.
- **Deliverables:** 250 ms client-rendered frames (`frame_key`); E_i/E_v relay; the invitee
  device's separate non-extractable ECDSA key, every later join call signed over
  `(hs, server challenge)`; kmac + commit check; server MAC verify with the **time bound
  measured by the server** (`report_at − play_at`; client timings may only tighten it);
  attempts ≤ 5 per handshake; outcomes Verified/Accepted/Retry with hints; signup token
  bound to E_v; `RegisterFinish` sets the Friendship level from the outcome (**Verified ⇒
  friend+, Accepted ⇒ friend**, fidelity #20) and a later Verified upgrade handshake
  promotes it (`Edges.Upgrade`); dual-QR fallback (Accepted max); friend+ upgrade
  handshakes (kind 1); recovery claim handshakes (kind 2); SAS confirmation kept for every
  handshake kind; diversity multiplier job; 30 d + 3 friend+ probation for accounts created
  after the cut-over (mid-probation accounts finish under the P0 rule).
- **Acceptance:** outcome table exhaustive; Accepted invite creates a plain friend edge and
  a later Verified upgrade makes it friend+; photographed QR without e_v ⇒ 404; join call
  without a valid signature ⇒ 404; a report arriving after the server-side bound ⇒ Retry
  even if client timings claim otherwise; sixth attempt ⇒ 404; dual-QR never Verified;
  Playwright with fake camera + the `AudioInput` seam.

#### P1-W03: Graph P1: friend+ chains, hide-via, friend+-only audience rules, lazy BFS
- **Owns:** `internal/gate/{graph,visibility,friends,devseed}/**`,
  `internal/api/friendsapi/**`, `api/openapi/friends.yaml`, `web/src/features/people/**`,
  `web/src/i18n/locales/en/people.json`, `web/e2e/p1-w03.spec.ts`,
  `testdata/fixtures/p1-w03.json`, migrations `3030–3039`.
- **Implements:** `Graph.SetHideVia`, lazy search, `Chains` v2 (`FirstHopPlus`).
  **Consumes:** `Cosign` (request/accept/unfriend/block), `Handshakes.OpenUpgrade`.
- **Deliverables:** friend+ first-hop preference in the D22 rank; hide-via exclusions
  (transit only, epoch bump); `friend_plus` audience in `CheckPost`; co-signed friend
  request/accept/unfriend/block via `core.Cosign.Require`; upgrade entry point; lazy BFS +
  30-min cache keyed by `(viewer, epoch)` behind `PCC_LAZY_BFS`, deep tiers released at a
  fixed latency bucket, `graph.deep_search` metric.
- **Acceptance:** hide-via never removes the hidden friend's own posts, only paths through
  them; cached search invalidated by any removal; lazy == full BFS on random graphs;
  first hop prefers a friend+ edge over an older plain edge; graph-changing writes without
  a fresh assertion fail; `friend_plus` posts invisible to plain friends.

#### P1-W04: Media pipeline and CSAM handling
- **Owns:** `internal/platform/{blob,scan,imageproc}/**`, `internal/gate/media/**`,
  `internal/api/mediaapi/**`, `api/openapi/media.yaml`, `web/src/features/media/**`
  (except `index.ts`), `deploy/cloudflare/**`, `deploy/runbooks/{csam,media}.md`,
  `testdata/scan/**`, `web/src/i18n/locales/en/media.json`, `web/e2e/p1-w04.spec.ts`,
  `testdata/fixtures/p1-w04.json`, migrations `3040–3049`.
- **Implements:** `core.Media`; `platform/{blob,scan,imageproc}`. **Consumes:**
  `config.MediaEnabled`, `Limiter` (`media`, `media_full`), `ModQueue`.
- **Deliverables:** plan §4.7 media; scan-before-store; evidence preservation for 1 year;
  account hold on a match; preview-image matches handled identically (hold + priority-0
  item + source URL recorded); moderators see match metadata only; WebP variants;
  `DecodeConfig` bounds; signed per-viewer URLs (1 h, fixed-width MAC input) + dev
  `/m/{key}` verifier + Worker (HMAC before cache, private bucket, purge on delete,
  media-origin headers); `media_full` budget; fail-closed config; the **NCMEC runbook**
  (`deploy/runbooks/csam.md`: CyberTipline report, 1-year preservation, who may access,
  break-glass) and the `csam_report` moderator action that fills `report_ref` (endpoint
  in modapi is P1-W06's; W04 provides `core.Media`-side state changes it calls).
- **Acceptance:** prod config with `PCC_SCANNER=dev` or `none`, or without an evidence
  store, ⇒ upload `feature_disabled`; scanner error or timeout ⇒ rejected, nothing stored
  anywhere; matched image ⇒ evidence object + hold + queue item, uploader's next upload ⇒
  `media_rejected`; preview image matching the dev blocklist ⇒ hold + queue item with
  `source_url`; EXIF GPS absent in all variants; a 50,000×50,000 PNG header is refused
  before decode; expired/unsigned/other-viewer-tag URL ⇒ 403; blob keys are not content
  hashes.

#### P1-W05: Web Push and server i18n
- **Owns:** `internal/platform/{webpush,i18n}/**`, `internal/gate/{notify,metrics}/**`,
  `internal/api/{notifyapi,pushapi}/**`, `api/openapi/{notify,push}.yaml`,
  `web/src/sw/push.ts`, `web/src/api/sse.ts`, `web/src/features/{push,notifications}/**`,
  `web/src/i18n/locales/en/{push,notifications}.json`, `web/e2e/p1-w05.spec.ts`,
  `testdata/fixtures/p1-w05.json`, migrations `3050–3059`.
- **Implements:** `Notifier` v2 (push fan-out). **Consumes:** `Bus` `SessionEnded`
  (unsubscribe that session's endpoint), `platform/i18n` (own).
- **Deliverables:** VAPID sender, `push.deliver` job (drop on 404/410), server-rendered
  text in the recipient's `ui_locale` from catalogs (kaptinlin/messageformat-go), no post
  bodies in payloads, "hide content on lock screen" option, duplicate endpoint reassigned to
  the current user/session, Go no-prose check extended to push payload builders,
  subscription UI.
- **Acceptance:** push disabled without VAPID ⇒ `{enabled:false}`; payload text from the
  catalog in an `en-XA` test; subscription removed on logout; re-subscribing an endpoint
  owned by another account moves it (the other account stops receiving); `hide_content`
  payloads contain only the generic string.

#### P1-W06: Moderation queue, quarantine, behavioral signals
- **Owns:** `internal/gate/{moderation,signals/behavior}/**`, `internal/api/modapi/**`,
  `api/openapi/mod.yaml`, `web/src/features/moderation/**`,
  `web/src/i18n/locales/en/moderation.json`, `web/e2e/p1-w06.spec.ts`,
  `testdata/fixtures/p1-w06.json`, migrations `3060–3069`.
- **Implements:** `core.ModQueue`; behavioral jobs. **Consumes:** `Graph`, `Users`,
  `Invites.Subtree`, `Probation.Reset`, `Notifier`, `Cosign` (second-moderator
  assertion over the JCS `ModAction` draft, security #30).
- **Deliverables:** unified `mod_queue` (reports, CSAM, signals, step-up repeats), claim/
  resolve, two-person actions with the second moderator's passkey assertion, subtree
  quarantine (≥ `PCC_QUARANTINE_K` confirmed ⇒ subtree probation, inviter paused 30 d,
  second offense revokes), removed-account marks (90 d; visible only to users in the stored
  `viewers` bitmap and moderators — fidelity #19), `csam_report` action, nightly cadence/
  similarity/invite-cluster jobs on `net_h` only, `signals.purge` (30 d), blame inbox page.
- **Acceptance:** no blame/mark without moderator confirmation; quarantine affects only the
  subtree; a removed-account mark is invisible to a user who joined the component after the
  removal; similarity job never stores bodies (MinHash only); no raw IP column exists;
  second-moderator approval with the first moderator's credential fails.

#### P1-W07: Device-bound sessions, automation signals, step-up triggers, timing padding, limits v2
- **Owns:** `internal/gate/{auth,limits,signals/automation}/**`,
  `internal/platform/dbsc/**`, `internal/api/{authapi,signalsapi}/**`,
  `internal/api/apix/pad/**`, `api/openapi/{auth,signals}.yaml`,
  `web/src/features/{auth,stepup}/**`, `web/src/i18n/locales/en/{auth,stepup,signals}.json`,
  `web/e2e/p1-w07.spec.ts`, `testdata/fixtures/p1-w07.json`, migrations `3070–3079`.
- **Implements:** step-up triggers + DBSC in `auth`, `core.StepUp`, `limits` v2, `apix/pad`.
  **Consumes:** `Cosign.Require` (passkey add/remove only — the general rule is P1-W01's).
- **Deliverables:** DBSC registration/refresh (Chromium): two cookies — the long-lived
  `__Host-pcc` is accepted **only** at `/api/auth/dbsc/refresh`; every other request from a
  bound session needs the 10-minute bound cookie `__Host-pcc-b`; a bound session never
  accepts an unbound request; single-use challenges; `aud` check; ES256 + RS256. Short
  sessions elsewhere (12 h, re-verify). Client signals (counts only), request-pattern
  scoring via an `apix` hook, signal-triggered step-up. Timing padding middleware over the
  derived padded set (bucket rounding, error paths included) + KS and histogram test
  helpers.
- **Acceptance:** keyboard-only and screen-reader Playwright profiles never trigger step-up;
  webdriver + sustained rate ⇒ step-up; a DBSC-bound session's long-lived cookie replayed
  against `/api/feed` from another context fails; a bound cookie past 10 min fails; a
  replayed refresh challenge fails; an RS256 key registers; padded routes' found/not-found
  durations fall in the same bucket.

#### P1-W08: Ranking P1: saves, grandchildren credit, Top
- **Owns:** `internal/gate/{rank,engage,feed,tree}/**`,
  `internal/api/{engageapi,feedapi,treeapi}/**`, `api/openapi/{engage,feed,tree}.yaml`,
  `web/src/features/{feed,seen,saves,engage}/**`,
  `web/src/i18n/locales/en/{feed,seen,saves,engage}.json`, `web/e2e/p1-w08.spec.ts`,
  `testdata/fixtures/p1-w08.json`, migrations `3080–3089`.
- **Implements:** `Prefer` v2 (saves, grandchildren). **Consumes:**
  `Visibility.VisibleChildren`.
- **Deliverables:** save/unsave + saves page, signal weights (like 1, save 2, cap 4), K²=400
  grandchildren credit with memoized bitmap intersections (each level filtered before
  taking K), Top preset in API + UI (lifts `preset_unavailable`), reasons updated.
- **Acceptance:** saves never displayed or counted in any view; Top = λ1 params;
  grandchildren from invisible links never count; adding invisible grandchildren never
  changes `KidsN`; a slippery parent still gets no credit.

#### P1-W09: Explorer format, themes, desktop minimap, integrity components
- **Owns:** `web/src/formats/explorer/**` (except `index.ts`),
  `web/src/features/{themes,tree}/**`, `web/src/formats/cards/TreeLayout.tsx`,
  `web/src/formats/cards/tree/**`, `web/src/formats/compact/TreeLayout.tsx`,
  `web/src/formats/compact/tree/**`, `web/src/integrity/**` except `types.ts` and
  `index.ts`, `internal/gate/themes/**`, `internal/api/themeapi/**`,
  `api/openapi/theme.yaml`, `web/src/i18n/locales/en/{themes,explorer,tree,integrity}.json`,
  `web/e2e/p1-w09.spec.ts`, `testdata/fixtures/p1-w09.json`, migrations `3090–3099`.
- **Deliverables:** token-only themes validated server-side (WCAG AA for integrity elements,
  size floor, font allowlist), Explorer canvas + outline mirror, desktop minimap.
- **Acceptance:** integrity presence suite passes for Explorer; INT-1's Playwright
  visibility/contrast pass covers every theme preset; theme with a low-contrast tier header
  rejected (`theme_contrast`).

#### P1-W10: Account P1: friend+ guardian recovery, co-signed deletion and recovery
- **Owns:** `internal/gate/{account,users}/**`, `internal/api/accountapi/**`,
  `api/openapi/account.yaml`, `web/src/features/{recovery,settings,modrecovery}/**`,
  `web/src/i18n/locales/en/{recovery,settings,account,modrecovery}.json`,
  `web/e2e/p1-w10.spec.ts`, `testdata/fixtures/p1-w10.json`, migrations `3100–3109`.
- **Consumes:** `Handshakes.OpenRecovery/ClaimRecovery`, `Cosign`, `Passkeys`, `Notifier`.
- **Deliverables:** guardian-initiated in-person recovery (claim via `core.Handshakes`, 2nd
  guardian approval with assertion, 24 h hold, cancel from any live session, friends
  notified), the shared `recovery.release` job (also used by manual recovery), co-signed
  account deletion.
- **Acceptance:** recovery needs 2 distinct friend+ guardians; cancel during hold works;
  non-guardians cannot approve (uniform 404); released recovery revokes all sessions.

#### P1-W11: Preview images and Markdown tables
- **Owns:** `internal/platform/{safefetch,urlx,mdscan}/**`, `internal/gate/previews/**`,
  `testdata/markdown/**`, `testdata/tracking-params.json`, `web/src/markdown/**`,
  `web/src/i18n/locales/en/markdown.json`, `deploy/runbooks/egress.md`,
  migrations `3110–3119`.
- **Consumes:** `Media.Ingest`.
- **Deliverables:** `og:image` fetch via `GetImage` (same SSRF policy, image types only,
  size cap) → `Media.Ingest` (scan) → `image_media_id`; GFM tables (≤10 cols) both sides.
- **Acceptance:** preview image never fetched by viewers; a CSAM-matched preview image is
  **held and queued** (never silently dropped) and the preview shows no image; table
  fixtures agree in Go and TS.

#### P1-W12: Posts P1: media attachments, friend+-only audience, views
- **Owns:** `internal/gate/{posts,postindex,views}/**`, `internal/api/postsapi/**`,
  `api/openapi/posts.yaml`, `web/src/features/{post,composer}/**`,
  `web/src/formats/cards/{FeedLayout,PostLayout}.tsx`, `web/src/formats/cards/feed/**`,
  `web/src/formats/compact/{FeedLayout,PostLayout}.tsx`, `web/src/formats/compact/feed/**`,
  `web/src/i18n/locales/en/{post,composer}.json`, `web/e2e/p1-w12.spec.ts`,
  `testdata/fixtures/p1-w12.json`, migrations `3120–3129`.
- **Implements:** `Views` v2. **Consumes:** `Media.Attach/Views`; renders
  `PreviewView.Image` from `link_previews.image_media_id` written by P1-W11.
- **Deliverables:** ≤4 media per post (owned, ready), `friend_plus` audience option, media in
  `PostView` and in card/compact layouts, Post record lists media ids, composer attach via
  `features/media` (`MediaPicker` contract).
- **Acceptance:** media of an invisible post ⇒ URL request 404; friend+ audience hidden from
  plain friends; another user's media ref cannot be attached.

#### P1-W13: Handshake audio codec and sound design
- **Owns:** `web/src/features/handshake-audio/**` (except `index.ts`),
  `web/src/i18n/locales/en/handshake.json`, `docs/sound/**`.
- **Deliverables:** pure TS: jingle synth (3-note motif + 8 × 3-bit notes over a fixed
  rhythm, 400–4000 Hz, one 8-tone scale), ultrasonic FSK (18–20 kHz, 32 bits) mixed in one
  ~1.5 s buffer; Goertzel decoder + FSK demod + sync offset; volume-low detection; quiet
  mode; implements the `HandshakeAudio` contract and an `AudioInput` from a WAV for INT-1.
- **Acceptance:** Vitest: round-trip decode at SNR 10 dB with a speaker roll-off filter
  > 99% (jingle) / > 95% (ultrasonic); ≤ 1.6 s; 1,000 random tails all within the scale.

Count: 13 packages (one over the 8–12 target) because P1-W13 is pure TypeScript signal
processing with no server overlap; merging it into W02 would serialize the riskiest
research task behind the protocol work.

#### INT-1: Phase 1 integration
- **Owns:** `internal/gate/conformance/**`, `web/e2e/journeys/**`,
  `web/e2e/integrity.spec.ts`.
- **Deliverables:** P1 conformance additions (KS timing over the whole padded set,
  co-sign forgery, media URL expiry, a11y never steps up, DBSC replay, fail-closed config
  matrix against a real binary), the real fake-audio journey (P1-W13 encoder → WAV →
  Chromium fake audio → P1-W02 decoder), Phase 1 scope list (§18) ticked with evidence.

---

## 9. Process: worktrees, merge queue, contract steward

### 9.1 Worktrees and merge queue
1. After SCAFFOLD-0a and 0b are merged, the orchestrator runs `scripts/wt.sh new <id>` for
   each WP: `git worktree add ../pcc-wt/<id> -b wp/<id> <scaffold-commit>` + `npm ci`.
2. A WP is "done" when `scripts/check-wp.sh <id>` is green in its worktree.
3. The merge queue (one agent, serial): `git rebase main` in the WP's worktree → global
   `scripts/check.sh` → fast-forward `main`. Failure bounces the WP back with the log. WPs
   whose files are disjoint rebase cleanly by construction; the only shared inputs are
   steward-owned contract files, which only ever grow additively.
4. Ports, databases and static dirs are per-run (plan §7), so the merge queue's check and
   any number of WP checks can run at the same time on one machine.

### 9.2 What WPs never do
Run `go get`, `go mod tidy`, `npm install`, `npm ci` (except the one `wt.sh` run), edit
`go.mod`/`go.sum`/`package*.json`, edit a file they do not own, drop or force-drop a
database they did not create, bind a fixed port, or write into `web/dist` outside a full
`npm run build`.

### 9.3 Contract steward (buildability C3)
During each parallel phase one agent (or the orchestrator) owns every scaffold-owned file.
It reads `docs/contract-requests/*.md`, and for **additive** changes — a new method on a
new interface, a new optional view field, a new Bus event type or field, a new nullable
column in the scaffold range, a new route that fits plan §5.2, a new error code, a new i18n
namespace — it patches `main` the same day, updating coreref so the new surface works
immediately. WPs rebase to pick it up. Anything that changes an existing signature or
meaning waits for INT, and the requesting WP uses a local unexported adapter until then
(allowed only for *its own* code — never a cross-domain import, which A8 forbids).

---

## 10. Decisions log (where this plan resolves something ARCHITECTURE leaves open)

| # | Decision | Why |
|---|---|---|
| D1 | All domain modules live under `internal/gate/…`; handlers in `internal/api/…`; IDs and `Viewer` in `gate/internal/core` | Makes §13 compiler-enforced (Go `internal`), not only lint-enforced |
| D2 | Viewer travels only in `context.Context` | Handlers can't leak or forge IDs |
| D3 | Each `Post` version signs `body_commit = sha256(salt ‖ utf8(NFC body))` with a per-version random salt; body and salt are hard-deleted together; exports ship `{body, salt}` | §10.2 hard delete must actually delete text, and a bare hash would be a confirmation oracle for short posts (security #25) |
| D4 | Refs: version byte ‖ AES-SIV with `version‖viewer‖kind` as associated data; never minted for viewer 0 | §13 unlinkability, kind confusion impossible, key rotation possible, no guest-shared ref space |
| D5 | Feed/tree snapshots in memory (LRU 30 min) keyed by `(viewer, random id)`, cursors HMAC'd with the viewer, **re-checked for visibility on every page** | Single binary; restart = new session; snapshot ids are not capabilities; §4.6 "next load" holds mid-session |
| D6 | Probation-era posts are stored as `d1` with `audience_forced` **permanently** (needs Corbin's call, plan §11) | Widening them when probation ends would visibly flip the audience badge on old posts and reveal to friends that the author had been on probation, which §8 restricts to moderators and the inviter. Forced `d1` renders byte-identically to a voluntary `d1` |
| D7 | **Blocks are one-directional for visibility**: the reader's search excludes only the reader's own blocks (and hide-vias). Being blocked hides nothing from you; it makes your references shadow links, silences your notifications/requests to the blocker, and keeps you out of the blocker's paths | §4 rule 2, §3 "never shown to the blocked person", §10.1.1 shadow links (fidelity #1). Replaces the draft's symmetric rule |
| D8 | Declined requests stored as state 1, rendered as "outgoing" to the requester; requests to a blocker are stored as declined at creation | §10.6 "a declined request looks exactly like a pending one"; blocks never revealed |
| D9 | P1 handshake: invitee reports `kmac=HKDF(S)`, inviter reports `sha256(kmac)` + decoded bits; the server verifies MACs and a **server-measured** time bound | The server scores without seeing audio; a relay that swaps keys fails the commit check; §7 "no self-reported calibration" |
| D10 | Friend+ recovery is guardian-initiated and in person (claim by scanning the guardian's QR) | No handle lookup oracle for the unauthenticated requester |
| D11 | Hand-written SQL per gate package; no sqlc/ORM | No shared generated file for parallel agents to fight over |
| D12 | OpenAPI as per-module fragments; Go tests merge them with kin-openapi, Node merges them for the TS client (gitignored) | No shared hand-edited spec file and no Go→Node test dependency |
| D13 | TypeScript pinned to ~6.0 with an `overrides` entry for openapi-typescript | typescript-eslint 8.71 peer `<6.1`; openapi-typescript peer `^5` verified to work under 6.0.3 |
| D14 | WebP is the shipped image format in P1; AVIF behind a flag | Pure-Go AVIF encoding is slow; WebP covers every target browser |
| D15 | Post language guess = first entry of the user's `languages[]`, then the UI locale's primary subtag; `und` and manual choice always available; on-device model deferred | §10.7 only requires `lang` recorded from Phase 0; `languages[]` is what the user said they write/read |
| D16 | The reader's own posts (tier 0) are never feed candidates; after publishing, the client prepends the new post locally (not part of any snapshot) | §4 tiers start at 1; keeps snapshots and "unseen first" exact |
| D17 | §13 rule checked at the consumer (A5, exported surface only) plus domain isolation (A8) and the `Checked` capability (A10) | Producer-side signature rules are unworkable; caller discipline is not a guarantee |
| D18 | Trusted Types with two policies; SW registered by our own code (`injectRegister: false`); dev-only extra script-URL policy | Workers and the SW need `TrustedScriptURL`; the PWA plugin's auto-register breaks under TT |
| D19 | **Every** request is charged in `apix.Route` before the handler and before ref resolution — reads and writes alike | Uniform charging (§17.2(3)); a write budget that moves only for valid refs would be an existence oracle |
| D20 | Fail-closed P1 integrations: media only with PhotoDNA + blob + evidence store + media-URL key; push only with VAPID; missing/unknown `PCC_ENV`, unknown enum values, weak/duplicate keys, missing KEK in prod ⇒ refuse to start; `PCC_SCANNER=dev|none` in prod ⇒ media `feature_disabled` | Media must never be stored unscanned or unpreservable; prod must never silently run with dev defaults |
| D21 | `handle_taken` is an accepted, documented H(V) exception: reachable only inside a confirmed (SAS) in-person handshake, ≤10 answers per handshake, counted against the inviter's handshake budget (needs Corbin's call, plan §11) | Handles are globally unique, so registration must say when one is taken; the mitigations make it useless as a lookup oracle |
| D22 | Chain first hop: `rank = (friend+ first [P1], older reader edge, lower ID)` propagated during BFS relaxation with replacement on a better rank; parents of middle hops by lower ID | Order-independent and equal to the brute-force "best first hop among all shortest paths" (fidelity #10) |
| D23 | Friend cap counts `edges + outgoing pending requests`, checked only against the caller's own numbers | Accept can never fail because of the other person's degree, so it never leaks it (fidelity #11) |
| D24 | P0 handshake adds a 6-digit SAS confirmed by the inviter before registration | A bystander who photographs the QR can't take the invite (security #6) |
| D25 | No `feed_new` SSE event; the client polls `/feed/new` every 60 s | Fan-out of new posts to a whole component is unbounded and visibility-sensitive (fidelity #24) |
| D26 | Manual recovery runs in the moderator UI with two fresh step-ups and a 24 h hold, never via CLI | A CLI that prints enrollment links lets any operator take over accounts (security #14) |
| D27 | Sensitive actions need a passkey step-up within 5 minutes from Phase 0 (passkey add/remove, export, delete, two-person moderation) | A stolen cookie must not become persistent takeover (security #8) |
| D28 | Search over visible posts and "introductions by a mutual friend" (§10.5) are **deferred** (not in the §18 Phase 0/1 scope lists) | Keeps P0/P1 to §18; both are horizon-safe to add later on top of `Visibility` (fidelity #16) |
| D29 | Age gate = attestation of `PCC_MIN_AGE` (default 16) plus ToS version acceptance, recorded at join and genesis enrollment; no birth date stored | §13 compliance with data minimization (fidelity #17) |
| D30 | Revoked `Friendship`/`Revocation` records are purged 30 days after revocation; the `Block` record is deleted on unblock; declined requests keep a timestamp-free row | §2.3 retention must cover the signed records too (security #16); the row keeps "declined looks pending" working |
| D31 | Hides are kept for **one year** (`engage.HideRetention`, purged by `engage.hides_purge`), not 30 days (product decision, INT-0) | Hides filter family-tree rows too (D32), and a tree can surface a post long after it left the feed window; a hidden post must stay hidden |

---

## 11. Needs Corbin's call

The plan proceeds with the recommended default for each; changing one later is local to
the named WP.

| # | Question | Default in this plan | Owner if changed |
|---|---|---|---|
| Q1 | Do probation-era posts widen when probation ends (§4 rule 5 "until probation ends")? | No — they stay `d1` (D6), because widening reveals past probation to friends | P0-W05 / P0-W02 |
| Q2 | Is `handle_taken` at registration an acceptable disclosure (a confirmed in-person invitee learns that a handle exists somewhere)? | Yes, with the D21 mitigations; alternative is random handle suggestions only | P0-W04 |
| Q3 | Minimum age for the attestation | 16 (`PCC_MIN_AGE`) | config only |
| Q4 | Search over visible posts and introductions: which phase? | Deferred past Phase 1 (D28) | — |
| Q5 | Strict "unseen first" (ARCHITECTURE §19) | Kept strict | — |

---

## Appendix A. Critique dispositions

Every point from the three critiques. "Accepted" names where the plan now handles it.

### A.1 Fidelity critique
| # | Point | Disposition |
|---|---|---|
| 1 | Symmetric blocks break shadow links and reveal blocks | Accepted: D7 one-directional; `CanSeeUser`/`CheckPost` (§4.7 visibility); shadow rule in posts; notifications dropped; requests to blockers silently declined; tests in §7 and W02/W03/W05 |
| 2 | `KidsN` window counts invisible children | Accepted: `VisibleChildren` filters before the limit; `Prefer.KidsN` contract; property test |
| 3 | Slippery post must lose reply credit | Accepted: `KidsN` nil for `PolNobody`; W06 test |
| 4 | Snapshots served without re-checking visibility | Accepted: per-page re-check (feed, tree), D5; W07/W08 acceptance; §7 freshness property |
| 5 | Contradictory scanner fail-closed rules | Accepted: one rule (D20); `cloudflare` removed; `MediaEnabled` requires evidence store + URL key; config matrix test |
| 6 | Phase 0 metrics missing | Accepted: `gate/metrics`, `metrics_daily`, `pcc metrics`, closed names, logging policy A11 (W10) |
| 7 | Write charging order is an oracle | Accepted: D19, write classes in `apix`, counter-delta acceptance |
| 8 | Padding coverage too narrow | Accepted: padded set derived from route params/body schemas (§5.1) |
| 9 | Noninterference false failures; inviter labels; component isolation | Accepted: `ids.Named`, token normalization, H(V) held fixed incl. inviter labels and 2-hop likes/probation, isolation scan (§7) |
| 10 | Chain first-hop propagation unspecified | Accepted: D22 rule + `ReferenceFirstHop` oracle |
| 11 | Requests/accepts reveal blocks or caps | Accepted: requests to blockers stored declined; D23 cap rule |
| 12 | `handle_taken` cross-component oracle | Accepted as documented exception D21 + plan §11 Q2 |
| 13 | Probation visibility for inviter/moderators; D6 | Accepted: `/invites/invitees`, `ModItem.SubjectProbation`; D6 kept with reason and flagged (Q1); forced `d1` renders identical |
| 14 | "Show less" doesn't affect ranking | Accepted: personal show-less penalty in `rank` (outside clamp, λ-scaled) |
| 15 | Tree tie-breaks missing | Accepted: `rank.Less` shared by feed and tree |
| 16 | Introductions and search absent | Accepted as explicit deferral D28 / Q4 |
| 17 | Age gate and ToS acceptance missing | Accepted: D29, users columns, join/enroll steps |
| 18 | Free-text moderation reasons | Accepted: CHECK enums, `core.ModReason`, A7 |
| 19 | Removed-account mark audience not implementable | Accepted: `viewers` bitmap snapshot of the component at removal (P1-W06) |
| 20 | P1 invite friendship level | Accepted: level from outcome, upgrade promotes (P1-W02) |
| 21 | P1 co-signing at signup ceremony | Accepted: create → get-assertion over drafts → commit (P1-W01) |
| 22 | Session cookie SameSite | Accepted: `SameSite=Strict` pinned and tested (httpx) |
| 23 | Preview timing observable | Accepted: two fixed buckets, else failed (previews, schema) |
| 24 | `feed_new` SSE fan-out | Accepted: dropped, polling (D25) |
| 25 | Chains bypass `graph_read` | Accepted as documented: inline middles per §4.5, bounded by feed/post budgets (PostView comment) |
| 26 | 204 with a body | Accepted: ref-policy returns 200 |
| 27 | `CanReference` wording | Accepted: rewritten (§4.7 visibility) |
| 28 | Language guess inconsistent | Accepted: D15 revised |
| 29 | Tree folding missing | Accepted: W08 deliverable + acceptance |
| 30 | P0 must reject `top` | Accepted: `preset_unavailable` |
| 31 | Chain verification from signed edges | Accepted: `AuditEdges` CLI + weekly job + INT check |
| 32 | Quarantine k; P1 probation cut-over | Accepted: `PCC_QUARANTINE_K=3`; mid-probation accounts keep the P0 rule |
| 33 | Bot-isolation simulation | Accepted: §7 simulation (INT-0) |
| 34 | Second passkey at signup | Accepted: optional step in join and enroll |
| 35 | NCMEC workflow | Accepted: runbook + `csam_report` action filling `report_ref` (P1-W04/W06) |

### A.2 Buildability critique
| # | Point | Disposition |
|---|---|---|
| A1 | Whole-repo checks can't hold in one tree | Accepted: worktree per WP + merge queue (§0, §9), `check-wp.sh`, per-module `apitest.Service`. Rejected sub-point: "archtest skips packages that fail to type-check" — it would hide real violations, and worktrees remove the need |
| A2 | Infrastructure stubs break everyone | Accepted: scaffold ships working session/CSRF/guard, pass-through limiter, SPA serving; stubs delegate to `coreref` |
| A3 | Per-WP e2e needs other WPs | Accepted: `/api/_test/login` + per-WP fixtures; journeys in INT; `AudioInput` seam for P1-W02 |
| A4 | Ports, DB, `web/dist`, generated files collide | Accepted: per-run port/DB/static dir, built binary, atomic generators under a lock, worktrees |
| A5 | Dependency manifests unowned | Accepted: `internal/deps`, plan §0.6, §9.2 |
| A6 | Test DB capacity | Accepted: MaxConns 4, `-p 4`, `max_connections=400`, safe `db-reset.sh` |
| A7 | Silent skips, `-race`, missing golangci-lint | Accepted: default URL + `PCC_REQUIRE_DB`, `-race` CI-only, scaffold installs the linter |
| B1 | `Memo` unusable | Accepted: exported `Get/Put` |
| B2 | Undefined contracts | Accepted: `Credential`, all Bus events, `SettingsPatch`, `KeyView`, `VerifyReport`, `PushSub`, `ModItem`, `BlameItem`, `LogItem`, integrity props |
| B3 | coretest inconsistent | Accepted: `coreref` reference implementations incl. visibility/prefer; World writes DB + memory |
| B4 | npm `ERESOLVE` | Accepted: `overrides` |
| B5 | `react/no-danger` without plugin | Accepted: `no-restricted-syntax` |
| B6 | Missing type packages | Accepted: `@types/react`, `@types/react-dom`, `@types/node` |
| B7 | Go tests depend on Node output | Accepted: Go-side merge with kin-openapi, per-fragment validation |
| B8 | A5 over-broad | Accepted: exported surface only |
| B9 | Route pattern ambiguity | Accepted: patterns relative to `/api` |
| B10 | Shared-package prefix ownership | Accepted: separate packages (`signals/behavior`, `signals/automation`, `themes`, `apix/pad`); assertion verification lives in `auth` from P0, so no `assert*.go` split |
| C1 | Critical-path W05/W11 | Accepted: `users` complete in scaffold; `views` moved to W02; integrity components to W10 |
| C2 | SCAFFOLD-0 serial | Accepted: 0a (Go) ∥ 0b (web); camera fixture to W04. Not deferred: A9 is cheap and already specified |
| C3 | Contract requests only at INT | Accepted: contract steward (§9.3) |
| C4 | Co-sign rule owned thrice | Accepted: P1-W01 owns `Cosign.Require`; callers listed |
| C5 | No table ownership | Accepted: §3.4 matrix, additive nullable migrations, no `SELECT *`, `hide_via` created in P0 |
| D1 | Noninterference false failures from IDs/tokens | Accepted: `ids.Named` + first-seen normalization |
| D2 | Explicit IDs collide with identity | Accepted: high range + `setval` |
| D3 | Perf assertions flake | Accepted: `-tags perf` only |
| D4 | Contrast in jsdom | Accepted: Playwright check |
| D5 | Markdown parity | Accepted: scheme-only autolinks, code-point offsets and counts |
| E | `gotnospirit/messageformat` unmaintained | Accepted: `kaptinlin/messageformat-go` v0.8.6 |
| E | `jsqr` unmaintained → `zxing-wasm` | Rejected: wasm needs `'wasm-unsafe-eval'` in `script-src`, weakening the CSP; jsqr is small, pure JS and works |
| E | precis has no confusables | Accepted: documented as mixed-script detection only |
| E | i18next lint gap | Accepted: covered by the pseudo-locale pass (documented) |
| E | TT + SW registration | Accepted: `injectRegister: false`, own registration, dev policy |
| E | Old CI action majors | Accepted: current majors pinned by SHA at scaffold time |
| E | Line endings | Accepted: `.gitattributes` |
| F | `MediaEnabled` without evidence store | Accepted (D20) |
| F | `cloudflare` scanner | Accepted: removed |
| F | `PCC_ALLOW_FS_BLOB` escape hatch | Accepted: only with a real scanner, WARN on every start |

### A.3 Security critique
| # | Point | Disposition |
|---|---|---|
| 1 | Missing `PCC_ENV` ⇒ dev mode in prod | Accepted: required, allowlist `DevLike()`, loopback rule, hooks only in `test` |
| 2 | Snapshots/cursors/tokens not viewer-bound | Accepted: D5 |
| 3 | Envelope fields outside the signature | Accepted: kind/schema_version in body; domain-separated message with key_id |
| 4 | JCS value grammar | Accepted: grammar enforced in `sigs.Canonical`, idempotence assert, no re-canonicalization |
| 5 | Passkey assertions not verifiable offline | Accepted: COSE key in `PasskeyChange`, P1 credential chain, exported |
| 6 | Handshake claim race | Accepted: SAS (D24) |
| 7 | CSAM fail-open path and legal gap | Accepted: no cloudflare driver; preview matches held + queued with source URL; 1-year retention; metadata-only moderator view; account hold |
| 8 | Passkey add/remove without re-auth | Accepted: D27, `excludeCredentials`, `sessions.credential_id` revocation |
| 9 | Step-up/co-sign must use own credential | Accepted: `Passkeys` assertion check list |
| 10 | Deterministic co-sign challenges | Accepted: nonce + issued_at in drafts; atomic consume |
| 11 | Login ceremony state | Accepted: guest session, 2 min, single use, fresh token, status refusal |
| 12 | CSRF/fetch-metadata on the whole router | Accepted: `APIGuard` on `/api` only; unauthenticated POSTs checked; CSRF from `csrf_seed`; SSE closed on `SessionEnded` |
| 13 | Enrollment tokens in URL paths | Accepted: `/enroll#token`, session binding, atomic use, route-pattern logging |
| 14 | Manual recovery operator takeover | Accepted: D26 (UI, two fresh step-ups, 24 h hold, cancel, revoke sessions, audit) |
| 15 | DBSC bypass | Accepted: two cookies, bound sessions never unbound, single-use challenge, `aud`, RS256 |
| 16 | Retention vs signed records | Accepted: D30 |
| 17 | Write charges inside services | Accepted: D19 |
| 18 | Visibility by caller discipline | Accepted: `Checked` capability, `VisibleChildren`, archtest A10 |
| 19 | Padding floor only, too few routes | Accepted: bucket rounding, error paths, derived route set, histogram assertion |
| 20 | Oracle edge cases | Accepted: requests to blockers, `handle_taken` documented, check order rule (§0.8). "Collapse hops held by anyone who blocked V" — moot under D7 (no person page 404s for blockers, so nothing to reveal) |
| 21 | Step-up error code | Accepted: `RequireUser` returns `ErrStepUp` |
| 22 | SSRF gaps | Accepted: full list in `safefetch` + W09 acceptance + egress runbook |
| 23 | Preview/identity spoofing | Accepted: `CleanText`, reserved glyphs, handle always shown, client strips tracking params |
| 24 | Markdown links | Accepted: protocol check, no titles, server-issued mention refs, `X-Frame-Options`, CSP `report-to` |
| 25 | `body_sha256` oracle | Accepted: salted commit (D3) |
| 26 | P1 handshake timing from client | Accepted: server-measured bound, attempts ≤ 5, separate ECDSA key, signed join calls |
| 27 | Media signed URLs | Accepted: fixed-width MAC, private bucket, HMAC before cache, purge on delete, media-origin headers, `DecodeConfig` |
| 28 | `RealIP` without config | Accepted: `PCC_TRUSTED_PROXIES`, `PCC_BEHIND_CDN` validation |
| 29 | Key handling | Accepted: AAD on wrapped keys, ref version byte, strict base64, no viewer-0 refs, HKDF-derived distinct keys + validation, RPID suffix check |
| 30 | Two-person moderation undefined | Accepted: P0 fresh step-up of the second moderator; P1 assertion over the JCS draft |
| Low | Logging policy | Accepted: archtest A11, `logx` |
| Low | Cross-account IP correlation | Accepted: `net_h` HMAC under a daily salt, 30-day retention |
| Low | Export via GET | Accepted: POST with fresh step-up |
| Low | `feed_new` SSE | Accepted: removed (D25) |
| Low | Push lock-screen content; duplicate endpoints | Accepted: `hide_content`; endpoint reassignment |
| Low | Body size limits | Accepted: per-route limits (§5.1) |
| Low | WebAuthn sign_count / BE flag / RPOrigins | Accepted: schema comments + assertion checks; exactly one origin per environment (dev = Vite origin) |
| Low | Export key trust (TOFU) | Accepted: `pcc keys fingerprints`, fingerprints on the terms page, `export-verify --fingerprints` |
