# shellcheck shell=bash

store_dump() {
  local db=$1 suffix=$2 tmp=$3 expected=${4:-}
  local ts now day week month last daily weekly monthly
  ts=$(date +%s)
  now=$(date -d "@$ts" +%Y%m%d-%H%M%S)
  day=$(date -d "@$ts" +%Y%m%d)
  week=$(date -d "@$ts" +%G%V)
  month=$(date -d "@$ts" +%Y%m)
  last="${db}-${now}${suffix}"
  daily="${db}-${day}${suffix}"
  weekly="${db}-${week}${suffix}"
  monthly="${db}-${month}${suffix}"

  mkdir -p "$BACKUP_DIR/last" "$BACKUP_DIR/daily" "$BACKUP_DIR/weekly" "$BACKUP_DIR/monthly" || return 1
  mv -f "$tmp" "$BACKUP_DIR/last/$last" || return 1
  if [ -n "$expected" ]; then
    printf '%s\n' "$expected" > "$BACKUP_DIR/last/$last.tables" || return 1
  fi
  ln -f "$BACKUP_DIR/last/$last" "$BACKUP_DIR/daily/$daily" || return 1
  ln -f "$BACKUP_DIR/last/$last" "$BACKUP_DIR/weekly/$weekly" || return 1
  ln -f "$BACKUP_DIR/last/$last" "$BACKUP_DIR/monthly/$monthly" || return 1
  ln -sf "$last" "$BACKUP_DIR/last/${db}-latest${suffix}" || return 1
  ln -sf "$daily" "$BACKUP_DIR/daily/${db}-latest${suffix}" || return 1
  ln -sf "$weekly" "$BACKUP_DIR/weekly/${db}-latest${suffix}" || return 1
  ln -sf "$monthly" "$BACKUP_DIR/monthly/${db}-latest${suffix}" || return 1
}

prune() {
  local db=$1 suffix=$2 d8 d6 t6
  d8='[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]'
  d6='[0-9][0-9][0-9][0-9][0-9][0-9]'
  t6=$d6
  find "$BACKUP_DIR/last" -maxdepth 1 -mmin "+${KEEP_MINS}" \( -name "${db}-${d8}-${t6}${suffix}" -o -name "${db}-${d8}-${t6}${suffix}.tables" \) -exec rm -f {} +
  find "$BACKUP_DIR/daily" -maxdepth 1 -mtime "+${KEEP_DAYS}" -name "${db}-${d8}${suffix}" -exec rm -f {} +
  find "$BACKUP_DIR/weekly" -maxdepth 1 -mtime "+$((KEEP_WEEKS * 7 + 1))" -name "${db}-${d6}${suffix}" -exec rm -f {} +
  find "$BACKUP_DIR/monthly" -maxdepth 1 -mtime "+$((KEEP_MONTHS * 31 + 1))" -name "${db}-${d6}${suffix}" -exec rm -f {} +
}
