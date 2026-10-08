#!/usr/bin/env bash
# shellcheck shell=bash

test_central_runs_each_target() {
  local data bk out
  data=$(new_volume cen_data)
  bk=$(new_volume cen_bk)
  sqlite_data "$data"
  reset_config
  write_config <<'CONFIG'
targets:
  alpha:
    driver: sqlite
    paths:
      app: /data/app.db
    ping_url: http://mockping:8080/t_cen_alpha
    verify_ping_url: http://mockping:8080/t_cen_alpha_v
  beta:
    driver: postgres
    host: postgres
    user: dbb
    password_env: BETA_PASSWORD
    databases: [app2]
    ping_url_env: BETA_PING
CONFIG
  local -a beta=(-e BETA_PASSWORD=pgpass -e BETA_PING=http://mockping:8080/t_cen_beta)
  dbbr "$bk" -v "$data:/data" -- alpha backup > /dev/null || fail "alpha backup failed"
  dbbr "$bk" "${beta[@]}" -- beta backup > /dev/null || fail "beta backup failed"
  out=$(in_vol "$bk" 'ls /backups/last')
  assert_contains "$out" "app-latest.db.gz" "alpha dump"
  assert_contains "$out" "app2-latest.sql.gz" "beta dump"
  ping_seen /t_cen_alpha || fail "alpha ping not sent"
  ping_seen /t_cen_beta || fail "beta ping not sent (resolved from the environment)"
  dbbr "$bk" -v "$data:/data" -- alpha verify > /dev/null || fail "alpha verify failed"
  ping_seen /t_cen_alpha_v || fail "alpha verify ping not sent"
  dbbr "$bk" "${beta[@]}" -- beta verify > /dev/null || fail "beta verify failed"
  pass "central runs each target"
}

test_central_child_env_is_isolated() {
  local bk stub out order
  bk=$(new_volume iso_bk)
  stub=${TMPDIR:-/tmp}/dbbtest_stub_$$
  cat > "$stub" <<'STUB'
#!/bin/sh
echo start >> /backups/order.txt
env | sort > "/backups/env.$$.txt"
sleep 2
echo end >> /backups/order.txt
STUB
  chmod +x "$stub"
  reset_config
  write_config <<'CONFIG'
targets:
  one:
    driver: sqlite
    paths: {one: /data/one.db}
    password_env: ONE_PW
  two:
    driver: sqlite
    paths: {two: /data/two.db}
    password_env: TWO_PW
CONFIG
  docker run --rm -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" -v "$stub:/usr/local/bin/db-backup:ro" \
    -e ONE_PW=secret-one -e TWO_PW=secret-two --entrypoint bash "$IMAGE" \
    -c 'db-backup-run one backup & db-backup-run one backup & wait'
  out=$(in_vol "$bk" 'cat /backups/env.*.txt')
  assert_contains "$out" "DB_PASSWORD=secret-one" "the target's own secret reaches its process"
  assert_not_contains "$out" "secret-two" "another target's secret does not"
  assert_not_contains "$out" "TWO_PW" "another target's variable name does not"
  assert_not_contains "$out" "ONE_PW" "the indirection variable is not passed on"
  order=$(in_vol "$bk" 'paste -sd" " /backups/order.txt')
  assert_eq "$order" "start end start end" "two runs of one target are serialised"
  pass "central child env is isolated"
}

test_central_rejects_bad_targets() {
  local bk out rc
  bk=$(new_volume bad_bk)
  reset_config
  write_config <<'CONFIG'
targets:
  typo:
    driver: sqlite
    paths: {app: /data/app.db}
    schedle: "@daily"
  secret:
    driver: postgres
    host: postgres
    user: dbb
    password: hunter2
    databases: [app]
  noenv:
    driver: sqlite
    paths: {app: /data/app.db}
    password_env: NOT_SET
CONFIG

  rc=0
  out=$(dbbr "$bk" -- typo check 2>&1) || rc=$?
  assert_eq "$rc" 1 "typo exit code"
  assert_contains "$out" "field schedle not found" "typo message"

  rc=0
  out=$(dbbr "$bk" -- secret check 2>&1) || rc=$?
  assert_eq "$rc" 1 "plain password exit code"
  assert_contains "$out" "field password not found" "plain password message"

  rc=0
  out=$(dbbr "$bk" -- noenv check 2>&1) || rc=$?
  assert_eq "$rc" 1 "unset variable exit code"
  assert_contains "$out" "environment variable NOT_SET" "unset variable message"

  rc=0
  out=$(dbbr "$bk" -- 'bad/name' check 2>&1) || rc=$?
  assert_eq "$rc" 1 "bad name exit code"
  assert_contains "$out" "invalid target name" "bad name message"

  rc=0
  out=$(dbbr "$bk" -- missing check 2>&1) || rc=$?
  assert_eq "$rc" 1 "undefined target exit code"
  assert_contains "$out" "target missing is not defined" "undefined target message"
  pass "central rejects bad targets"
}

test_central_duplicate_dump_names() {
  local data bk out rc=0
  data=$(new_volume dup_data)
  bk=$(new_volume dup_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'mkdir -p /data/a /data/b; sqlite3 /data/a/app.db "create table t (x)"; sqlite3 /data/b/app.db "create table t (x)"'
  reset_config
  write_config <<'CONFIG'
targets:
  first:
    driver: sqlite
    paths: {app: /data/a/app.db}
  second:
    driver: sqlite
    paths: {app: /data/b/app.db}
CONFIG
  out=$(docker run --rm -v "$data:/data" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" "$IMAGE" 2>&1) || rc=$?
  assert_eq "$rc" 1 "duplicate dump names exit code"
  assert_contains "$out" "dump name app.db.gz is used by targets first and second" "duplicate message"
  pass "central duplicate dump names"
}

test_central_timeout_kills_and_alerts() {
  local bk rc=0 start elapsed
  bk=$(new_volume slow_bk)
  reset_config
  write_config <<'CONFIG'
targets:
  slow:
    driver: postgres
    host: postgres
    user: dbb
    password_env: PG_PASSWORD
    databases: [lockdb]
    timeout: 3
    ping_url: http://mockping:8080/t_cen_slow
CONFIG
  compose exec -d -T postgres psql -U dbb -d lockdb -c "BEGIN; LOCK TABLE t IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(30);"
  sleep 2
  start=$(date +%s)
  dbbr "$bk" -e PG_PASSWORD=pgpass -- slow backup > /dev/null 2>&1 || rc=$?
  elapsed=$(($(date +%s) - start))
  compose exec -T postgres psql -U dbb -d postgres -Atc "select pg_terminate_backend(pid) from pg_stat_activity where datname = 'lockdb' and pid <> pg_backend_pid()" > /dev/null
  assert_eq "$rc" 124 "a timed-out run exits 124"
  [ "$elapsed" -lt 25 ] || fail "the run took ${elapsed}s; the 3 s timeout did not stop it"
  ping_seen /t_cen_slow/fail || fail "fail ping not sent after the timeout"
  pass "central timeout kills and alerts"
}

test_central_entrypoint_generates_crontab() {
  local data bk name tab
  data=$(new_volume crt_data)
  bk=$(new_volume crt_bk)
  name=dbbtest_crt_$$
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/a.db "create table t (x)"; sqlite3 /data/b.db "create table t (x)"'
  reset_config
  write_config <<'CONFIG'
targets:
  a:
    driver: sqlite
    paths: {a: /data/a.db}
    schedule: "0 3 * * *"
    verify_schedule: "30 4 * * 0"
  b:
    driver: sqlite
    paths: {b: /data/b.db}
CONFIG
  docker run -d --name "$name" -v "$data:/data" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" "$IMAGE" > /dev/null
  if ! wait_for 20 "docker exec $name pgrep -x supercronic > /dev/null 2>&1"; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "central mode did not reach supercronic"
  fi
  tab=$(docker exec "$name" cat /tmp/crontab)
  docker rm -fv "$name" > /dev/null
  assert_contains "$tab" "0 3 * * * db-backup-run a backup" "a: backup schedule"
  assert_contains "$tab" "30 4 * * 0 db-backup-run a verify" "a: verify schedule"
  assert_contains "$tab" "@daily db-backup-run b backup" "b: default schedule"
  pass "central entrypoint generates crontab"
}

test_central_backup_on_start() {
  local data bk name
  data=$(new_volume cst_data)
  bk=$(new_volume cst_bk)
  name=dbbtest_cst_$$
  sqlite_data "$data"
  reset_config
  write_config <<'CONFIG'
targets:
  only:
    driver: sqlite
    paths: {app: /data/app.db}
    ping_url: http://mockping:8080/t_cen_start
CONFIG
  docker run -d --name "$name" --network "$NETWORK" -v "$data:/data" -v "$bk:/backups" \
    -v "$CONFIG_HOST:/config:ro" -e BACKUP_ON_START=TRUE "$IMAGE" > /dev/null
  if ! wait_for 30 'ping_seen /t_cen_start'; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "no dump at container start"
  fi
  docker rm -fv "$name" > /dev/null
  pass "central backup on start"
}

test_central_bad_target_is_skipped_not_fatal() {
  local data bk name tab logs
  data=$(new_volume skip_data)
  bk=$(new_volume skip_bk)
  name=dbbtest_skip_$$
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/a.db "create table t (x)"; sqlite3 /data/z.db "create table t (x)"'
  reset_config
  write_config <<'CONFIG'
targets:
  a:
    driver: sqlite
    paths: {a: /data/a.db}
  my.app:
    driver: sqlite
    paths: {x: /data/a.db}
  z:
    driver: sqlite
    paths: {z: /data/z.db}
  broken:
    driver: sqlite
    paths: {y: /data/a.db}
    bogus: 1
    ping_url: http://mockping:8080/t_cen_broken
CONFIG
  docker run -d --name "$name" --network "$NETWORK" -v "$data:/data" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" "$IMAGE" > /dev/null
  if ! wait_for 20 "docker exec $name pgrep -x supercronic > /dev/null 2>&1"; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "bad targets stopped the good ones"
  fi
  tab=$(docker exec "$name" cat /tmp/crontab)
  logs=$(docker logs "$name" 2>&1)
  docker exec "$name" test -e /tmp/dbb-skipped || {
    docker rm -fv "$name" > /dev/null
    fail "no marker that targets were skipped"
  }
  docker rm -fv "$name" > /dev/null
  assert_contains "$tab" "db-backup-run a backup" "good target a is scheduled"
  assert_contains "$tab" "db-backup-run z backup" "good target z is scheduled"
  assert_not_contains "$tab" "my.app" "the badly named target is not scheduled"
  assert_not_contains "$tab" "broken" "the invalid target is not scheduled"
  assert_contains "$logs" "invalid target name 'my.app'" "the skipped name is reported"
  assert_contains "$logs" "field bogus not found" "the skipped target's error is reported"
  ping_seen /t_cen_broken/fail || fail "no fail ping for the skipped target"
  pass "central bad target is skipped not fatal"
}

test_central_no_valid_targets_stops_start() {
  local bk name rc out
  bk=$(new_volume none_bk)
  name=dbbtest_none_$$
  reset_config
  write_config <<'CONFIG'
targets:
  broken:
    driver: sqlite
    paths: {a: /data/a.db}
    bogus: 1
CONFIG
  docker run -d --name "$name" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" "$IMAGE" > /dev/null
  if ! wait_for 20 "[ \"\$(docker inspect -f '{{.State.Running}}' $name)\" = false ]"; then
    docker rm -fv "$name" > /dev/null
    fail "a container with no valid target kept running"
  fi
  rc=$(docker inspect -f '{{.State.ExitCode}}' "$name")
  out=$(docker logs "$name" 2>&1)
  docker rm -fv "$name" > /dev/null
  assert_eq "$rc" 1 "exit status"
  assert_contains "$out" "no valid targets" "message"
  pass "central no valid targets stops start"
}

test_central_rejects_bad_schedules() {
  local data bk rc out name
  data=$(new_volume sched_data)
  bk=$(new_volume sched_bk)
  sqlite_data "$data"
  reset_config
  write_config <<'CONFIG'
targets:
  short:
    driver: sqlite
    paths: {app: /data/app.db}
    schedule: "0 3 * *"
  inject:
    driver: sqlite
    paths: {app: /data/app.db}
    schedule: "* * * * * touch /backups/PWNED; echo"
  badverify:
    driver: sqlite
    paths: {app: /data/app.db}
    verify_schedule: "0 3 * * *; id"
  five:
    driver: sqlite
    paths: {app: /data/app.db}
    schedule: "0 3 * * *"
  seven:
    driver: sqlite
    paths: {app: /data/app.db}
    schedule: "*/5 * * * * * *"
  macro:
    driver: sqlite
    paths: {app: /data/app.db}
    schedule: "@daily"
CONFIG
  for name in short inject badverify; do
    rc=0
    out=$(dbbr "$bk" -v "$data:/data" -- "$name" check 2>&1) || rc=$?
    assert_eq "$rc" 1 "$name exit code"
    assert_contains "$out" "invalid schedule" "$name message"
  done
  for name in five seven macro; do
    dbbr "$bk" -v "$data:/data" -- "$name" check > /dev/null || fail "a valid schedule was refused: $name"
  done
  pass "central rejects bad schedules"
}

test_central_rejects_ambiguous_fields() {
  local bk out rc
  bk=$(new_volume amb_bk)
  reset_config
  write_config <<'CONFIG'
targets:
  both:
    driver: sqlite
    paths: {app: /data/app.db}
    ping_url: http://mockping:8080/x
    ping_url_env: SOME_URL
  pw:
    driver: postgres
    host: postgres
    user: dbb
    password_env: PW
    password_file: /run/pw
    databases: [app]
CONFIG
  rc=0
  out=$(dbbr "$bk" -- both check 2>&1) || rc=$?
  assert_eq "$rc" 1 "both ping fields exit code"
  assert_contains "$out" "set only one of ping_url and ping_url_env" "both ping fields message"
  rc=0
  out=$(dbbr "$bk" -- pw check 2>&1) || rc=$?
  assert_eq "$rc" 1 "both password fields exit code"
  assert_contains "$out" "set only one of password_env and password_file" "both password fields message"
  pass "central rejects ambiguous fields"
}

test_central_edit_after_start_is_ignored_until_restart() {
  local data bk name before
  data=$(new_volume snap_data)
  bk=$(new_volume snap_bk)
  name=dbbtest_snap_$$
  sqlite_data "$data"
  reset_config
  write_config <<'CONFIG'
targets:
  live:
    driver: sqlite
    paths: {app: /data/app.db}
    schedule: "*/3 * * * * * *"
    ping_url: http://mockping:8080/t_cen_snap
CONFIG
  docker run -d --name "$name" --network "$NETWORK" -v "$data:/data" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" "$IMAGE" > /dev/null
  if ! wait_for 25 'ping_seen /t_cen_snap'; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "the scheduled run never pinged"
  fi
  printf '    bogus: 1\n' >> "$CONFIG_HOST/config.yaml"
  before=$(ping_count /t_cen_snap)
  if ! wait_for 25 "[ \"\$(ping_count /t_cen_snap)\" -gt \"$before\" ]"; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "runs stopped after the config file was edited"
  fi
  docker rm -fv "$name" > /dev/null
  ! ping_seen /t_cen_snap/fail || fail "a fail ping was sent after the edit"
  pass "central edit after start is ignored until restart"
}

test_central_scheduler_rejected_schedule_is_skipped() {
  local data bk name tab
  data=$(new_volume badsched_data)
  bk=$(new_volume badsched_bk)
  name=dbbtest_badsched_$$
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/a.db "create table t (x)"; sqlite3 /data/b.db "create table t (x)"'
  reset_config
  write_config <<'CONFIG'
targets:
  a:
    driver: sqlite
    paths: {a: /data/a.db}
  b:
    driver: sqlite
    paths: {b: /data/b.db}
    schedule: "99 99 * * *"
CONFIG
  docker run -d --name "$name" -v "$data:/data" -v "$bk:/backups" -v "$CONFIG_HOST:/config:ro" "$IMAGE" > /dev/null
  if ! wait_for 20 "docker exec $name pgrep -x supercronic > /dev/null 2>&1"; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "a target the scheduler rejects stopped the container"
  fi
  tab=$(docker exec "$name" cat /tmp/crontab)
  docker exec "$name" test -e /tmp/dbb-skipped || {
    docker rm -fv "$name" > /dev/null
    fail "no marker that a target was skipped"
  }
  docker rm -fv "$name" > /dev/null
  assert_contains "$tab" "db-backup-run a backup" "the good target is scheduled"
  assert_not_contains "$tab" "db-backup-run b" "the rejected target is not scheduled"
  pass "central scheduler rejected schedule is skipped"
}
