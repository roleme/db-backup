# shellcheck shell=bash

MYSQL_ARGS=()

driver_validate() {
  require_env DB_HOST DB_USER DATABASES
  local password
  password=$(secret DB_PASSWORD)
  [ -n "$password" ] || die "DB_PASSWORD or DB_PASSWORD_FILE is required"
  export MYSQL_PWD=$password
  MYSQL_ARGS=(-h "$DB_HOST" -P "${DB_PORT:-3306}" -u "$DB_USER" --skip-ssl-verify-server-cert)
  [ -n "$(split_list "$DATABASES")" ] || die "DATABASES lists no databases"
}

mysql_table_count() {
  mariadb "${MYSQL_ARGS[@]}" -N -s -e "select count(*) from information_schema.tables where table_schema = '$1' and table_type = 'BASE TABLE'"
}

driver_targets() {
  split_list "$DATABASES"
}

driver_suffix() {
  printf '%s' '.sql.gz'
}

driver_dump() {
  local opts=()
  read -ra opts <<< "$EXTRA_OPTS"
  mariadb-dump "${MYSQL_ARGS[@]}" --single-transaction --no-tablespaces --routines --triggers ${opts[@]+"${opts[@]}"} "$1" | gzip "-${GZIP_LEVEL}" > "$2"
}

driver_expected_tables() {
  gunzip -c "$2" | awk '/^CREATE TABLE /{n++} END{print n+0}'
}

driver_verify() {
  local target=$1 dump=$2 expected=$3 scratch="dbb_verify_$1" got="" rc=0
  mariadb "${MYSQL_ARGS[@]}" -e "DROP DATABASE IF EXISTS \`$scratch\`" || return 1
  mariadb "${MYSQL_ARGS[@]}" -e "CREATE DATABASE \`$scratch\`" || return 1
  if gunzip -c "$dump" | mariadb "${MYSQL_ARGS[@]}" --one-database "$scratch"; then
    got=$(mysql_table_count "$scratch") || rc=1
  else
    rc=1
  fi
  mariadb "${MYSQL_ARGS[@]}" -e "DROP DATABASE IF EXISTS \`$scratch\`" || log "WARN: could not drop $scratch" >&2
  [ "$rc" -eq 0 ] || return 1
  [ "$got" = "$expected" ] || {
    log "ERROR: $target restored $got tables, expected $expected" >&2
    return 1
  }
}
