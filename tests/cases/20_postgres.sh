#!/usr/bin/env bash
# shellcheck shell=bash

pg_env=(-e DRIVER=postgres -e DB_HOST=postgres -e DB_USER=dbb -e DB_PASSWORD=pgpass -e "DATABASES=app,app2")

scratch_dbs() {
  compose exec -T postgres psql -U dbb -d app -Atc "select count(*) from pg_database where datname like 'dbb_verify_%'"
}

test_postgres_backup_and_verify() {
  local bk bk2 out rows
  bk=$(new_volume pg_bk)
  bk2=$(new_volume pg_bk2)
  dbb "$bk" "${pg_env[@]}" -e HC_PING_URL=http://mockping:8080/t_pg -- backup > /dev/null
  out=$(in_vol "$bk" 'cd /backups
echo "app tables:$(gunzip -c app/latest.sql.gz | grep -c "^CREATE TABLE")"
echo "app2 tables:$(gunzip -c app2/latest.sql.gz | grep -c "^CREATE TABLE")"
echo "hello:$(gunzip -c app/latest.sql.gz | grep -c hello)"
ls')
  assert_contains "$out" "app tables:2" "app table count"
  assert_contains "$out" "app2 tables:1" "app2 table count"
  assert_contains "$out" "hello:1" "app data in dump"
  assert_contains "$out" "app2" "second database dumped"
  ping_seen /t_pg || fail "success ping not sent"

  dbb "$bk" "${pg_env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_pg_v -- verify > /dev/null \
    || fail "verify of good postgres dumps failed"
  ping_seen /t_pg_v || fail "verify ping not sent"
  assert_eq "$(scratch_dbs)" "0" "scratch databases dropped"

  dbb "$bk2" "${pg_env[@]}" -e DATABASES=app -e EXTRA_OPTS=--exclude-table-data=notes -- backup > /dev/null
  rows=$(in_vol "$bk2" 'gunzip -c /backups/app/latest.sql.gz | grep -c hello || true')
  assert_eq "$rows" "0" "EXTRA_OPTS exclude-table-data honoured"
  pass "postgres backup and verify"
}

test_postgres_wrong_password() {
  local bk rc=0 out
  bk=$(new_volume pgbad_bk)
  dbb "$bk" "${pg_env[@]}" -e DB_PASSWORD=wrong -e HC_PING_URL=http://mockping:8080/t_pg_bad -- backup > /dev/null 2>&1 || rc=$?
  assert_eq "$rc" 1 "wrong password exit code"
  ping_seen /t_pg_bad/fail || fail "fail ping not sent"
  out=$(in_vol "$bk" 'ls -A /backups')
  assert_eq "$out" "" "nothing left behind after failed dumps"
  pass "postgres wrong password"
}

test_postgres_verify_fails_on_zero_tables() {
  local bk out
  bk=$(new_volume pgmis_bk)
  dbb "$bk" "${pg_env[@]}" -- backup > /dev/null
  in_vol "$bk" 'cd /backups; for f in app/$(readlink app/latest.sql.gz) app2/$(readlink app2/latest.sql.gz); do printf "SELECT 1;\n" | gzip > "$f"; done'
  if out=$(dbb "$bk" "${pg_env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_pg_mis -- verify 2>&1); then
    fail "verify accepted a dump with no tables"
  fi
  assert_contains "$out" "restored no tables" "no-tables reason"
  ping_seen /t_pg_mis/fail || fail "fail ping not sent"
  assert_eq "$(scratch_dbs)" "0" "scratch databases dropped after a failed verify"
  pass "postgres verify fails on zero tables"
}

test_postgres_verify_detects_corrupt_dump() {
  local bk out
  bk=$(new_volume pgcor_bk)
  dbb "$bk" "${pg_env[@]}" -e DATABASES=app -- backup > /dev/null
  in_vol "$bk" 'cd /backups/app; f=$(readlink latest.sql.gz); printf "THIS IS NOT SQL;\n" | gzip > "$f"'
  if out=$(dbb "$bk" "${pg_env[@]}" -e DATABASES=app -e HC_VERIFY_PING_URL=http://mockping:8080/t_pg_cor -- verify 2>&1); then
    fail "verify accepted a corrupt dump"
  fi
  assert_contains "$out" "syntax error" "psql rejected the corrupt dump"
  ping_seen /t_pg_cor/fail || fail "fail ping not sent"
  assert_eq "$(scratch_dbs)" "0" "scratch database dropped after a corrupt restore"
  pass "postgres verify detects corrupt dump"
}

test_postgres_list_edge_cases() {
  local bk out rc=0
  bk=$(new_volume pglist_bk)
  dbb "$bk" "${pg_env[@]}" -e "DATABASES=app, app2" -- backup > /dev/null \
    || fail "backup with whitespace in DATABASES failed"
  out=$(in_vol "$bk" 'ls /backups/app2')
  assert_contains "$out" "latest.sql.gz" "database name trimmed"
  out=$(dbb "$bk" "${pg_env[@]}" -e DATABASES=, -- check 2>&1) || rc=$?
  assert_eq "$rc" 1 "empty DATABASES exit code"
  assert_contains "$out" "databases lists no databases" "empty DATABASES message"
  pass "postgres list edge cases"
}

test_postgres_double_compression_fails() {
  local bk out rc=0
  bk=$(new_volume pgz_bk)
  out=$(dbb "$bk" "${pg_env[@]}" -e DATABASES=app -e EXTRA_OPTS=-Z6 \
    -e HC_PING_URL=http://mockping:8080/t_pg_z -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "-Z in EXTRA_OPTS exit code"
  assert_contains "$out" "contains no tables" "-Z in EXTRA_OPTS message"
  ping_seen /t_pg_z/fail || fail "fail ping not sent"
  pass "postgres double compression fails"
}


test_postgres_dedicated_role() {
  local bk out
  bk=$(new_volume pgro_bk)
  local -a env=(-e DRIVER=postgres -e DB_HOST=postgres -e DB_USER=dbb_ro -e DB_PASSWORD=ropw -e DATABASES=app)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup as a read-only role failed"
  dbb "$bk" "${env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_pg_ro -- verify > /dev/null \
    || fail "verify as a read-only role failed"
  ping_seen /t_pg_ro || fail "verify ping not sent"
  out=$(in_vol "$bk" 'gunzip -c /backups/app/latest.sql.gz | grep -c "OWNER TO" || true')
  assert_eq "$out" "0" "the dump carries no ownership statements"
  pass "postgres dedicated role"
}


test_postgres_exclude_table_data() {
  local bk out
  bk=$(new_volume pgexcl_bk)
  local -a env=("${pg_env[@]}" -e DATABASES=app -e EXCLUDE_TABLE_DATA=notes)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup with excluded rows failed"
  out=$(in_vol "$bk" 'cd /backups/app
echo "rows:$(gunzip -c latest.sql.gz | grep -c hello || true)"
echo "tables:$(gunzip -c latest.sql.gz | grep -c "^CREATE TABLE")"')
  assert_contains "$out" "rows:0" "rows of the excluded table are gone"
  assert_contains "$out" "tables:2" "the schema of the excluded table is kept"
  dbb "$bk" "${env[@]}" -- verify > /dev/null || fail "verify after excluding rows failed"
  pass "postgres exclude table data"
}
