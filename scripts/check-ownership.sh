#!/usr/bin/env bash
# Fails if the work package's branch diff (vs the scaffold base, $PCC_SCAFFOLD_BASE
# or merge-base with main) touches a path it does not own (scripts/ownership.json).
# Usage: scripts/check-ownership.sh <wp-id>
. "$(dirname "$0")/lib.sh"
cd "$ROOT"
if [ $# -ne 1 ]; then
  echo "usage: scripts/check-ownership.sh <wp-id>" >&2
  exit 2
fi
node scripts/ownership.mjs check "$1"
