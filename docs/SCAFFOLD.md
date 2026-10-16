# Scaffold: rules for work packages

The scaffold (SCAFFOLD-0a, SCAFFOLD-0b and SCAFFOLD-1, all landed) is the contract layer for
Phase 0 and Phase 1. Every work package (WP) builds on it in its own worktree and **never edits
a frozen file**. The full plan is [IMPLEMENTATION.md](IMPLEMENTATION.md). File ownership is
machine-readable in [`scripts/ownership.json`](../scripts/ownership.json), and
`scripts/check-ownership.sh <wp-id>` enforces it.

## 1. Frozen files (contract steward only)

No WP edits these. A WP that needs a change writes `docs/contract-requests/<wp-id>-<n>.md`
(the only kind of file a WP may add outside its own paths). The steward applies **additive**
changes the same day: a new interface, an optional field, an event type, a nullable column, a
route that fits plan §5.2, an error code or a namespace. Breaking changes wait for INT.

**Repo plumbing**
- `go.mod`, `go.sum`, `web/package.json`, `web/package-lock.json`. WPs never run `go get`,
  `go mod tidy`, `npm install` or `npm ci` (the one exception is `scripts/wt.sh`). A new
  dependency is a contract request.
- `.gitattributes`, `.gitignore`, `.golangci.yml`, `Makefile`, `docker-compose.yml`,
  `deploy/postgres/**`, `.github/workflows/ci.yml`, `scripts/**` (including `ownership.json`).

**Go contracts**
- `internal/gate/{gate.go,views.go,errors.go}`: views, `gate.Ref`, the closed error set,
  `gate.Class` and `gate.NotFoundJSON`.
- `internal/gate/internal/core/**`: IDs, `Viewer`, every cross-module interface, `Deps`, the
  `Bus` and its event types.
- `internal/gate/internal/coreref/**`: the reference implementations every stub delegates to.
- `internal/gate/internal/coretest/**`: `World`, `FakePasskeys`, `FixedKeys`, `Config` and
  `Clock` for tests.
- `internal/gate/wire/**` (`App`, `Build`, `Hooks`, `wiretest`).
- `internal/api/{apix,router,apitest,testhooks}/**` and `internal/api/deps.go`. The one
  exception is `internal/api/apix/pad/**`, which passes to P1-W07. The route catalogue is
  `internal/api/router/catalogue.go`.
- `internal/store/**`: the pool, transactions, the migration runner, `jobq`, `storetest` and
  migrations `0001–0099` and `2001–2099`.
- `internal/platform/{config,clock,ids,refs,sigs,httpx,logx}/**`, plus `internal/deps/**`,
  `internal/archtest/**` and `cmd/pcc/**`.
- `api/openapi/_common.yaml`.

**Web contracts**
- `web/{vite,vitest,playwright}.config.ts`, `web/tsconfig*.json`, `web/eslint.config.js`,
  `web/index.html`, `web/embed.go`, `web/dist/.keep`, `web/public/**`.
- `web/scripts/{lib,gen,openapi-merge,pseudo-locale,check-i18n,check-css-logical}.mjs`.
- `web/e2e/fixtures/{auth,world,server,global-setup}.ts` and `web/e2e/scaffold.spec.ts`.
- `web/src/main.tsx`, `web/src/app/**` (route table, shell, slots, `settingsNav.ts`),
  `web/src/api/{client,errors}.ts`, `web/src/vm/types.ts`, `web/src/lib/**`,
  `web/src/styles/**` and `web/src/test/**`.
- `web/src/i18n/{setup,namespaces}.ts`, `web/src/i18n/resources.d.ts` and
  `web/src/i18n/locales/en/{common,errors}.json`.
- `web/src/formats/{types,registry}.ts`, `web/src/formats/{cards,compact,explorer}/index.ts`
  and `web/src/formats/formats.test.tsx`.
- `web/src/integrity/{types,index}.ts`, `web/src/sw/{sw,register}.ts`,
  `web/src/features/media/index.ts` and `web/src/features/handshake-audio/index.ts`.

