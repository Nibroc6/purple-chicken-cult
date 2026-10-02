#!/usr/bin/env bash
# Worktrees for parallel work packages (plan §9.1).
#   scripts/wt.sh new <wp-id> [base]   git worktree add ../pcc-wt/<wp-id> -b wp/<wp-id> <base> + npm ci (once)
#   scripts/wt.sh rm <wp-id>           remove the worktree (keeps the branch)
. "$(dirname "$0")/lib.sh"
cd "$ROOT"
cmd="${1:-}"
wp="${2:-}"
if [ -z "$cmd" ] || [ -z "$wp" ]; then
  echo "usage: scripts/wt.sh new|rm <wp-id> [base]" >&2
  exit 2
fi
dir="$(cd "$ROOT/.." && pwd)/pcc-wt/$wp"
case "$cmd" in
  new)
    base="${3:-main}"
    git worktree add "$dir" -b "wp/$wp" "$base"
    (cd "$dir/web" && npm ci)
    log "worktree ready: $dir (branch wp/$wp from $base); set PCC_SCAFFOLD_BASE=$(git rev-parse "$base")"
    ;;
  rm)
    git worktree remove "$dir"
    ;;
  *)
    echo "unknown command $cmd" >&2
    exit 2
    ;;
esac
