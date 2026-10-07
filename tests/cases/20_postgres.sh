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
  out=$(in_vol "$bk" 'cd /backups/last
echo "app tables:$(cat app-[0-9]*.sql.gz.tables)"
echo "app2 tables:$(cat app2-[0-9]*.sql.gz.tables)"
echo "hello:$(gunzip -c app-latest.sql.gz | grep -c hello)"
ls')
  assert_contains "$out" "app tables:2" "app table count"
  assert_contains "$out" "app2 tables:1" "app2 table count"
  assert_contains "$out" "hello:1" "app data in dump"
  assert_contains "$out" "app2-latest.sql.gz" "second database dumped"
  ping_seen /t_pg || fail "success ping not sent"

  dbb "$bk" "${pg_env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_pg_v -- verify > /dev/null \
    || fail "verify of good postgres dumps failed"
  ping_seen /t_pg_v || fail "verify ping not sent"
  assert_eq "$(scratch_dbs)" "0" "scratch databases dropped"

  dbb "$bk2" "${pg_env[@]}" -e DATABASES=app -e EXTRA_OPTS=--exclude-table-data=notes -- backup > /dev/null
  rows=$(in_vol "$bk2" 'gunzip -c /backups/last/app-latest.sql.gz | grep -c hello || true')
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

test_postgres_verify_detects_mismatch() {
  local bk out
  bk=$(new_volume pgmis_bk)
  dbb "$bk" "${pg_env[@]}" -- backup > /dev/null
  in_vol "$bk" 'cd /backups/last; for f in app-[0-9]*.sql.gz.tables; do echo 99 > "$f"; done'
  if out=$(dbb "$bk" "${pg_env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_pg_mis -- verify 2>&1); then
    fail "verify accepted a table count mismatch"
  fi
  assert_contains "$out" "restored 2 tables, expected 99" "mismatch reason"
  ping_seen /t_pg_mis/fail || fail "fail ping not sent"
  assert_eq "$(scratch_dbs)" "0" "scratch databases dropped after a failed verify"
  pass "postgres verify detects mismatch"
}

test_postgres_verify_detects_corrupt_dump() {
  local bk out
  bk=$(new_volume pgcor_bk)
  dbb "$bk" "${pg_env[@]}" -e DATABASES=app -- backup > /dev/null
  in_vol "$bk" 'cd /backups/last; f=$(readlink app-latest.sql.gz); printf "THIS IS NOT SQL;\n" | gzip > "$f"'
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
  out=$(in_vol "$bk" 'ls /backups/last')
  assert_contains "$out" "app2-latest.sql.gz" "database name trimmed"
  out=$(dbb "$bk" "${pg_env[@]}" -e DATABASES=, -- check 2>&1) || rc=$?
  assert_eq "$rc" 1 "empty DATABASES exit code"
  assert_contains "$out" "DATABASES lists no databases" "empty DATABASES message"
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
