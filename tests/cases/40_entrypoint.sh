#!/usr/bin/env bash
# shellcheck shell=bash

test_entrypoint_rejects_bad_config() {
  local out rc=0
  out=$(docker run --rm -e DRIVER=sqlite "$IMAGE" 2>&1) || rc=$?
  assert_eq "$rc" 1 "bad config exit code"
  assert_contains "$out" "SQLITE_PATHS is required" "bad config message"
  pass "entrypoint rejects bad config"
}

test_entrypoint_schedules_and_runs_on_start() {
  local data bk name tab
  data=$(new_volume entry_data)
  bk=$(new_volume entry_bk)
  name=dbbtest_entry_$$
  sqlite_data "$data"
  docker run -d --name "$name" --network "$NETWORK" -v "$bk:/backups" -v "$data:/data" \
    -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db -e BACKUP_ON_START=TRUE \
    -e SCHEDULE='0 3 * * *' -e VERIFY_SCHEDULE='30 4 * * 0' \
    -e HC_PING_URL=http://mockping:8080/t_entry "$IMAGE" > /dev/null
  if ! wait_for 30 'ping_seen /t_entry'; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "no ping from the dump at container start"
  fi
  tab=$(docker exec "$name" cat /tmp/crontab)
  docker exec "$name" pgrep -x supercronic > /dev/null || { docker rm -fv "$name" > /dev/null; fail "supercronic is not running"; }
  docker rm -fv "$name" > /dev/null
  assert_contains "$tab" "0 3 * * * db-backup backup" "backup schedule"
  assert_contains "$tab" "30 4 * * 0 db-backup verify" "verify schedule"
  pass "entrypoint schedules and runs on start"
}

test_entrypoint_cron_fires() {
  local data bk name
  data=$(new_volume cron_data)
  bk=$(new_volume cron_bk)
  name=dbbtest_cron_$$
  sqlite_data "$data"
  docker run -d --name "$name" --network "$NETWORK" -v "$bk:/backups" -v "$data:/data" \
    -e DRIVER=sqlite -e SQLITE_PATHS=/data/app.db \
    -e SCHEDULE='*/5 * * * * * *' \
    -e HC_PING_URL=http://mockping:8080/t_cron "$IMAGE" > /dev/null
  if ! wait_for 25 'ping_seen /t_cron'; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "supercronic never ran db-backup backup"
  fi
  docker rm -fv "$name" > /dev/null
  pass "entrypoint cron fires"
}