**Hand-off stubs** are not frozen. The scaffold created them so everything compiles, and
ownership passes to the named WP, which replaces their content:
- every `internal/gate/<module>/` package;
- every `internal/api/<module>api/routes.go`;
- every `api/openapi/<module>.yaml`;
- every `web/src/features/<f>/**`;
- every `web/src/i18n/locales/en/<ns>.json` except `common` and `errors`;
- the layout files under `web/src/formats/{cards,compact,explorer}/`;
- the seven integrity components;
- `web/src/markdown/**`, `web/src/api/sse.ts`, `web/src/sw/push.ts`;
- `internal/platform/{secheaders,safefetch,urlx,mdscan,blob,scan,imageproc,webpush,i18n,dbsc}`.

## 2. How a package adds…

**Routes.** Edit only your own `internal/api/<module>api/routes.go` (and `handlers_*.go`).
Register **flat patterns relative to `/api`** with
`apix.Route(r, method, "/posts/{ref}/like", apix.Write("like"), handler)`:
- Never call `r.Route`, `r.Mount` or `r.Group` (archtest A12).
- Every route needs a class: `apix.Read`, `apix.Write`, `apix.Anon` or `apix.Free`.
  `apix.Route` charges it before your handler runs.
- Path parameters are `{ref}`, `{hs}`, `{id}`, `{snapshot}` or `{key}` only.
- The route must be listed in `internal/api/router/catalogue.go`. Adding a path is a steward
  request, and `router_test` fails on routes outside the catalogue.
- Handlers return errors and never write a 404 or 403 themselves: use `gate.ErrNotFound`,
  `gate.NewProblem(...)` and the rest of the closed error set.
- Responses go through `apix.JSON`. Request bodies go through `apix.Decode[T](r, limit)`.
- Document the route in your own `api/openapi/<module>.yaml`, where `$ref` can point into
  `_common.yaml`. `apitest` validates every request and response against it.

**Migrations.** Only when you truly need one, in your reserved range:
- Phase 0 WP *n*: `1000+10n … +9` (P0-W05 → `1050–1059`).
- Phase 1 WP *n*: `3000+10n … +9` (P1-W05 → `3050–3059`).
- The scaffold uses `0001–0099` and `2001–2099`.

The rules for migrations:
- File name: `internal/store/migrations/NNNN_name.sql`.
- Additive only. Add nullable or defaulted columns; never drop, rename, or add NOT NULL
  without a default.
- Name every column (no `SELECT *` and no positional `INSERT`).
- Never edit an applied migration: the runner refuses a changed sha256.
- Write only the tables your WP owns (plan §3.4). Every other module goes through its `core`
  interface.

**i18n keys.**
- Put keys only in your own namespaces, in `web/src/i18n/locales/en/<ns>.json`, as ICU
  MessageFormat.
- Load with `useTranslation('<ns>')` and call `t('area.key')`. Keys are type-checked through
  `resources.d.ts`.
- Error codes use `<ns>:errors.<code>` or the shared `errors:<code>`. Declare dynamic keys
  with a `// i18n-dynamic: <ns>:<prefix>` comment.
- No literal user-facing strings: ESLint `i18next/no-literal-string` enforces this on the
  client and archtest A9 on the server.
- `npm --prefix web run lint` runs `check-i18n`, which catches unused keys, ICU errors and
  stray pseudo-locale keys. The `en-XA` pseudo-locale is generated, so never edit it.
- A new namespace is a steward request: it goes in `namespaces.ts` and `resources.d.ts`.

**API types.**
- **Go:** handler-facing service methods take and return only builtins and stdlib types,
  `gate.*` views, `gate.Ref`, `json.RawMessage`, `io.Reader`/`io.Writer`, or your package's own
  exported DTOs (archtest A5).
- A new shared view field goes in `internal/gate/views.go` and `_common.yaml` through the
  steward. Module-only DTOs live in your package, with module-prefixed schema names in your
  OpenAPI fragment.
- **TypeScript:** `npm run gen:api` merges `api/openapi/*.yaml` into
  `web/src/api/schema.gen.ts`, which is gitignored and regenerated by `pre*` hooks.
- Map API JSON to view-models in your feature (`features/<f>/vm.ts`).
- Formats and integrity components receive VMs only and never import `src/api`.

**Cross-module calls (Go).**
- Use `core` interfaces through `d *core.Deps` (`d.Graph`, `d.Vis`, `d.Signer`, …).
- Never import another domain package (archtest A8). The exceptions are `gate/rank`, which
  anyone may import, and `devseed → graph`.
