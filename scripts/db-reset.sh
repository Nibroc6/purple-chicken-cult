#!/usr/bin/env bash
# Drops stale test databases: pcc_t_* / pcc_e2e_* older than 6 hours with zero
# connections, and pcc_tpl_* templates that no current migration hash names.
# Never uses DROP DATABASE ... WITH (FORCE): it cannot kill another agent's tests.
. "$(dirname "$0")/lib.sh"
ensure_db
cd "$ROOT"
current="$(go run ./internal/store/storetest/cmd/testdb template-name 2>/dev/null || true)"
stale="$(psql_admin "SELECT d.datname FROM pg_database d
  WHERE (d.datname LIKE 'pcc\\_t\\_%' OR d.datname LIKE 'pcc\\_e2e\\_%')
    AND NOT EXISTS (SELECT 1 FROM pg_stat_activity a WHERE a.datname = d.datname)
    AND COALESCE((pg_stat_file('base/' || d.oid || '/PG_VERSION')).modification, now()) < now() - interval '6 hours'")"
templates="$(psql_admin "SELECT datname FROM pg_database WHERE datname LIKE 'pcc\\_tpl\\_%'")"
for db in $stale; do
  log "drop $db"
  psql_admin "DROP DATABASE IF EXISTS \"$db\""
done
for db in $templates; do
  if [ -n "$current" ] && [ "$db" != "$current" ]; then
    if [ -z "$(psql_admin "SELECT 1 FROM pg_stat_activity WHERE datname = '$db' LIMIT 1")" ]; then
      log "drop template $db"
      psql_admin "ALTER DATABASE \"$db\" IS_TEMPLATE false" || true
      psql_admin "DROP DATABASE IF EXISTS \"$db\""
    fi
  fi
done
log "done"
