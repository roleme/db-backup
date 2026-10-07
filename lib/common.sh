# shellcheck shell=bash

log() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

die() {
  printf '%s ERROR: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >&2
  exit 1
}

require_env() {
  local name
  for name in "$@"; do
    [ -n "${!name:-}" ] || die "$name is required"
  done
}

secret() {
  local name=$1 file_var="${1}_FILE"
  if [ -n "${!file_var:-}" ]; then
    [ -r "${!file_var}" ] || die "$file_var is not readable"
    tr -d '\r\n' < "${!file_var}"
  else
    printf '%s' "${!name:-}"
  fi
}

split_list() {
  local item
  while IFS= read -r item; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    if [ -n "$item" ]; then
      printf '%s\n' "$item"
    fi
  done <<< "${1//,/$'\n'}"
}

ping_url() {
  local base=$1 suffix=${2:-}
  [ -n "$base" ] || return 0
  curl -fsS -m 10 --retry 3 -o /dev/null "${base%/}${suffix}" 2> /dev/null || log "WARN: ping ${suffix:-success} failed"
  return 0
}

load_config() {
  require_env DRIVER
  case "$DRIVER" in
    postgres | mysql | sqlite) ;;
    *) die "DRIVER must be postgres, mysql or sqlite (got '$DRIVER')" ;;
  esac
  export BACKUP_DIR=${BACKUP_DIR:-/backups}
  export KEEP_MINS=${KEEP_MINS:-1440}
  export KEEP_DAYS=${KEEP_DAYS:-7}
  export KEEP_WEEKS=${KEEP_WEEKS:-4}
  export KEEP_MONTHS=${KEEP_MONTHS:-6}
  export GZIP_LEVEL=${GZIP_LEVEL:-6}
  export EXTRA_OPTS=${EXTRA_OPTS:-}
  export EXTRA_PATHS=${EXTRA_PATHS:-}
  export EXCLUDE_TABLE_DATA=${EXCLUDE_TABLE_DATA:-}
  export HC_PING_URL=${HC_PING_URL:-}
  export HC_VERIFY_PING_URL=${HC_VERIFY_PING_URL:-}
  local name
  for name in KEEP_DAYS KEEP_WEEKS KEEP_MONTHS; do
    [[ "${!name}" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer"
  done
  [[ "$KEEP_MINS" =~ ^[1-9][0-9]*$ ]] || die "KEEP_MINS must be a positive integer"
  [[ "$GZIP_LEVEL" =~ ^[1-9]$ ]] || die "GZIP_LEVEL must be between 1 and 9"
  [ -d "$BACKUP_DIR" ] && [ -w "$BACKUP_DIR" ] || die "BACKUP_DIR $BACKUP_DIR is not a writable directory"
}
