#!/usr/bin/env bash
# shellcheck shell=bash

test_usage_without_args() {
  local out rc=0
  out=$(docker run --rm --entrypoint db-backup "$IMAGE" 2>&1) || rc=$?
  assert_eq "$rc" 2 "usage exit code"
  assert_contains "$out" "usage: db-backup" "usage text"
  pass "usage without args"
}

test_image_size() {
  local size
  size=$(docker image inspect "$IMAGE" -f '{{.Size}}')
  [ "$size" -lt 260000000 ] || fail "image is $((size / 1000000)) MB; the full mariadb-client (about 66 MB more, mostly Perl) has probably crept back in"
  pass "image size $((size / 1000000)) MB"
}

test_env_validation() {
  local bk out rc
  bk=$(new_volume env_bk)

  rc=0
  out=$(dbb "$bk" -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "missing DRIVER exit code"
  assert_contains "$out" "DRIVER is required" "missing DRIVER message"

  rc=0
  out=$(dbb "$bk" -e DRIVER=oracle -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "unknown DRIVER exit code"
  assert_contains "$out" "DRIVER must be postgres, mysql or sqlite" "unknown DRIVER message"

  rc=0
  out=$(dbb "$bk" -e DRIVER=sqlite -e BACKUP_DIR=/nonexistent -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "bad BACKUP_DIR exit code"
  assert_contains "$out" "not a writable directory" "bad BACKUP_DIR message"

  rc=0
  out=$(dbb "$bk" -e DRIVER=sqlite -e KEEP_MINS=0 -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "KEEP_MINS=0 exit code"
  assert_contains "$out" "KEEP_MINS must be a positive integer" "KEEP_MINS message"

  rc=0
  out=$(dbb "$bk" -e DRIVER=sqlite -e KEEP_DAYS=abc -- backup 2>&1) || rc=$?
  assert_eq "$rc" 1 "KEEP_DAYS=abc exit code"
  assert_contains "$out" "KEEP_DAYS must be a non-negative integer" "KEEP_DAYS message"
  pass "env validation"
}
