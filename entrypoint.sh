#!/usr/bin/env bash
set -Eeuo pipefail

LIB=${DBB_LIB:-/usr/local/lib/db-backup}
export TARGETS_DIR=${TARGETS_DIR:-/config/targets.d}
crontab=/tmp/crontab

if compgen -G "$TARGETS_DIR/*.env" > /dev/null; then
  # shellcheck disable=SC1091
  . "$LIB/common.sh"
  # shellcheck disable=SC1091
  . "$LIB/targets.sh"
  declare -A owner=()
  : > "$crontab"
  names=$(list_targets)
  while IFS= read -r name; do
    db-backup-run "$name" check
    while IFS= read -r dump; do
      [ -z "${owner[$dump]:-}" ] || die "dump name $dump is used by targets ${owner[$dump]} and $name"
      owner[$dump]=$name
    done < <(db-backup-run "$name" names)
    parse_target "$name"
    printf '%s db-backup-run %s backup\n' "${TARGET_VARS[SCHEDULE]:-@daily}" "$name" >> "$crontab"
    if [ -n "${TARGET_VARS[VERIFY_SCHEDULE]:-}" ]; then
      printf '%s db-backup-run %s verify\n' "${TARGET_VARS[VERIFY_SCHEDULE]}" "$name" >> "$crontab"
    fi
  done <<< "$names"
  if [ "${BACKUP_ON_START:-FALSE}" = TRUE ]; then
    while IFS= read -r name; do
      db-backup-run "$name" backup || true
    done <<< "$names"
  fi
else
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
