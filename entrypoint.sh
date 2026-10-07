#!/usr/bin/env bash
set -Eeuo pipefail

db-backup check

crontab=/tmp/crontab
{
  printf '%s db-backup backup\n' "${SCHEDULE:-@daily}"
  if [ -n "${VERIFY_SCHEDULE:-}" ]; then
    printf '%s db-backup verify\n' "$VERIFY_SCHEDULE"
  fi
} > "$crontab"

if [ "${BACKUP_ON_START:-FALSE}" = TRUE ]; then
  db-backup backup || true
fi

exec supercronic "$crontab"
