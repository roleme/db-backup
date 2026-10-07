# shellcheck shell=bash

PG_TABLES_SQL="select count(*) from information_schema.tables where table_type = 'BASE TABLE' and table_schema not in ('pg_catalog', 'information_schema')"

driver_validate() {
  require_env DB_HOST DB_USER DATABASES
  local password
  password=$(secret DB_PASSWORD)
  [ -n "$password" ] || die "DB_PASSWORD or DB_PASSWORD_FILE is required"
  export PGHOST=$DB_HOST PGPORT=${DB_PORT:-5432} PGUSER=$DB_USER PGPASSWORD=$password
  [ -n "$(split_list "$DATABASES")" ] || die "DATABASES lists no databases"
}

driver_targets() {
  split_list "$DATABASES"
}

driver_suffix() {
  printf '%s' '.sql.gz'
}

pg_dump_bin() {
  local num bin
  num=$(psql -d postgres -Atq -c 'show server_version_num') || return 1
  bin=/usr/lib/postgresql/$((num / 10000))/bin/pg_dump
  [ -x "$bin" ] || {
    log "ERROR: no pg_dump for server version $num" >&2
    return 1
  }
  printf '%s' "$bin"
}

driver_dump() {
  local bin opts=()
  bin=$(pg_dump_bin) || return 1
  read -ra opts <<< "$EXTRA_OPTS"
  "$bin" -d "$1" --no-owner --no-privileges --lock-wait-timeout=60s ${opts[@]+"${opts[@]}"} | gzip "-${GZIP_LEVEL}" > "$2"
}

driver_expected_tables() {
  gunzip -c "$2" | awk '/^CREATE (UNLOGGED )?TABLE /{n++} END{print n+0}'
}

driver_verify() {
  local target=$1 dump=$2 expected=$3 scratch="dbb_verify_$1" got="" rc=0
  psql -d postgres -q -c "DROP DATABASE IF EXISTS \"$scratch\"" || return 1
  psql -d postgres -q -c "CREATE DATABASE \"$scratch\"" || return 1
  if gunzip -c "$dump" | psql -v ON_ERROR_STOP=1 -q -d "$scratch" > /dev/null; then
    got=$(psql -d "$scratch" -Atq -c "$PG_TABLES_SQL") || rc=1
  else
    rc=1
  fi
  psql -d postgres -q -c "DROP DATABASE IF EXISTS \"$scratch\"" || log "WARN: could not drop $scratch" >&2
  [ "$rc" -eq 0 ] || return 1
  [ "$got" = "$expected" ] || {
    log "ERROR: $target restored $got tables, expected $expected" >&2
    return 1
  }
}
