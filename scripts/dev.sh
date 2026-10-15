#!/usr/bin/env bash
# Local development: Postgres (reused if running), migrations, the Go server on
# 127.0.0.1:8080 (PCC_ENV=dev) and the Vite dev server on http://localhost:5173,
# which proxies /api to Go. Open http://localhost:5173. Ctrl-C stops both.
. "$(dirname "$0")/lib.sh"
ensure_db
cd "$ROOT"
export PCC_ENV="${PCC_ENV:-dev}"
build_binary
"$PCC_BIN" migrate
log "pcc serve (PCC_ENV=$PCC_ENV) + vite"
"$PCC_BIN" serve &
SERVER=$!
trap 'kill $SERVER 2>/dev/null || true' EXIT INT TERM
npm --prefix web run dev
