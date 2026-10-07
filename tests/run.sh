#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck disable=SC1091
. "$HERE/lib.sh"

seed_all() {
  compose exec -T postgres psql -U dbb -d postgres -v ON_ERROR_STOP=1 -q < "$HERE/seed/postgres.sql"
  mysql_seed < "$HERE/seed/mysql.sql"
}

filter=${1:-}
CURRENT_TEST=
trap cleanup EXIT
trap 'printf "FAIL: %s aborted at line %s\n" "${CURRENT_TEST:-setup}" "$LINENO" >&2' ERR
if [ "${SKIP_BUILD:-}" != 1 ]; then
  docker build -q -t "$IMAGE" "$HERE/.." > /dev/null
fi
stack_up
seed_all

for f in "$HERE"/cases/*.sh; do
  # shellcheck disable=SC1090
  . "$f"
done

for t in $(declare -F | awk '{print $3}' | grep '^test_' | sort); do
  case "$t" in
    *"$filter"*)
      CURRENT_TEST=$t
      "$t"
      ;;
    *) ;;
  esac
done

printf '%s passed\n' "$PASSED"
