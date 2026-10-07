# shellcheck shell=bash

declare -A SQLITE_FILES=()

sqlite_count() {
  sqlite3 "$1" "select count(*) from sqlite_master where type = 'table'"
}

driver_validate() {
  require_env SQLITE_PATHS
  local path name
  SQLITE_FILES=()
  while IFS= read -r path; do
    [ -f "$path" ] || die "SQLITE_PATHS entry $path does not exist"
    name=$(basename "$path")
    name=${name%.*}
    [ -z "${SQLITE_FILES[$name]:-}" ] || die "SQLITE_PATHS has two files named $name"
    SQLITE_FILES[$name]=$path
  done < <(split_list "$SQLITE_PATHS")
  [ "${#SQLITE_FILES[@]}" -gt 0 ] || die "SQLITE_PATHS lists no files"
}

driver_targets() {
  printf '%s\n' "${!SQLITE_FILES[@]}"
}

driver_suffix() {
  printf '%s' '.db.gz'
}

driver_dump() {
  local copy rc
  copy=$(mktemp "$BACKUP_DIR/.sqlite.XXXXXX") || return 1
  if ! sqlite3 -cmd '.timeout 30000' "${SQLITE_FILES[$1]}" ".backup '$copy'"; then
    rm -f "$copy"
    return 1
  fi
  gzip "-${GZIP_LEVEL}" < "$copy" > "$2"
  rc=$?
  rm -f "$copy"
  return "$rc"
}

driver_expected_tables() {
  local copy count
  copy=$(mktemp "$BACKUP_DIR/.sqlite.XXXXXX") || return 1
  if gunzip -c "$2" > "$copy" && count=$(sqlite_count "$copy"); then
    rm -f "$copy"
    printf '%s\n' "$count"
    return 0
  fi
  rm -f "$copy"
  return 1
}

driver_verify() {
  local copy res count
  copy=$(mktemp "$BACKUP_DIR/.sqlite.XXXXXX") || return 1
  if ! gunzip -c "$2" > "$copy"; then
    rm -f "$copy"
    return 1
  fi
  if ! res=$(sqlite3 "$copy" 'PRAGMA integrity_check' 2>&1); then
    rm -f "$copy"
    log "ERROR: $1: $res" >&2
    return 1
  fi
  count=$(sqlite_count "$copy") || count=""
  rm -f "$copy"
  [ "$res" = ok ] || {
    log "ERROR: $1 integrity_check: $res" >&2
    return 1
  }
  [ "$count" = "$3" ] || {
    log "ERROR: $1 restored $count tables, expected $3" >&2
    return 1
  }
}
