# shellcheck shell=bash

declare -A SQLITE_FILES=()

sqlite_count() {
  sqlite3 "$1" "select count(*) from sqlite_master where type = 'table'"
}

sqlite_ident() {
  local id=${1//\"/\"\"}
  printf '"%s"' "$id"
}

sqlite_exclude_rows() {
  local table deletes="" drops recreates
  [ -n "${EXCLUDE_TABLE_DATA:-}" ] || return 0
  drops=$(sqlite3 "$1" "select 'DROP TRIGGER \"' || replace(name, '\"', '\"\"') || '\";' from sqlite_master where type = 'trigger'") || return 1
  recreates=$(sqlite3 "$1" "select sql || ';' from sqlite_master where type = 'trigger' and sql is not null") || return 1
  while IFS= read -r table; do
    deletes+="DELETE FROM $(sqlite_ident "$table"); "
  done < <(split_list "$EXCLUDE_TABLE_DATA")
  sqlite3 "$1" "BEGIN; $drops $deletes $recreates COMMIT;" || return 1
  sqlite3 "$1" "VACUUM"
}

sqlite_validate() {
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

sqlite_units() {
  printf '%s\n' "${!SQLITE_FILES[@]}"
}

sqlite_suffix() {
  printf '%s' '.db.gz'
}

sqlite_dump() {
  local dir copy rc
  dir=$(mktemp -d "$BACKUP_DIR/.sqlite.$1.XXXXXX") || return 1
  copy="$dir/db"
  if ! sqlite3 -cmd '.timeout 30000' "${SQLITE_FILES[$1]}" "VACUUM INTO '$copy'" || ! sqlite_exclude_rows "$copy"; then
    rm -rf "$dir"
    return 1
  fi
  gzip "-${GZIP_LEVEL}" < "$copy" > "$2"
  rc=$?
  rm -rf "$dir"
  return "$rc"
}

sqlite_tables() {
  local copy count
  copy=$(mktemp "$BACKUP_DIR/.sqlite.$1.XXXXXX") || return 1
  if gunzip -c "$2" > "$copy" && count=$(sqlite_count "$copy"); then
    rm -f "$copy"
    printf '%s\n' "$count"
    return 0
  fi
  rm -f "$copy"
  return 1
}

sqlite_verify() {
  local copy res count fk=""
  copy=$(mktemp "$BACKUP_DIR/.sqlite.$1.XXXXXX") || return 1
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
  if [ -n "${EXCLUDE_TABLE_DATA:-}" ]; then
    fk=$(sqlite3 "$copy" 'PRAGMA foreign_key_check' 2>&1) || fk="check failed: $fk"
  fi
  rm -f "$copy"
  [ "$res" = ok ] || {
    log "ERROR: $1 integrity_check: $res" >&2
    return 1
  }
  [ "$count" = "$3" ] || {
    log "ERROR: $1 restored $count tables, expected $3" >&2
    return 1
  }
  [ -z "$fk" ] || {
    log "ERROR: $1 foreign key violations after excluding rows: $fk" >&2
    return 1
  }
}
