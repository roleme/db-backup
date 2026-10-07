#!/usr/bin/env bash
set -Eeuo pipefail

LIB=${DBB_LIB:-/usr/local/lib/db-backup}
# shellcheck disable=SC1091
. "$LIB/common.sh"
# shellcheck disable=SC1091
. "$LIB/layout.sh"

usage() {
  printf 'usage: db-backup backup|verify|check\n' >&2
  exit 2
}

[ $# -eq 1 ] || usage
cmd=$1
case "$cmd" in
  backup | verify | check) ;;
  *) usage ;;
esac

load_config
# shellcheck disable=SC1090
. "$LIB/drivers/${DRIVER}.sh"

backup_one() {
  local target=$1 suffix tmp expected
  suffix=$(driver_suffix)
  tmp=$(mktemp "$BACKUP_DIR/.${target}.partial.XXXXXX") || return 1
  if ! driver_dump "$target" "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  if ! expected=$(driver_expected_tables "$target" "$tmp"); then
    rm -f "$tmp"
    return 1
  fi
  if [ "${expected:-0}" = 0 ]; then
    log "ERROR: dump of $target contains no tables" >&2
    rm -f "$tmp"
    return 1
  fi
  store_dump "$target" "$suffix" "$tmp" "$expected" || return 1
  prune "$target" "$suffix"
  log "dumped $target"
}

backup_extra() {
  local path=$1 name tmp
  name=$(basename "$path")
  if [ ! -d "$path" ]; then
    log "ERROR: EXTRA_PATHS entry $path is not a directory" >&2
    return 1
  fi
  tmp=$(mktemp "$BACKUP_DIR/.${name}.partial.XXXXXX") || return 1
  if ! tar -C "$(dirname "$path")" -cf - "$name" | gzip "-${GZIP_LEVEL}" > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  store_dump "$name" .tar.gz "$tmp" || return 1
  prune "$name" .tar.gz
  log "archived $path"
}

validate_extra_paths() {
  local path name
  local -A seen=()
  while IFS= read -r path; do
    name=$(basename "$path")
    [ -z "${seen[$name]:-}" ] || die "EXTRA_PATHS has two directories named $name"
    seen[$name]=$path
  done < <(split_list "$EXTRA_PATHS")
}

backup_all() {
  local failures=0 target path
  driver_validate
  validate_extra_paths
  find "$BACKUP_DIR" -maxdepth 1 \( -name '.*.partial.*' -o -name '.sqlite.*' \) -mmin +60 -exec rm -rf {} +
  while IFS= read -r target; do
    if ! backup_one "$target"; then
      log "ERROR: dump of $target failed" >&2
      failures=$((failures + 1))
    fi
  done < <(driver_targets)
  while IFS= read -r path; do
    if ! backup_extra "$path"; then
      log "ERROR: archive of $path failed" >&2
      failures=$((failures + 1))
    fi
  done < <(split_list "$EXTRA_PATHS")
  [ "$failures" -eq 0 ] || die "$failures backup(s) failed"
}

verify_all() {
  local failures=0 target path latest name expected suffix
  driver_validate
  validate_extra_paths
  suffix=$(driver_suffix)
  while IFS= read -r target; do
    latest="$BACKUP_DIR/last/${target}-latest${suffix}"
    if [ ! -e "$latest" ]; then
      log "ERROR: no dump found for $target" >&2
      failures=$((failures + 1))
      continue
    fi
    name=$(readlink "$latest")
    expected=$(cat "$BACKUP_DIR/last/$name.tables" 2> /dev/null || true)
    if [ -z "$expected" ]; then
      log "ERROR: no table count recorded for $name" >&2
      failures=$((failures + 1))
      continue
    fi
    if driver_verify "$target" "$latest" "$expected"; then
      log "verified $target"
    else
      log "ERROR: verify of $target failed" >&2
      failures=$((failures + 1))
    fi
  done < <(driver_targets)
  while IFS= read -r path; do
    latest="$BACKUP_DIR/last/$(basename "$path")-latest.tar.gz"
    if [ -e "$latest" ] && tar -tzf "$latest" > /dev/null 2>&1; then
      log "verified $path"
    else
      log "ERROR: verify of $path failed" >&2
      failures=$((failures + 1))
    fi
  done < <(split_list "$EXTRA_PATHS")
  [ "$failures" -eq 0 ] || die "$failures verification(s) failed"
}

case "$cmd" in
  check)
    driver_validate
    validate_extra_paths
    log "config ok"
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
