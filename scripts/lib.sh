# Shared shell setup for scripts/*.sh (POSIX bash; Git Bash on Windows, Linux CI).
# Source it: . "$(dirname "$0")/lib.sh"
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"

export PCC_TEST_DATABASE_URL="${PCC_TEST_DATABASE_URL:-postgres://pcc:pcc@localhost:54329/pcc_test?sslmode=disable}"
export PCC_REQUIRE_DB="${PCC_REQUIRE_DB:-1}"
PCC_ADMIN_DATABASE_URL="${PCC_ADMIN_DATABASE_URL:-postgres://pcc:pcc@localhost:54329/pcc?sslmode=disable}"

GOEXE="$(go env GOEXE 2>/dev/null || true)"
PCC_BIN="$ROOT/var/pcc$GOEXE"

log() { printf '\033[1;35m==> %s\033[0m\n' "$*" >&2; }

# ensure_db starts the dev Postgres only when no container named pcc-postgres
# exists (an existing container is reused as-is). CI provides its own service.
ensure_db() {
  if [ -n "${CI:-}" ]; then return 0; fi
  if ! command -v docker >/dev/null 2>&1; then return 0; fi
  # (grep, not --filter name=^/...: Git Bash would rewrite the leading slash as a path)
  local all running
  all="$(docker ps -a --format '{{.Names}}')"
  running="$(docker ps --format '{{.Names}}')"
  if ! grep -qx pcc-postgres <<<"$all"; then
    log "starting postgres (docker compose)"
    docker compose -f "$ROOT/docker-compose.yml" up -d postgres
  elif ! grep -qx pcc-postgres <<<"$running"; then
    log "starting existing pcc-postgres container"
    docker start pcc-postgres >/dev/null
  fi
  for _ in $(seq 1 30); do
    if docker exec pcc-postgres pg_isready -U pcc >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "postgres did not become ready" >&2
  return 1
}

# psql_admin runs SQL against the admin database with psql, or through the
# container when psql is not installed.
psql_admin() {
  if command -v psql >/dev/null 2>&1; then
    psql "$PCC_ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -qAt -c "$1"
  else
    docker exec pcc-postgres psql -U pcc -d pcc -v ON_ERROR_STOP=1 -qAt -c "$1"
  fi
}

build_binary() {
  log "go build -o var/pcc$GOEXE ./cmd/pcc"
  mkdir -p "$ROOT/var"
  (cd "$ROOT" && go build -o "$PCC_BIN" ./cmd/pcc)
}
