#!/usr/bin/env bash
set -Eeuo pipefail

LIB=${DBB_LIB:-/usr/local/lib/db-backup}
export TARGETS_DIR=${TARGETS_DIR:-/config/targets.d}
export DBB_SKIPPED=${DBB_SKIPPED:-/tmp/dbb-skipped}
crontab=/tmp/crontab
snapshot=/tmp/dbb-targets

# shellcheck disable=SC1091
. "$LIB/common.sh"

schedule_works() {
  printf '%s true\n' "$1" > /tmp/dbb-schedule-test
  supercronic -test /tmp/dbb-schedule-test > /dev/null 2>&1
}

rm -f "$DBB_SKIPPED"

if compgen -G "$TARGETS_DIR/*.env" > /dev/null; then
  # shellcheck disable=SC1091
  . "$LIB/targets.sh"
  declare -A owner=()
  valid=()
  : > "$crontab"
  rm -rf "$snapshot"
  mkdir -p "$snapshot"
  names=$(list_targets)
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    if ! db-backup-run "$name" check || ! dumps=$(db-backup-run "$name" names); then
      log "ERROR: skipping target $name" >&2
      : > "$DBB_SKIPPED"
      target_fail_ping "$name"
      continue
    fi
    parse_target "$name"
    backup_schedule=${TARGET_VARS[SCHEDULE]:-@daily}
    verify_schedule=${TARGET_VARS[VERIFY_SCHEDULE]:-}
    if ! schedule_works "$backup_schedule" || { [ -n "$verify_schedule" ] && ! schedule_works "$verify_schedule"; }; then
      log "ERROR: skipping target $name: the scheduler rejects its schedule" >&2
      : > "$DBB_SKIPPED"
      target_fail_ping "$name"
      continue
    fi
    while IFS= read -r dump; do
      [ -z "${owner[$dump]:-}" ] || die "dump name $dump is used by targets ${owner[$dump]} and $name"
      owner[$dump]=$name
    done <<< "$dumps"
    printf '%s db-backup-run %s backup\n' "$backup_schedule" "$name" >> "$crontab"
    if [ -n "$verify_schedule" ]; then
      printf '%s db-backup-run %s verify\n' "$verify_schedule" "$name" >> "$crontab"
    fi
    cp "$(target_file "$name")" "$snapshot/$name.env"
    valid+=("$name")
  done <<< "$names"
  [ "${#valid[@]}" -gt 0 ] || die "no valid targets in $TARGETS_DIR"
  export TARGETS_DIR=$snapshot
  if [ "${BACKUP_ON_START:-FALSE}" = TRUE ]; then
    for name in "${valid[@]}"; do
      db-backup-run "$name" backup || true
    done
  fi
else
  check_schedule single SCHEDULE "${SCHEDULE:-@daily}"
  if [ -n "${VERIFY_SCHEDULE:-}" ]; then
    check_schedule single VERIFY_SCHEDULE "$VERIFY_SCHEDULE"
  fi
  db-backup check
  {
    printf '%s db-backup backup\n' "${SCHEDULE:-@daily}"
    if [ -n "${VERIFY_SCHEDULE:-}" ]; then
      printf '%s db-backup verify\n' "$VERIFY_SCHEDULE"
    fi
  } > "$crontab"
  if [ "${BACKUP_ON_START:-FALSE}" = TRUE ]; then
    db-backup backup || true
  fi
fi

exec supercronic "$crontab"
