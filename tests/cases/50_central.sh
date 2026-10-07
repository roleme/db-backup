#!/usr/bin/env bash
# shellcheck shell=bash

test_central_runs_each_target() {
  local data bk out
  data=$(new_volume cen_data)
  bk=$(new_volume cen_bk)
  sqlite_data "$data"
  reset_targets
  write_target alpha <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/app.db
HC_PING_URL=http://mockping:8080/t_cen_alpha
HC_VERIFY_PING_URL=http://mockping:8080/t_cen_alpha_v
TARGET
  write_target beta <<'TARGET'
DRIVER=postgres
DB_HOST=postgres
DB_USER=dbb
DB_PASSWORD_ENV=BETA_PASSWORD
DATABASES=app2
HC_PING_URL_ENV=BETA_PING
TARGET
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
  reset_targets
  write_target one <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/one.db
DB_PASSWORD_ENV=ONE_PW
TARGET
  write_target two <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/two.db
DB_PASSWORD_ENV=TWO_PW
TARGET
  docker run --rm -v "$bk:/backups" -v "$TARGETS_HOST:/config/targets.d:ro" -v "$stub:/usr/local/bin/db-backup:ro" \
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
  reset_targets
  write_target typo <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/app.db
SCHEDLE=@daily
TARGET
  write_target secret <<'TARGET'
DRIVER=postgres
DB_HOST=postgres
DB_USER=dbb
DB_PASSWORD=hunter2
DATABASES=app
TARGET
  write_target noenv <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/app.db
DB_PASSWORD_ENV=NOT_SET
TARGET

  rc=0
  out=$(dbbr "$bk" -- typo check 2>&1) || rc=$?
  assert_eq "$rc" 1 "typo exit code"
  assert_contains "$out" "unknown key SCHEDLE" "typo message"

  rc=0
  out=$(dbbr "$bk" -- secret check 2>&1) || rc=$?
  assert_eq "$rc" 1 "plain password exit code"
  assert_contains "$out" "unknown key DB_PASSWORD" "plain password message"

  rc=0
  out=$(dbbr "$bk" -- noenv check 2>&1) || rc=$?
  assert_eq "$rc" 1 "unset variable exit code"
  assert_contains "$out" "environment variable NOT_SET" "unset variable message"

  rc=0
  out=$(dbbr "$bk" -- 'bad/name' check 2>&1) || rc=$?
  assert_eq "$rc" 1 "bad name exit code"
  assert_contains "$out" "invalid target name" "bad name message"
  pass "central rejects bad targets"
}

test_central_duplicate_dump_names() {
  local data bk out rc=0
  data=$(new_volume dup_data)
  bk=$(new_volume dup_bk)
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'mkdir -p /data/a /data/b; sqlite3 /data/a/app.db "create table t (x)"; sqlite3 /data/b/app.db "create table t (x)"'
  reset_targets
  write_target first <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/a/app.db
TARGET
  write_target second <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/b/app.db
TARGET
  out=$(docker run --rm -v "$data:/data" -v "$bk:/backups" -v "$TARGETS_HOST:/config/targets.d:ro" "$IMAGE" 2>&1) || rc=$?
  assert_eq "$rc" 1 "duplicate dump names exit code"
  assert_contains "$out" "dump name app.db.gz is used by targets first and second" "duplicate message"
  pass "central duplicate dump names"
}

test_central_timeout_kills_and_alerts() {
  local bk rc=0 start elapsed
  bk=$(new_volume slow_bk)
  reset_targets
  write_target slow <<'TARGET'
DRIVER=postgres
DB_HOST=postgres
DB_USER=dbb
DB_PASSWORD_ENV=PG_PASSWORD
DATABASES=lockdb
TIMEOUT=3
HC_PING_URL=http://mockping:8080/t_cen_slow
TARGET
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
  reset_targets
  write_target a <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/a.db
SCHEDULE=0 3 * * *
VERIFY_SCHEDULE=30 4 * * 0
TARGET
  write_target b <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/b.db
TARGET
  docker run -d --name "$name" -v "$data:/data" -v "$bk:/backups" -v "$TARGETS_HOST:/config/targets.d:ro" "$IMAGE" > /dev/null
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
  reset_targets
  write_target only <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/app.db
HC_PING_URL=http://mockping:8080/t_cen_start
TARGET
  docker run -d --name "$name" --network "$NETWORK" -v "$data:/data" -v "$bk:/backups" \
    -v "$TARGETS_HOST:/config/targets.d:ro" -e BACKUP_ON_START=TRUE "$IMAGE" > /dev/null
  if ! wait_for 30 'ping_seen /t_cen_start'; then
    docker logs "$name" >&2
    docker rm -fv "$name" > /dev/null
    fail "no dump at container start in central mode"
  fi
  docker rm -fv "$name" > /dev/null
  pass "central backup on start"
}

test_central_bad_target_file_name_stops_start() {
  local data bk name rc
  data=$(new_volume badname_data)
  bk=$(new_volume badname_bk)
  name=dbbtest_badname_$$
  docker run --rm -v "$data:/data" --entrypoint bash "$IMAGE" -c 'sqlite3 /data/a.db "create table t (x)"; sqlite3 /data/z.db "create table t (x)"'
  reset_targets
  write_target a <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/a.db
TARGET
  write_target my.app <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/a.db
TARGET
  write_target z <<'TARGET'
DRIVER=sqlite
SQLITE_PATHS=/data/z.db
TARGET
  docker run -d --name "$name" -v "$data:/data" -v "$bk:/backups" -v "$TARGETS_HOST:/config/targets.d:ro" "$IMAGE" > /dev/null
  if ! wait_for 20 "[ \"\$(docker inspect -f '{{.State.Running}}' $name)\" = false ]"; then
    docker rm -fv "$name" > /dev/null
    fail "a badly named target file did not stop the container; later targets were silently dropped"
  fi
  rc=$(docker inspect -f '{{.State.ExitCode}}' "$name")
  assert_contains "$(docker logs "$name" 2>&1)" "invalid target name 'my.app'" "the error names the file"
  docker rm -fv "$name" > /dev/null
  assert_eq "$rc" 1 "start refused with a failure status"
  pass "central bad target file name stops start"
}
