#!/usr/bin/env bash
# Go tests against the local Postgres (template DB per migration hash, one
# database per test). Never -race locally (no cgo on the Windows dev machine).
# Usage: scripts/test-go.sh [go test args...]   (default: ./...)
. "$(dirname "$0")/lib.sh"
ensure_db
cd "$ROOT"
if [ $# -eq 0 ]; then set -- ./...; fi
log "go test -p 4 $*"
go test -p 4 -count=1 "$@"
