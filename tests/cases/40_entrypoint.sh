#!/usr/bin/env bash
# shellcheck shell=bash

test_entrypoint_rejects_missing_config() {
  local out rc=0
  out=$(docker run --rm "$IMAGE" 2>&1) || rc=$?
  assert_eq "$rc" 1 "missing config exit code"
  assert_contains "$out" "is not readable" "missing config message"
  pass "entrypoint rejects missing config"
}

test_entrypoint_rejects_broken_yaml() {
  local out rc=0
  reset_config
  printf 'targets: [\n' | write_config
  out=$(docker run --rm -v "$CONFIG_HOST:/config:ro" "$IMAGE" 2>&1) || rc=$?
  assert_eq "$rc" 1 "broken yaml exit code"
  assert_contains "$out" "config.yaml" "broken yaml message names the file"
  pass "entrypoint rejects broken yaml"
}

test_entrypoint_cron_fires() {
  local data bk name
  data=$(new_volume cron_data)
  bk=$(new_volume cron_bk)
  name=dbbtest_cron_$$
  sqlite_data "$data"
  reset_config
  write_config <<'CONFIG'
targets:
  app:
    driver: sqlite
    paths:
      app: /data/app.db
    schedule: "*/5 * * * * * *"
    ping_url: http://mockping:8080/t_cron
CONFIG
  docker run -d --name "$name" --network "$NETWORK" -v "$bk:/backups" -v "$data:/data" \
    -v "$CONFIG_HOST:/config:ro" "$IMAGE" > /dev/null
  if ! wait_for 25 'ping_seen /t_cron'; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "supercronic never ran db-backup-run"
  fi
  docker rm -fv "$name" > /dev/null
  pass "entrypoint cron fires"
}
