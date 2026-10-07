# shellcheck shell=bash

TARGET_KEYS=" DRIVER DB_HOST DB_PORT DB_USER DB_PASSWORD_ENV DB_PASSWORD_FILE DATABASES SQLITE_PATHS EXTRA_PATHS EXTRA_OPTS EXCLUDE_TABLE_DATA SCHEDULE VERIFY_SCHEDULE HC_PING_URL HC_PING_URL_ENV HC_VERIFY_PING_URL HC_VERIFY_PING_URL_ENV KEEP_MINS KEEP_DAYS KEEP_WEEKS KEEP_MONTHS GZIP_LEVEL TIMEOUT "

declare -A TARGET_VARS=()
CHILD_ENV=()

check_target_name() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || die "invalid target name '$1'"
}

target_file() {
  check_target_name "$1"
  printf '%s/%s.env' "${TARGETS_DIR:-/config/targets.d}" "$1"
}

list_targets() {
  local file name
  for file in "${TARGETS_DIR:-/config/targets.d}"/*.env; do
    [ -e "$file" ] || continue
    name=$(basename "$file" .env)
    check_target_name "$name"
    printf '%s\n' "$name"
  done
}

parse_target() {
  local name=$1 file line key value
  file=$(target_file "$name")
  [ -r "$file" ] || die "target $name: $file is not readable"
  TARGET_VARS=()
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      '' | '#'*) continue ;;
    esac
    [[ "$line" == *=* ]] || die "target $name: line without '=': $line"
    key=${line%%=*}
    value=${line#*=}
    [[ "$TARGET_KEYS" == *" $key "* ]] || die "target $name: unknown key $key"
    [ -z "${TARGET_VARS[$key]+x}" ] || die "target $name: duplicate key $key"
    if [[ "$value" =~ ^\"(.*)\"$ ]] || [[ "$value" =~ ^\'(.*)\'$ ]]; then
      value=${BASH_REMATCH[1]}
    fi
    TARGET_VARS[$key]=$value
  done < "$file"
}

build_child_env() {
  local key value ref
  CHILD_ENV=()
  for key in "${!TARGET_VARS[@]}"; do
    value=${TARGET_VARS[$key]}
    case "$key" in
      TIMEOUT) ;;
      *_ENV)
        ref=$value
        [[ "$ref" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "$key must name an environment variable"
        [ -n "${!ref:-}" ] || die "environment variable $ref (from $key) is not set"
        CHILD_ENV+=("${key%_ENV}=${!ref}")
        ;;
      *) CHILD_ENV+=("$key=$value") ;;
    esac
  done
}

child_var() {
  local pair
  for pair in "${CHILD_ENV[@]}"; do
    if [ "${pair%%=*}" = "$1" ]; then
      printf '%s' "${pair#*=}"
      return 0
    fi
  done
  return 0
}
