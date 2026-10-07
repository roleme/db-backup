#!/usr/bin/env bash
set -Eeuo pipefail

LIB=${DBB_LIB:-/usr/local/lib/db-backup}
# shellcheck disable=SC1091
. "$LIB/common.sh"
# shellcheck disable=SC1091
. "$LIB/targets.sh"

if [ $# -ne 2 ]; then
  printf 'usage: db-backup-run NAME backup|verify|check|names\n' >&2
  exit 2
fi
name=$1
action=$2
case "$action" in
  backup | verify | check | names) ;;
  *)
    printf 'usage: db-backup-run NAME backup|verify|check|names\n' >&2
    exit 2
    ;;
esac

parse_target "$name"
build_child_env

limit=${TARGET_VARS[TIMEOUT]:-3600}
[[ "$limit" =~ ^[1-9][0-9]*$ ]] || die "target $name: TIMEOUT must be a positive number of seconds"

lock_dir=${DBB_LOCK_DIR:-/tmp/dbb-locks}
mkdir -p "$lock_dir"

rc=0
env -i PATH="$PATH" TZ="${TZ:-UTC}" BACKUP_DIR="${BACKUP_DIR:-/backups}" DBB_LIB="$LIB" \
  ${CHILD_ENV[@]+"${CHILD_ENV[@]}"} \
  flock "$lock_dir/$name.lock" timeout --kill-after=30 "$limit" db-backup "$action" || rc=$?

if [ "$rc" -eq 124 ] || [ "$rc" -eq 137 ]; then
  log "ERROR: target $name $action timed out after ${limit}s" >&2
  case "$action" in
    backup) ping_url "$(child_var HC_PING_URL)" /fail ;;
    verify) ping_url "$(child_var HC_VERIFY_PING_URL)" /fail ;;
    *) ;;
  esac
fi
exit "$rc"
