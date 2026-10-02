#!/usr/bin/env bash
# Playwright end-to-end run, isolated per run (plan §7, buildability A4): builds
# var/pcc once (never `go run`), clones a fresh pcc_e2e_<rand> database from the
# migrated template, serves the SPA from a per-run static dir, picks a free port,
# runs the given specs (default: all), then drops the database.
# Usage: scripts/e2e.sh [web/e2e/<spec>.ts ...]
. "$(dirname "$0")/lib.sh"
ensure_db
cd "$ROOT"

RUN_DIR="$ROOT/var/e2e-$$"
mkdir -p "$RUN_DIR"
log "building the SPA into $RUN_DIR/static"
PCC_WEB_OUT_DIR="$RUN_DIR/static" npm --prefix web run build >/dev/null
build_binary

DB_URL="$(go run ./internal/store/storetest/cmd/testdb create pcc_e2e_)"
DB_NAME="$(basename "${DB_URL%%\?*}")"
cleanup() {
  go run ./internal/store/storetest/cmd/testdb drop "$DB_NAME" >/dev/null 2>&1 || true
  rm -rf "$RUN_DIR"
}
trap cleanup EXIT INT TERM

specs=()
for s in "$@"; do specs+=("${s#web/}"); done

export PCC_DATABASE_URL="$DB_URL"
export PCC_STATIC_DIR="$RUN_DIR/static"
export PCC_E2E_BIN="$PCC_BIN"
log "playwright ${specs[*]:-(all specs)}"
(cd web && npx playwright test --pass-with-no-tests "${specs[@]}")
