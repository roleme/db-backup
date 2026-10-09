#!/usr/bin/env bash
# shellcheck shell=bash

test_sqlite_wal_live_writer() {
  local data bk writer out
  data=$(new_volume wal_data)
  bk=$(new_volume wal_bk)
  sqlite_data "$data"
  writer=dbbtest_writer_$$
  docker run -d --name "$writer" -v "$data:/data" --entrypoint sqlite3 "$IMAGE" /data/app.db \
    "PRAGMA journal_mode=WAL;" "PRAGMA wal_autocheckpoint=0;" "INSERT INTO notes VALUES (3);" ".shell sleep 60" > /dev/null
  sleep 3
  if ! dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null; then
    docker rm -fv "$writer" > /dev/null
    fail "backup against a live WAL writer failed"
  fi
  docker rm -fv "$writer" > /dev/null
  out=$(in_vol "$bk" 'gunzip -c /backups/app/latest.db.gz > /tmp/x.db; sqlite3 /tmp/x.db "select group_concat(x) from notes"')
  assert_eq "$out" "1,2,3" "uncheckpointed WAL rows are in the dump"
  pass "sqlite WAL live writer"
}

test_sqlite_leaves_no_root_files() {
  local data bk out
  data=$(new_volume owner_data)
  bk=$(new_volume owner_bk)
  sqlite_data "$data"
  docker run --rm -v "$data:/data" --user 1000:1000 --entrypoint sqlite3 "$IMAGE" /data/app.db "PRAGMA journal_mode=WAL;" > /dev/null
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -- backup > /dev/null
  out=$(docker run --rm -v "$data:/data" --entrypoint find "$IMAGE" /data -not -user 1000)
  assert_eq "$out" "" "no root-owned files left in the data directory"
  pass "sqlite leaves no root files"
}

test_sqlite_extra_paths() {
  local data bk out rc=0
  data=$(new_volume extra_data)
  bk=$(new_volume extra_bk)
  sqlite_data "$data"
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'mkdir -p /data/keys && echo secret > /data/keys/k.txt'
  local -a env=(-v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -e EXTRA_PATHS=/data/keys)
  dbb "$bk" "${env[@]}" -- backup > /dev/null
  out=$(in_vol "$bk" 'tar -tzf /backups/keys/latest.tar.gz')
  assert_contains "$out" "keys/k.txt" "archive content"
  dbb "$bk" "${env[@]}" -e HC_VERIFY_PING_URL=http://mockping:8080/t_extra_v -- verify > /dev/null \
    || fail "verify with extra paths failed"
  ping_seen /t_extra_v || fail "verify ping not sent"

  in_vol "$bk" 'cd /backups/keys; f=$(readlink latest.tar.gz); printf garbage > "$f"'
  if dbb "$bk" "${env[@]}" -- verify > /dev/null 2>&1; then
    fail "verify accepted a corrupt archive"
  fi
  out=$(dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -e EXTRA_PATHS=/data/nope -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "missing extra path exit code"
  pass "sqlite extra paths"
}

test_sqlite_name_edge_cases() {
  local data bk out rc=0
  data=$(new_volume names_data)
  bk=$(new_volume names_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'mkdir -p /data/a /data/b
sqlite3 "/data/my app.db" "create table t (x)"
sqlite3 /data/a/app.db "create table t (x)"
sqlite3 /data/b/app.db "create table t (x)"'

  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e "SQLITE_PATHS=/data/my app.db" -- backup > /dev/null
  dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e "SQLITE_PATHS=/data/my app.db" -- verify > /dev/null \
    || fail "verify of a path with spaces failed"
  out=$(in_vol "$bk" 'ls "/backups/my app"')
  assert_contains "$out" "latest.db.gz" "name with a space"

  out=$(dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/a/app.db,/data/b/app.db -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "duplicate basenames exit code"
  assert_contains "$out" "two files named app" "duplicate basenames message"
  pass "sqlite name edge cases"
}

test_extra_paths_duplicate() {
  local data bk out rc=0
  data=$(new_volume dupextra_data)
  bk=$(new_volume dupextra_bk)
  sqlite_data "$data"
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'mkdir -p /data/a/up /data/b/up; echo f > /data/a/up/f; echo g > /data/b/up/g'
  out=$(dbb "$bk" -v "$data:/data" -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -e EXTRA_PATHS=/data/a/up,/data/b/up -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "duplicate extra basenames exit code"
  assert_contains "$out" "extra_paths has two directories named up" "duplicate extra message"
  assert_eq "$(in_vol "$bk" 'ls -A /backups')" "" "nothing written when the config is invalid"
  pass "extra paths duplicate"
}

test_sqlite_named_paths_tell_same_file_names_apart() {
  local data bk out
  data=$(new_volume named_data)
  bk=$(new_volume named_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'mkdir -p /data/one /data/two && sqlite3 /data/one/db.db "create table a (x)" && sqlite3 /data/two/db.db "create table b (x); create table c (x)"'
  local -a env=(-v "$data:/data" -e DRIVER=sqlite)
  dbb "$bk" "${env[@]}" -e SQLITE_PATHS=one=/data/one/db.db,two=/data/two/db.db -- backup > /dev/null || fail "backup with named paths failed"
  out=$(in_vol "$bk" 'find /backups -name latest.db.gz')
  assert_contains "$out" "/backups/one/latest.db.gz" "first named dump"
  assert_contains "$out" "/backups/two/latest.db.gz" "second named dump"
  dbb "$bk" "${env[@]}" -e SQLITE_PATHS=one=/data/one/db.db,two=/data/two/db.db -- verify > /dev/null || fail "verify with named paths failed"
  if out=$(dbb "$bk" "${env[@]}" -e SQLITE_PATHS=/data/one/db.db,/data/two/db.db -- backup 2>&1); then
    fail "two files with the same name were accepted without names"
  fi
  assert_contains "$out" "two files named db" "unnamed collision message"
  pass "sqlite named paths tell same file names apart"
}
