#!/usr/bin/env bash
set -Eeuo pipefail

LIB=${DBB_LIB:-/usr/local/lib/db-backup}
# shellcheck disable=SC1091
. "$LIB/common.sh"
# shellcheck disable=SC1091
. "$LIB/layout.sh"

usage() {
  printf 'usage: db-backup backup|verify|check|names\n' >&2
  exit 2
}

[ $# -eq 1 ] || usage
cmd=$1
case "$cmd" in
  backup | verify | check | names) ;;
  *) usage ;;
esac

load_config
# shellcheck disable=SC1090
. "$LIB/drivers/${DRIVER}.sh"
ADAPTERS=("$DRIVER")
if [ -n "$EXTRA_PATHS" ]; then
  # shellcheck disable=SC1091
  . "$LIB/drivers/paths.sh"
  ADAPTERS+=(paths)
fi

validate_all() {
  local adapter
  for adapter in "${ADAPTERS[@]}"; do
    "${adapter}_validate"
  done
}

list_units() {
  local adapter unit
  for adapter in "${ADAPTERS[@]}"; do
    while IFS= read -r unit; do
      printf '%s %s\n' "$adapter" "$unit"
    done < <("${adapter}_units")
  done
}

has_tables() {
  declare -F "${1}_tables" > /dev/null
}

backup_one() {
  local adapter=$1 unit=$2 suffix tmp tables=""
  suffix=$("${adapter}_suffix")
  tmp=$(mktemp "$BACKUP_DIR/.${unit}.partial.XXXXXX") || return 1
  if ! "${adapter}_dump" "$unit" "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  if has_tables "$adapter"; then
    if ! tables=$("${adapter}_tables" "$unit" "$tmp"); then
      rm -f "$tmp"
      return 1
    fi
    if [ "${tables:-0}" = 0 ]; then
      log "ERROR: dump of $unit contains no tables" >&2
      rm -f "$tmp"
      return 1
    fi
  fi
  store_dump "$unit" "$suffix" "$tmp" "$tables" || return 1
  prune "$unit" "$suffix"
  log "backed up $unit"
}

verify_one() {
  local adapter=$1 unit=$2 suffix latest name expected=""
  suffix=$("${adapter}_suffix")
  latest="$BACKUP_DIR/last/${unit}-latest${suffix}"
  if [ ! -e "$latest" ]; then
    log "ERROR: no dump found for $unit" >&2
    return 1
  fi
  if has_tables "$adapter"; then
    name=$(readlink "$latest")
    expected=$(cat "$BACKUP_DIR/last/$name.tables" 2> /dev/null || true)
    if [ -z "$expected" ]; then
      log "ERROR: no table count recorded for $name" >&2
      return 1
    fi
  fi
  "${adapter}_verify" "$unit" "$latest" "$expected" || return 1
  log "verified $unit"
}

backup_all() {
  local failures=0 adapter unit
  validate_all
  while read -r adapter unit; do
    find "$BACKUP_DIR" -maxdepth 1 \( -name ".${unit}.partial.*" -o -name ".sqlite.${unit}.*" \) -mmin +60 -exec rm -rf {} +
  done < <(list_units)
  while read -r adapter unit; do
    if ! backup_one "$adapter" "$unit"; then
      log "ERROR: backup of $unit failed" >&2
      failures=$((failures + 1))
    fi
  done < <(list_units)
  [ "$failures" -eq 0 ] || die "$failures backup(s) failed"
}

verify_all() {
  local failures=0 adapter unit
  validate_all
  while read -r adapter unit; do
    if ! verify_one "$adapter" "$unit"; then
      log "ERROR: verify of $unit failed" >&2
      failures=$((failures + 1))
    fi
  done < <(list_units)
  [ "$failures" -eq 0 ] || die "$failures verification(s) failed"
}

case "$cmd" in
  check)
    validate_all
    log "config ok"
    ;;
  names)
    validate_all
    while read -r adapter unit; do
      printf '%s%s\n' "$unit" "$("${adapter}_suffix")"
    done < <(list_units)
    ;;
  backup)
    if (backup_all); then
      ping_url "$HC_PING_URL"
    else
      ping_url "$HC_PING_URL" /fail
      exit 1
    fi
    ;;
  verify)
    if (verify_all); then
      ping_url "$HC_VERIFY_PING_URL"
    else
      ping_url "$HC_VERIFY_PING_URL" /fail
      exit 1
    fi
    ;;
esac
