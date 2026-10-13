#!/usr/bin/env bash
# shellcheck shell=bash

HEARTBEAT_CONFIG='
heartbeat:
  ping_url: http://mockping:8080/%s
  schedule: "5 3 * * *"
  verify_ping_url_env: VERIFY_URL
  verify_schedule: "30 6 * * 0"
targets:
  app:
    driver: sqlite
    paths: {app: /data/app.db}
    verify_schedule: "0 5 * * 0"
'

write_heartbeat_config() {
  reset_config
  # shellcheck disable=SC2059
  printf "$HEARTBEAT_CONFIG" "$1" | write_config
}

test_heartbeat_pings_after_a_recorded_backup_and_verify() {
  local data bk
  data=$(new_volume hb_data)
  bk=$(new_volume hb_bk)
  sqlite_data "$data"
  write_heartbeat_config t_hb_ok
  dbbr "$bk" -v "$data:/data" -- app backup > /dev/null || fail "backup failed"
  dbbr "$bk" -v "$data:/data" -- app verify > /dev/null || fail "verify failed"
  dbhb "$bk" -e VERIFY_URL=http://mockping:8080/t_hb_ok_v -- dumps > /dev/null || fail "dumps heartbeat failed"
  ping_seen /t_hb_ok || fail "no dumps heartbeat ping"
  dbhb "$bk" -e VERIFY_URL=http://mockping:8080/t_hb_ok_v -- verify > /dev/null || fail "verify heartbeat failed"
  ping_seen /t_hb_ok_v || fail "no verify heartbeat ping"
  ! ping_seen /t_hb_ok/fail || fail "a fail ping was sent for a healthy backup"
  pass "heartbeat pings after a recorded backup and verify"
}

test_heartbeat_reports_a_missing_backup_as_a_failure() {
  local bk rc=0 out
  bk=$(new_volume hbf_bk)
  write_heartbeat_config t_hb_missing
  out=$(dbhb "$bk" -e VERIFY_URL=http://mockping:8080/t_hb_missing_v -- dumps 2>&1) || rc=$?
  assert_eq "$rc" 1 "exit code"
  assert_contains "$out" "app: no backup recorded" "the reason is logged"
  ping_seen /t_hb_missing/fail || fail "no fail ping for a missing backup"
  pass "heartbeat reports a missing backup as a failure"
}

test_heartbeat_reports_a_failed_backup() {
  local data bk rc=0
  data=$(new_volume hbb_data)
  bk=$(new_volume hbb_bk)
  write_heartbeat_config t_hb_bad
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'printf garbage > /data/app.db'
  dbbr "$bk" -v "$data:/data" -- app backup > /dev/null 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || fail "a backup of a corrupt file must fail"
  dbhb "$bk" -e VERIFY_URL=http://mockping:8080/t_hb_bad_v -- dumps > /dev/null 2>&1 || true
  ping_seen /t_hb_bad/fail || fail "no fail ping after a failed backup"
  pass "heartbeat reports a failed backup"
}

test_heartbeat_is_scheduled_by_the_entrypoint() {
  local data bk name tab
  data=$(new_volume hbs_data)
  bk=$(new_volume hbs_bk)
  name=dbbtest_hbs_$$
  sqlite_data "$data"
  write_heartbeat_config t_hb_sched
  docker run -d --name "$name" --network "$NETWORK" -v "$data:/data" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" \
    -e VERIFY_URL=http://mockping:8080/t_hb_sched_v "$IMAGE" > /dev/null
  if ! wait_for 20 "docker exec $name pgrep -x supercronic > /dev/null 2>&1"; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "the container did not reach supercronic"
  fi
  tab=$(docker exec "$name" cat /tmp/crontab)
  docker rm -fv "$name" > /dev/null
  assert_contains "$tab" "5 3 * * * db-backup-heartbeat dumps" "dumps heartbeat is scheduled"
  assert_contains "$tab" "30 6 * * 0 db-backup-heartbeat verify" "verify heartbeat is scheduled"
  pass "heartbeat is scheduled by the entrypoint"
}

test_heartbeat_without_configuration_refuses() {
  local bk rc=0 out
  bk=$(new_volume hbn_bk)
  reset_config
  write_config <<'CONFIG'
targets:
  app: {driver: sqlite, paths: {app: /data/app.db}}
CONFIG
  out=$(dbhb "$bk" -- dumps 2>&1) || rc=$?
  assert_eq "$rc" 1 "exit code"
  assert_contains "$out" "no heartbeat is configured" "message"
  pass "heartbeat without configuration refuses"
}
