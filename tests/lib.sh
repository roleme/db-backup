#!/usr/bin/env bash
# shellcheck shell=bash

IMAGE=${IMAGE:-db-backup:test}
PROJECT=dbbtest
NETWORK=${PROJECT}_default
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PASSED=0

compose() {
  docker compose -p "$PROJECT" -f "$HERE/compose.yaml" "$@"
}

stack_up() {
  compose up -d --wait > /dev/null
}

stack_down() {
  compose down -v > /dev/null 2>&1 || true
}

cleanup() {
  stack_down
  docker ps -aq --filter "name=dbbtest_" | xargs docker rm -f > /dev/null 2>&1 || true
  docker volume ls -q --filter "name=dbbtest_" | xargs docker volume rm -f > /dev/null 2>&1 || true
  rm -f "${TMPDIR:-/tmp}/dbbtest_pw_$$"
  rm -rf "${TMPDIR:-/tmp}/dbbtest_targets_$$" "${TMPDIR:-/tmp}/dbbtest_stub_$$"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

pass() {
  PASSED=$((PASSED + 1))
  printf 'ok   %s\n' "$*"
}

assert_eq() {
  [ "$1" = "$2" ] || fail "$3: expected '$2', got '$1'"
}

assert_contains() {
  case "$1" in
    *"$2"*) ;;
    *) fail "$3: '$2' not found in: $1" ;;
  esac
}

assert_not_contains() {
  case "$1" in
    *"$2"*) fail "$3: unexpected '$2' in: $1" ;;
    *) ;;
  esac
}

new_volume() {
  docker volume create "dbbtest_${1}_$$" > /dev/null
  printf 'dbbtest_%s_%s' "$1" "$$"
}

dbb() {
  local vol=$1 flags=()
  shift
  while [ "$1" != "--" ]; do
    flags+=("$1")
    shift
  done
  shift
  docker run --rm --network "$NETWORK" -v "$vol:/backups" --entrypoint db-backup ${flags[@]+"${flags[@]}"} "$IMAGE" "$@"
}

in_vol() {
  local vol=$1
  shift
  docker run --rm -v "$vol:/backups" --entrypoint bash "$IMAGE" -c "$@"
}

pings() {
  compose logs --no-log-prefix mockping 2>&1
}

ping_seen() {
  local out
  out=$(pings)
  grep -qx "PING GET $1" <<< "$out"
}

wait_for() {
  local secs=$1 cmd=$2
  for _ in $(seq 1 "$secs"); do
    if eval "$cmd"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

psql_seed() {
  compose exec -T postgres psql -U dbb -d "$1" -v ON_ERROR_STOP=1 -q
}

mysql_seed() {
  compose exec -T -e MYSQL_PWD=rootpw mysql mysql -uroot
}

TARGETS_HOST=${TMPDIR:-/tmp}/dbbtest_targets_$$

reset_targets() {
  mkdir -p "$TARGETS_HOST"
  rm -f "$TARGETS_HOST"/*.env
}

write_target() {
  cat > "$TARGETS_HOST/$1.env"
}

dbbr() {
  local vol=$1 flags=()
  shift
  while [ "$1" != "--" ]; do
    flags+=("$1")
    shift
  done
  shift
  docker run --rm --network "$NETWORK" -v "$vol:/backups" -v "$TARGETS_HOST:/config/targets.d:ro" \
    --entrypoint db-backup-run ${flags[@]+"${flags[@]}"} "$IMAGE" "$@"
}

ping_count() {
  local out
  out=$(pings)
  grep -cx "PING GET $1" <<< "$out" || true
}
