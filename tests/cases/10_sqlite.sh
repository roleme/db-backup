#!/usr/bin/env bash
# shellcheck shell=bash

sqlite_data() {
  docker run --rm -v "$1:/data" --entrypoint chown "$IMAGE" 1000:1000 /data
  docker run --rm -v "$1:/data" --user 1000:1000 --entrypoint sqlite3 "$IMAGE" /data/app.db \
    "CREATE TABLE notes (x); INSERT INTO notes VALUES (1), (2); CREATE TABLE tags (y);"
}

test_sqlite_requires_paths() {
  local bk out rc=0
  bk=$(new_volume sqlreq_bk)
  out=$(dbb "$bk" -e DRIVER=sqlite -- check 2>&1) || rc=$?
  assert_eq "$rc" 1 "check without SQLITE_PATHS"
  assert_contains "$out" "paths is required" "message"
  pass "sqlite requires SQLITE_PATHS"
}

test_sqlite_backup_layout() {
  local data bk out
  data=$(new_volume layout_data)
  bk=$(new_volume layout_bk)
  sqlite_data "$data"
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db \
    -e HC_PING_URL=http://mockping:8080/t_layout -- backup > /dev/null
  out=$(in_vol "$bk" 'cd /backups
test -L app/latest.db.gz && echo "latest:app"
for d in last daily weekly monthly; do test -e app/$d/app-latest.db.gz && echo "tierlatest:$d"; done
echo "tiers:$(ls app | tr "\n" " ")"
echo "links:$(find app/last -name "app-[0-9]*.db.gz" -printf "%n")"
echo "sidecars:$(find /backups -name "*.tables" | wc -l)"
gunzip -c app/latest.db.gz > /tmp/x.db
echo "rows:$(sqlite3 /tmp/x.db "select count(*) from notes")"')
  assert_contains "$out" "latest:app" "latest pointer in the unit folder"
  assert_not_contains "$out" "tierlatest" "no latest link inside the tiers"
  assert_contains "$out" "tiers:daily last latest.db.gz monthly weekly " "unit folder layout"
  assert_contains "$out" "links:4" "dump hardlinked into four tiers"
  assert_contains "$out" "sidecars:0" "no table-count file is written"
  assert_contains "$out" "rows:2" "dump content"
  ping_seen /t_layout || fail "success ping not sent"
  ! ping_seen /t_layout/fail || fail "fail ping sent on success"
  pass "sqlite backup layout and ping"
}

test_sqlite_verify_and_corruption() {
  local data bk out
  data=$(new_volume verify_data)
  bk=$(new_volume verify_bk)
  sqlite_data "$data"
  local -a env=(-v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db)
  dbb "$bk" "${env[@]}" -- backup > /dev/null

  dbb "$bk" "${env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_verify_ok -- verify > /dev/null \
    || fail "verify of a good dump failed"
  ping_seen /t_verify_ok || fail "verify success ping not sent"

  in_vol "$bk" 'cd /backups/app; f=$(readlink latest.db.gz); printf garbage | gzip > "$f"'
  if out=$(dbb "$bk" "${env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_verify_bad -- verify 2>&1); then
    fail "verify accepted a corrupt dump"
  fi
  assert_contains "$out" "not a database" "verify failed because the dump is not a database"
  ping_seen /t_verify_bad/fail || fail "verify fail ping not sent"
  pass "sqlite verify and corruption"
}

test_sqlite_failure_keeps_previous_dump() {
  local data bk before after rc out
  data=$(new_volume keep_data)
  bk=$(new_volume keep_bk)
  sqlite_data "$data"
  local -a env=(-v "$data:/data" -e DRIVER=sqlite -e HC_PING_URL=http://mockping:8080/t_keep)
  dbb "$bk" "${env[@]}" -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  before=$(in_vol "$bk" 'readlink /backups/app/latest.db.gz')

  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'printf garbage > /data/app.db'
  rc=0
  dbb "$bk" "${env[@]}" -e SQLITE_PATHS=/data/app.db -- backup > /dev/null 2>&1 || rc=$?
  assert_eq "$rc" 1 "corrupt database exit code"
  after=$(in_vol "$bk" 'readlink /backups/app/latest.db.gz')
  assert_eq "$after" "$before" "latest unchanged after failed dump"
  out=$(in_vol "$bk" 'find /backups -name "*.partial*" -o -name ".sqlite.*"')
  assert_eq "$out" "" "no partial files left"
  ping_seen /t_keep/fail || fail "fail ping not sent for corrupt database"

  rc=0
  dbb "$bk" "${env[@]}" -e SQLITE_PATHS=/data/missing.db -- backup > /dev/null 2>&1 || rc=$?
  assert_eq "$rc" 1 "missing path exit code"
  out=$(docker run --rm -v "$data:/data" --entrypoint ls "$IMAGE" /data)
  assert_not_contains "$out" "missing.db" "missing database must not be created"
  pass "sqlite failure keeps previous dump"
}

test_sqlite_prune_isolated() {
  local data bk out
  data=$(new_volume prune_data)
  bk=$(new_volume prune_bk)
  sqlite_data "$data"
  in_vol "$bk" 'mkdir -p /backups/app; cd /backups/app; mkdir -p last daily weekly monthly
touch -d 2020-01-01 daily/app-20200101.db.gz daily/app-extra-20200101.db.gz weekly/app-202001.db.gz monthly/app-202001.db.gz'
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  out=$(in_vol "$bk" 'cd /backups/app; ls daily')
  assert_not_contains "$out" "app-20200101.db.gz" "old daily pruned"
  assert_contains "$out" "app-extra-20200101.db.gz" "other job's daily kept"
  out=$(in_vol "$bk" 'cd /backups/app; ls weekly monthly')
  assert_not_contains "$out" "app-202001.db.gz" "old weekly and monthly pruned"
  pass "sqlite prune isolated"
}

test_ping_failure_not_fatal() {
  local data bk out rc=0
  data=$(new_volume pingfail_data)
  bk=$(new_volume pingfail_bk)
  sqlite_data "$data"
  out=$(dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db \
    -e HC_PING_URL=http://nonexistent.invalid/x -- backup 2>&1) || rc=$?
  assert_eq "$rc" 0 "backup exit code with unreachable ping"
  assert_contains "$out" "WARN: ping" "warning logged"
  pass "ping failure not fatal"
}

test_sqlite_no_targets() {
  local bk out rc=0
  bk=$(new_volume notargets_bk)
  out=$(dbb "$bk" -e DRIVER=sqlite -e SQLITE_PATHS=, -- check 2>&1) || rc=$?
  assert_eq "$rc" 1 "empty SQLITE_PATHS exit code"
  assert_contains "$out" "paths lists no files" "empty SQLITE_PATHS message"
  pass "sqlite no targets"
}

test_stale_partials_removed() {
  local data bk out
  data=$(new_volume stale_data)
  bk=$(new_volume stale_bk)
  sqlite_data "$data"
  in_vol "$bk" 'cd /backups; touch -d "2 hours ago" .app.partial.OLD111 .sqlite.app.OLD222 .other.partial.OLD444 .sqlite.other.OLD555; touch .app.partial.FRESH3'
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  out=$(in_vol "$bk" 'ls -A /backups')
  assert_not_contains "$out" "OLD111" "stale partial removed"
  assert_not_contains "$out" "OLD222" "stale sqlite temp removed"
  assert_contains "$out" "FRESH3" "recent partial kept"
  assert_contains "$out" "OLD444" "another target's old partial is not touched"
  assert_contains "$out" "OLD555" "another target's old sqlite temp is not touched"
  pass "stale partials removed"
}

test_sqlite_empty_database_fails() {
  local data bk out rc=0
  data=$(new_volume empty_data)
  bk=$(new_volume empty_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c ': > /data/empty.db'
  out=$(dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/empty.db \
    -e HC_PING_URL=http://mockping:8080/t_empty -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "empty database exit code"
  assert_contains "$out" "contains no tables" "empty database message"
  ping_seen /t_empty/fail || fail "fail ping not sent"
  assert_eq "$(in_vol "$bk" 'ls -A /backups')" "" "nothing stored for an empty dump"
  pass "sqlite empty database fails"
}


test_sqlite_dump_is_compacted() {
  local data bk size
  data=$(new_volume compact_data)
  bk=$(new_volume compact_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/big.db "create table t (x blob); with recursive c(i) as (select 1 union all select i+1 from c where i < 3000) insert into t select randomblob(1024) from c; delete from t;"'
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/big.db -- backup > /dev/null
  size=$(in_vol "$bk" 'gunzip -c /backups/big/latest.db.gz | wc -c')
  [ "$size" -lt 100000 ] || fail "the dump holds $size bytes; a compacted copy is under 100000"
  pass "sqlite dump is compacted"
}

test_sqlite_exclude_table_data() {
  local data bk out
  data=$(new_volume excl_data)
  bk=$(new_volume excl_bk)
  sqlite_data "$data"
  local -a env=(-v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -e EXCLUDE_TABLE_DATA=notes)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup with excluded rows failed"
  out=$(in_vol "$bk" 'cd /backups/app
gunzip -c latest.db.gz > /tmp/x.db
echo "notes:$(sqlite3 /tmp/x.db "select count(*) from notes")"
echo "tables:$(sqlite3 /tmp/x.db "select count(*) from sqlite_master where type=\"table\"")"')
  assert_contains "$out" "notes:0" "rows of the excluded table are gone"
  assert_contains "$out" "tables:2" "the schema of the excluded table is kept"
  dbb "$bk" "${env[@]}" -- verify > /dev/null || fail "verify after excluding rows failed"
  pass "sqlite exclude table data"
}

test_sqlite_exclude_breaks_foreign_keys() {
  local data bk out
  data=$(new_volume fk_data)
  bk=$(new_volume fk_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/fk.db "create table parent (id integer primary key); create table child (id integer primary key, pid integer references parent(id)); insert into parent values (1); insert into child values (1, 1);"'
  local -a env=(-v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/fk.db -e EXCLUDE_TABLE_DATA=parent)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup failed"
  if out=$(dbb "$bk" "${env[@]}" -- verify 2>&1); then
    fail "verify accepted orphaned rows"
  fi
  assert_contains "$out" "foreign key" "verify names the reason"
  pass "sqlite exclude breaks foreign keys"
}

test_sqlite_exclude_unknown_table() {
  local data bk out rc=0
  data=$(new_volume exclbad_data)
  bk=$(new_volume exclbad_bk)
  sqlite_data "$data"
  out=$(dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -e EXCLUDE_TABLE_DATA=nope \
    -e HC_PING_URL=http://mockping:8080/t_excl_bad -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "unknown excluded table exit code"
  assert_contains "$out" "no such table" "unknown excluded table message"
  ping_seen /t_excl_bad/fail || fail "fail ping not sent"
  assert_eq "$(in_vol "$bk" 'ls -A /backups')" "" "nothing left behind"
  pass "sqlite exclude unknown table"
}

test_sqlite_exclude_does_not_fire_triggers() {
  local data bk out
  data=$(new_volume trg_data)
  bk=$(new_volume trg_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/trg.db "create table runs (id integer primary key, n integer); create table stats (n integer); create table users (id integer primary key); insert into runs values (1, 5); insert into stats values (42); insert into users values (1); create trigger runs_cleanup after delete on runs begin update stats set n = n - 1; delete from users; end;"'
  local -a env=(-v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/trg.db -e EXCLUDE_TABLE_DATA=runs)
  dbb "$bk" "${env[@]}" -- backup > /dev/null || fail "backup with excluded rows failed"
  out=$(in_vol "$bk" 'cd /backups/trg
gunzip -c latest.db.gz > /tmp/x.db
echo "runs:$(sqlite3 /tmp/x.db "select count(*) from runs")"
echo "users:$(sqlite3 /tmp/x.db "select count(*) from users")"
echo "stats:$(sqlite3 /tmp/x.db "select n from stats")"
echo "triggers:$(sqlite3 /tmp/x.db .schema | grep -c runs_cleanup)"')
  assert_contains "$out" "runs:0" "the excluded rows are gone"
  assert_contains "$out" "users:1" "another table is not emptied by a trigger"
  assert_contains "$out" "stats:42" "another table is not altered by a trigger"
  assert_contains "$out" "triggers:1" "the trigger itself is kept in the dump"
  dbb "$bk" "${env[@]}" -- verify > /dev/null || fail "verify failed"
  pass "sqlite exclude does not fire triggers"
}

test_prune_leaves_numeric_prefix_names() {
  local data bk out
  data=$(new_volume numpfx_data)
  bk=$(new_volume numpfx_bk)
  sqlite_data "$data"
  in_vol "$bk" 'mkdir -p /backups/app; cd /backups/app; mkdir -p last daily weekly monthly
touch -d 2020-01-01 daily/app-2-20200101.db.gz weekly/app-2-202001.db.gz monthly/app-2-202001.db.gz last/app-2-20200101-010000.db.gz'
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  out=$(in_vol "$bk" 'cd /backups/app; ls last daily weekly monthly')
  assert_contains "$out" "app-2-20200101.db.gz" "another target's old daily is kept"
  assert_contains "$out" "app-2-202001.db.gz" "another target's old weekly and monthly are kept"
  assert_contains "$out" "app-2-20200101-010000.db.gz" "another target's old last dump is kept"
  pass "prune leaves numeric prefix names"
}
