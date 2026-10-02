#!/usr/bin/env bash
# The global check (merge queue and CI): generated files, Go vet + lint + tests
# (incl. internal/archtest), web lint/typecheck/unit, build, all e2e specs.
# Set PCC_SKIP_E2E=1 to skip Playwright (e.g. no browsers installed).
. "$(dirname "$0")/lib.sh"
cd "$ROOT"
bash scripts/gen.sh
bash scripts/lint.sh
bash scripts/test-go.sh ./...
log "web unit tests"
npm --prefix web test
bash scripts/build.sh
if [ "${PCC_SKIP_E2E:-}" != "1" ]; then
  bash scripts/e2e.sh "$@"
fi
log "check: OK"