- Only `visibility`, `postindex` and `coreref` touch raw links or mint `core.Checked` (A10).
- Log with allowlisted keys only (`logx.Keys`), and log user IDs only as `logx.UID` (A11).

## 3. Commands

These run under Git Bash on Windows and on Linux. Go 1.27 is at `~/.local/go/bin`, which the
scripts prepend to `PATH`.

| Command | What it does |
|---|---|
| `bash scripts/dev.sh` (`make dev`) | Starts Postgres (reuses `pcc-postgres`), migrates, runs `pcc serve` on :8080 and Vite on http://localhost:5173 |
| `bash scripts/test-go.sh [pkgs]` | `go test -p 4 -count=1` against the local DB, with `PCC_REQUIRE_DB=1` so the DB is never silently skipped |
| `npm --prefix web test` | Vitest |
| `bash scripts/lint.sh` | `go vet`, golangci-lint, the ownership self-test, then web lint and typecheck |
| `bash scripts/build.sh` | Builds the SPA into `web/dist` (embedded) and the binary into `var/pcc` |
| `bash scripts/e2e.sh [web/e2e/x.spec.ts]` | Playwright with a per-run DB, port and static dir |
| `bash scripts/check.sh` | The global check (merge queue and CI). `PCC_SKIP_E2E=1` skips Playwright |
| `bash scripts/check-wp.sh <wp-id>` | A WP's green bar, run in its worktree |
| `bash scripts/wt.sh new <wp-id>` | Creates the worktree `../pcc-wt/<wp-id>` on branch `wp/<wp-id>` and runs `npm ci` |
| `bash scripts/db-reset.sh` | Drops stale `pcc_t_*` / `pcc_e2e_*` databases and old templates |
| `pcc migrate` · `pcc serve` · `pcc genesis --handle H` · `pcc mod grant` · `pcc keys …` · `pcc export-verify` · `pcc audit-edges` · `pcc metrics` · `pcc dev seed` | The CLI (plan §4.1). `PCC_ENV` is required |

Databases:
- Dev: `postgres://pcc:pcc@localhost:54329/pcc`.
- Tests: `PCC_TEST_DATABASE_URL`, which defaults to `…/pcc_test`. `storetest.NewDB` clones a
  hash-named template per test.

## 4. What the scaffold left unimplemented

Every method the plan assigns to a WP is a stub. Core interfaces delegate to `coreref`, which
keeps state in memory except the graph, which loads from the database. Handler-facing methods
return `gate.ErrNotImplemented`, which answers `501 {"error":"not_implemented"}`.

What already works for real:
- **Server basics:** the users directory, sessions, guest sessions, `APIGuard` (fetch
  metadata, exact Origin, CSRF), `/api/session` and logout.
- **Enrollment:** genesis seeding and enrollment tokens (`pcc genesis`), and
  `pcc mod grant`.
- **Platform:** refs (AES-SIV), sigs (JCS + Ed25519 with key IDs, validity windows and
  envelope verification), config with the fail-closed P1 matrix, security/TDM headers,
  `robots.txt`, `ai.txt` and `tdmrep.json`.
- **Plumbing:** the SPA fallback, the uniform 404, `jobq`, every migration, archtest A1–A12,
  `apitest` with OpenAPI validation, and the test hooks.
- **Web:** the shell, the route table with placeholder pages, i18n with the pseudo-locale,
  Trusted Types and the service worker.
- **Phase 1 references (SCAFFOLD-1):** `coreref.MemCosign` (single-use, per-purpose intents
  with a 5-minute lifetime, verified through `Passkeys.VerifyAssertion`), `MemHandshakes`
  (upgrade needs a plain friend edge, recovery a friend+ guardian; `ClaimRecovery` with the
  payload is Verified), `MemMedia` (owned, ready, unattached media, at most 4; `Put` stands in
  for an upload), `MemStepUp` and `MemModQueue`. `coretest.Mem` exposes all of them and
  `World.FriendsPlus` makes friend+ edges. In the running app media stays fail-closed until
  P1-W04 lands (`media.Service` uses `MemMedia` without deps, so no media id reaches a row).
- **Fail-closed startup:** `cmd/pcc` `TestServeFailClosed` boots the prod configuration
  against a real database: a `dev` or `none` scanner starts with media disabled, an unknown
  scanner such as `cloudflare` or a missing KEK refuses to start.
