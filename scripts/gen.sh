#!/usr/bin/env bash
# Generated, gitignored files: api/openapi.gen.yaml, web/src/api/schema.gen.ts and
# the en-XA pseudo-locale (atomic writes under web/.gen.lock).
. "$(dirname "$0")/lib.sh"
cd "$ROOT/web"
node scripts/gen.mjs "$@"
