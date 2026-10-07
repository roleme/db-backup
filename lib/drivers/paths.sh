# shellcheck shell=bash

declare -A PATHS_DIRS=()

paths_validate() {
  local path name
  PATHS_DIRS=()
  while IFS= read -r path; do
    name=$(basename "$path")
    [ -z "${PATHS_DIRS[$name]:-}" ] || die "EXTRA_PATHS has two directories named $name"
    PATHS_DIRS[$name]=$path
  done < <(split_list "$EXTRA_PATHS")
}

paths_units() {
  local path
  while IFS= read -r path; do
    basename "$path"
  done < <(split_list "$EXTRA_PATHS")
}

paths_suffix() {
  printf '%s' '.tar.gz'
}

paths_dump() {
  local path=${PATHS_DIRS[$1]}
  if [ ! -d "$path" ]; then
    log "ERROR: EXTRA_PATHS entry $path is not a directory" >&2
    return 1
  fi
  tar -C "$(dirname "$path")" -cf - "$1" | gzip "-${GZIP_LEVEL}" > "$2"
}

paths_verify() {
  tar -tzf "$2" > /dev/null 2>&1
}
