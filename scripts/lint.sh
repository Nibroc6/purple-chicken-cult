#!/usr/bin/env bash
# Static checks: go vet, golangci-lint (installs v2.14.0 into ~/go/bin when
# missing), the ownership self-test, web lint (ESLint + logical CSS + i18n
# catalogs) and the TypeScript typecheck.
. "$(dirname "$0")/lib.sh"
cd "$ROOT"
log "go vet ./..."
go vet ./...
if ! command -v golangci-lint >/dev/null 2>&1; then
  log "installing golangci-lint v2.14.0"
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
fi
log "golangci-lint run"
golangci-lint run ./...
log "ownership self-test"
node scripts/ownership.mjs selftest
log "web lint + typecheck"
npm --prefix web run lint
npm --prefix web run typecheck
