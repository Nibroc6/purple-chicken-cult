#!/usr/bin/env bash
# A work package's own green bar, run in its worktree (plan §0.10): ownership,
# go vet + golangci-lint + go test over the whole module (cheap: the tree holds
# only scaffold + this WP), web lint/typecheck/unit, i18n check scoped to the
# WP's namespaces, and the WP's own Playwright spec.
# Usage: scripts/check-wp.sh <wp-id>
. "$(dirname "$0")/lib.sh"
cd "$ROOT"
if [ $# -ne 1 ]; then
  echo "usage: scripts/check-wp.sh <wp-id>" >&2
  exit 2
fi
WP="$1"
bash scripts/check-ownership.sh "$WP"
bash scripts/gen.sh
log "go vet + golangci-lint"
go vet ./...
golangci-lint run ./...
bash scripts/test-go.sh ./...
log "web lint / typecheck / unit"
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
namespaces="$(node -e '
const own = require("./scripts/ownership.json");
const pats = Object.values(own.phases).map((p) => p[process.argv[1]] || []).flat();
const ns = new Set();
for (const p of pats) {
  const m = p.match(/^web\/src\/i18n\/locales\/en\/(\{([^}]*)\}|([^/]*))\.json$/);
  if (m) (m[2] ? m[2].split(",") : [m[3]]).forEach((n) => ns.add(n));
}
console.log([...ns].join(" "));
' "$WP")"
if [ -n "$namespaces" ]; then
  log "check-i18n $namespaces"
  # shellcheck disable=SC2086
  (cd web && node scripts/check-i18n.mjs $namespaces)
fi
spec="web/e2e/$WP.spec.ts"
if [ -f "$spec" ] && [ "${PCC_SKIP_E2E:-}" != "1" ]; then
  bash scripts/e2e.sh "$spec"
fi
log "check-wp $WP: OK"
