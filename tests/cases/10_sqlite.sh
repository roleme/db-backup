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
  assert_contains "$out" "SQLITE_PATHS is required" "message"
  pass "sqlite requires SQLITE_PATHS"
}

test_sqlite_backup_layout() {
  local data bk out d
  data=$(new_volume layout_data)
  bk=$(new_volume layout_bk)
  sqlite_data "$data"
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db \
    -e HC_PING_URL=http://mockping:8080/t_layout -- backup > /dev/null
  out=$(in_vol "$bk" 'cd /backups
for d in last daily weekly monthly; do test -L $d/app-latest.db.gz && echo "latest:$d"; done
echo "links:$(find last -name "app-[0-9]*.db.gz" -printf "%n")"
echo "tables:$(cat last/app-[0-9]*.db.gz.tables)"
gunzip -c last/app-latest.db.gz > /tmp/x.db
echo "rows:$(sqlite3 /tmp/x.db "select count(*) from notes")"')
  for d in last daily weekly monthly; do
    assert_contains "$out" "latest:$d" "latest symlink in $d"
  done
  assert_contains "$out" "links:4" "dump hardlinked into four tiers"
  assert_contains "$out" "tables:2" "table count recorded"
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

  in_vol "$bk" 'cd /backups/last; f=$(readlink app-latest.db.gz); printf garbage | gzip > "$f"'
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
  before=$(in_vol "$bk" 'readlink /backups/last/app-latest.db.gz')

  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'printf garbage > /data/app.db'
  rc=0
  dbb "$bk" "${env[@]}" -e SQLITE_PATHS=/data/app.db -- backup > /dev/null 2>&1 || rc=$?
  assert_eq "$rc" 1 "corrupt database exit code"
  after=$(in_vol "$bk" 'readlink /backups/last/app-latest.db.gz')
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
  in_vol "$bk" 'cd /backups; mkdir -p last daily weekly monthly
touch -d 2020-01-01 daily/app-20200101.db.gz daily/app-extra-20200101.db.gz weekly/app-202001.db.gz monthly/app-202001.db.gz'
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  out=$(in_vol "$bk" 'cd /backups; ls daily')
  assert_not_contains "$out" "app-20200101.db.gz" "old daily pruned"
  assert_contains "$out" "app-extra-20200101.db.gz" "other job's daily kept"
  out=$(in_vol "$bk" 'cd /backups; ls weekly monthly')
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
  assert_contains "$out" "SQLITE_PATHS lists no files" "empty SQLITE_PATHS message"
  pass "sqlite no targets"
}

test_stale_partials_removed() {
  local data bk out
  data=$(new_volume stale_data)
  bk=$(new_volume stale_bk)
  sqlite_data "$data"
  in_vol "$bk" 'cd /backups; touch -d "2 hours ago" .app.partial.OLD111 .sqlite.OLD222; touch .app.partial.FRESH3'
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  out=$(in_vol "$bk" 'ls -A /backups')
  assert_not_contains "$out" "OLD111" "stale partial removed"
  assert_not_contains "$out" "OLD222" "stale sqlite temp removed"
  assert_contains "$out" "FRESH3" "recent partial kept"
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
