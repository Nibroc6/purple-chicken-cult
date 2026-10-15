#!/usr/bin/env bash
# Production build: the SPA (web/dist, embedded) then the single pcc binary.
. "$(dirname "$0")/lib.sh"
cd "$ROOT"
log "npm --prefix web run build"
npm --prefix web run build
build_binary
log "built $PCC_BIN"
