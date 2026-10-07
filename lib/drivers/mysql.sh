# shellcheck shell=bash

MYSQL_ARGS=()

mysql_validate() {
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

mysql_orphans() {
  local queries query n total=0
  queries=$(mariadb "${MYSQL_ARGS[@]}" -N -s "$1" << 'SQL'
select concat('select count(*) from `', replace(k.TABLE_NAME, '`', '``'), '` c left join `', replace(k.REFERENCED_TABLE_NAME, '`', '``'), '` p on ',
  group_concat(concat('c.`', replace(k.COLUMN_NAME, '`', '``'), '` = p.`', replace(k.REFERENCED_COLUMN_NAME, '`', '``'), '`') separator ' and '),
  ' where p.`', replace(min(k.REFERENCED_COLUMN_NAME), '`', '``'), '` is null and ',
  group_concat(concat('c.`', replace(k.COLUMN_NAME, '`', '``'), '` is not null') separator ' and '))
from information_schema.KEY_COLUMN_USAGE k
where k.TABLE_SCHEMA = database() and k.REFERENCED_TABLE_SCHEMA = database() and k.REFERENCED_TABLE_NAME is not null
group by k.CONSTRAINT_NAME, k.TABLE_NAME, k.REFERENCED_TABLE_NAME
SQL
  ) || return 1
  while IFS= read -r query; do
    [ -n "$query" ] || continue
    n=$(mariadb "${MYSQL_ARGS[@]}" -N -s "$1" -e "$query") || return 1
    total=$((total + n))
  done <<< "$queries"
  printf '%s\n' "$total"
}

mysql_units() {
  split_list "$DATABASES"
}

mysql_suffix() {
  printf '%s' '.sql.gz'
}

mysql_dump() {
  local opts=() table
  read -ra opts <<< "$EXTRA_OPTS"
  while IFS= read -r table; do
    case "$table" in
      *.*) opts+=("--ignore-table-data=$table") ;;
      *) opts+=("--ignore-table-data=$1.$table") ;;
    esac
  done < <(split_list "$EXCLUDE_TABLE_DATA")
  mariadb-dump "${MYSQL_ARGS[@]}" --single-transaction --no-tablespaces --routines --triggers ${opts[@]+"${opts[@]}"} "$1" | gzip "-${GZIP_LEVEL}" > "$2"
}

mysql_tables() {
  gunzip -c "$2" | awk '/^CREATE TABLE /{n++} END{print n+0}'
}

mysql_verify() {
  local target=$1 dump=$2 expected=$3 scratch="dbb_verify_$1" got="" orphans=0 rc=0
  mariadb "${MYSQL_ARGS[@]}" -e "DROP DATABASE IF EXISTS \`$scratch\`" || return 1
  mariadb "${MYSQL_ARGS[@]}" -e "CREATE DATABASE \`$scratch\`" || return 1
  if gunzip -c "$dump" | sed -E 's/DEFINER=`[^`]*`@`[^`]*`//g' | mariadb "${MYSQL_ARGS[@]}" --one-database "$scratch"; then
    got=$(mysql_table_count "$scratch") || rc=1
    if [ -n "${EXCLUDE_TABLE_DATA:-}" ]; then
      orphans=$(mysql_orphans "$scratch") || rc=1
    fi
  else
    rc=1
  fi
  mariadb "${MYSQL_ARGS[@]}" -e "DROP DATABASE IF EXISTS \`$scratch\`" || log "WARN: could not drop $scratch" >&2
  [ "$rc" -eq 0 ] || return 1
  [ "$got" = "$expected" ] || {
    log "ERROR: $target restored $got tables, expected $expected" >&2
    return 1
  }
  [ "$orphans" = 0 ] || {
    log "ERROR: $target restored $orphans orphaned child rows after excluding table rows" >&2
    return 1
  }
}
