#!/usr/bin/env bash
# shellcheck shell=bash

mysql_scratch_dbs() {
  compose exec -T -e MYSQL_PWD=rootpw mysql mysql -uroot -N -e "show databases like 'dbb_verify%'"
}

test_mysql_backup_and_verify() {
  local bk out pwfile=${TMPDIR:-/tmp}/dbbtest_pw_$$
  bk=$(new_volume my_bk)
  printf 'bkppw\n' > "$pwfile"
  chmod 644 "$pwfile"
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD_FILE=/run/pw -e DATABASES=shop -v "$pwfile:/run/pw:ro")

  dbb "$bk" "${env[@]}" -e HC_PING_URL=http://mockping:8080/t_my -- backup > /dev/null \
    || fail "mysql backup failed"
  out=$(in_vol "$bk" 'cd /backups/shop
echo "tables:$(gunzip -c latest.sql.gz | grep -c "^CREATE TABLE")"
echo "posts:$(gunzip -c latest.sql.gz | grep -c "CREATE TABLE .posts.")"')
  assert_contains "$out" "tables:2" "table count"
  assert_contains "$out" "posts:1" "posts table in dump"
  ping_seen /t_my || fail "success ping not sent"

  dbb "$bk" "${env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_my_v -- verify > /dev/null \
    || fail "mysql verify failed"
  ping_seen /t_my_v || fail "verify ping not sent"
  assert_eq "$(mysql_scratch_dbs)" "" "scratch database dropped"
  rm -f "$pwfile"
  pass "mysql backup and verify"
}

test_mysql_verify_detects_corrupt_dump() {
  local bk out
  bk=$(new_volume mycor_bk)
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=shop)
  dbb "$bk" "${env[@]}" -- backup > /dev/null
  in_vol "$bk" 'cd /backups/shop; f=$(readlink latest.sql.gz); printf "THIS IS NOT SQL;\n" | gzip > "$f"'
  if out=$(dbb "$bk" "${env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_my_cor -- verify 2>&1); then
    fail "verify accepted a corrupt dump"
  fi
  assert_contains "$out" "syntax" "mysql rejected the corrupt dump"
  ping_seen /t_my_cor/fail || fail "fail ping not sent"
  assert_eq "$(mysql_scratch_dbs)" "" "scratch database dropped after a corrupt restore"
  pass "mysql verify detects corrupt dump"
}

test_mysql_wrong_password() {
  local bk rc=0
  bk=$(new_volume mybad_bk)
  dbb "$bk" -e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=wrong -e DATABASES=shop \
    -e HC_PING_URL=http://mockping:8080/t_my_bad -- backup > /dev/null 2>&1 || rc=$?
  assert_eq "$rc" 1 "wrong password exit code"
  ping_seen /t_my_bad/fail || fail "fail ping not sent"
  assert_eq "$(in_vol "$bk" 'ls -A /backups')" "" "nothing left behind"
  pass "mysql wrong password"
}

test_mysql_verify_never_touches_live_database() {
  local bk rows
  bk=$(new_volume myguard_bk)
  mysql_seed << 'SQL'
CREATE DATABASE IF NOT EXISTS guard;
CREATE TABLE IF NOT EXISTS guard.t (id INT);
DELETE FROM guard.t;
INSERT INTO guard.t VALUES (1), (2);
GRANT SELECT, SHOW VIEW, TRIGGER ON guard.* TO 'bkp'@'%';
SQL
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=guard -e EXTRA_OPTS=--databases)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup with --databases failed"
  mysql_seed <<< "INSERT INTO guard.t VALUES (99);"
  dbb "$bk" "${env[@]}" -- verify > /dev/null 2>&1 || true
  rows=$(compose exec -T -e MYSQL_PWD=rootpw mysql mysql -uroot -N -e "select group_concat(id order by id) from guard.t")
  assert_eq "$rows" "1,2,99" "live rows survive verify"
  pass "mysql verify never touches the live database"
}

test_mysql_dump_has_routines_and_triggers() {
  local bk out
  bk=$(new_volume myobj_bk)
  dbb "$bk" -e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=shop -- backup > /dev/null \
    || fail "backup as the least-privilege user failed"
  out=$(in_vol "$bk" 'cd /backups/shop
echo "functions:$(gunzip -c latest.sql.gz | grep -c "FUNCTION .one")"
echo "triggers:$(gunzip -c latest.sql.gz | grep -c "TRIGGER .tags_default")"')
  assert_not_contains "$out" "functions:0" "the function is in the dump"
  assert_not_contains "$out" "triggers:0" "the trigger is in the dump"
  pass "mysql dump has routines and triggers"
}


test_mysql_exclude_table_data() {
  local bk out
  bk=$(new_volume myexcl_bk)
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=shop -e EXCLUDE_TABLE_DATA=posts)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup with excluded rows failed"
  out=$(in_vol "$bk" 'cd /backups/shop
echo "schema:$(gunzip -c latest.sql.gz | grep -c "CREATE TABLE .posts.")"
echo "rows:$(gunzip -c latest.sql.gz | grep -c "INSERT INTO .posts.")"
echo "tables:$(gunzip -c latest.sql.gz | grep -c "^CREATE TABLE")"')
  assert_contains "$out" "schema:1" "the schema of the excluded table is kept"
  assert_contains "$out" "rows:0" "rows of the excluded table are gone"
  assert_contains "$out" "tables:2" "both tables are counted"
  dbb "$bk" "${env[@]}" -- verify > /dev/null || fail "verify after excluding rows failed"
  pass "mysql exclude table data"
}

test_mysql_exclude_leaving_orphans_is_caught() {
  local bk out
  bk=$(new_volume myorph_bk)
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=fkshop -e EXCLUDE_TABLE_DATA=parent)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup failed"
  if out=$(dbb "$bk" "${env[@]}" -- verify 2>&1); then
    fail "verify accepted orphaned child rows"
  fi
  assert_contains "$out" "orphaned" "verify names the reason"
  pass "mysql exclude leaving orphans is caught"
}

test_mysql_orphan_check_handles_quoted_names() {
  local bk out
  bk=$(new_volume myq_bk)
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=fkq -e 'EXCLUDE_TABLE_DATA=pa`rent')
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup failed"
  if out=$(dbb "$bk" "${env[@]}" -- verify 2>&1); then
    fail "verify accepted orphaned child rows"
  fi
  assert_contains "$out" "orphaned" "a table name containing a backtick is checked, not a syntax error"
  pass "mysql orphan check handles quoted names"
}
