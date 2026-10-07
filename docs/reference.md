# db-backup reference

A container that dumps PostgreSQL, MySQL and SQLite databases on a schedule, keeps daily, weekly and monthly copies, restores the latest dump into a scratch database to prove it works, and pings a Healthchecks-style endpoint on success or failure.

Dumps are plain `pg_dump`, `mariadb-dump` and `sqlite3` output, so they restore with the stock tools and need nothing from this project.

## How it runs

One container can serve many databases ("central mode"), or one container can serve one database ("single-target mode").

Central mode reads one env file per target from `/config/targets.d` (`/config/targets.d/<name>.env`). When that directory holds no `*.env` files, the container runs in single-target mode, configured by plain environment variables with the same names (`DRIVER`, `DB_HOST`, `DB_USER`, `DB_PASSWORD` or `DB_PASSWORD_FILE`, `DATABASES`, `HC_PING_URL`, and so on). Single-target mode has no `*_ENV` indirection, no `TIMEOUT` and no lock; the sections below that mention them describe central mode.

```yaml
services:
  db-backup:
    image: ghcr.io/roleme/db-backup:latest
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    mem_limit: 128m
    environment:
      APP_DB_PASSWORD: ${APP_DB_PASSWORD}
      APP_PING_URL: ${APP_PING_URL}
      APP_VERIFY_PING_URL: ${APP_VERIFY_PING_URL}
    volumes:
      - ./targets.d:/config/targets.d:ro
      - /srv/db-backups:/backups
      - /srv/apps/notes/data:/src/notes
```

`/config/targets.d/app.env`:

```
DRIVER=postgres
DB_HOST=app-postgres
DB_USER=backup
DB_PASSWORD_ENV=APP_DB_PASSWORD
DATABASES=app
SCHEDULE=20 1 * * *
VERIFY_SCHEDULE=0 5 * * 0
HC_PING_URL_ENV=APP_PING_URL
HC_VERIFY_PING_URL_ENV=APP_VERIFY_PING_URL
```

`/config/targets.d/notes.env`:

```
DRIVER=sqlite
SQLITE_PATHS=/src/notes/notes.db
```

## Target keys

Target files are parsed, not executed. Blank lines and lines starting with `#` are ignored. Any other key is an error, so a typo cannot be silently ignored. There is deliberately no plain `DB_PASSWORD` key: a key ending in `_ENV` names an environment variable of the container, and that value is passed to the target's process under the key without `_ENV`. Each target runs in a clean environment holding only its own values.

| Key | Meaning |
|---|---|
| `DRIVER` | `postgres`, `mysql` or `sqlite` (required) |
| `DB_HOST`, `DB_PORT`, `DB_USER` | connection (postgres and mysql) |
| `DB_PASSWORD_ENV` / `DB_PASSWORD_FILE` | the variable that holds the password, or a file that holds it |
| `DATABASES` | comma-separated database names (postgres, mysql) |
| `SQLITE_PATHS` | comma-separated database files; the file name without its extension names the dump |
| `EXTRA_PATHS` | comma-separated directories archived as `<name>-<stamp>.tar.gz` beside the dumps |
| `EXTRA_OPTS` | extra flags for `pg_dump` / `mariadb-dump`; compression is `GZIP_LEVEL`, so no `-Z` |
| `EXCLUDE_TABLE_DATA` | comma-separated tables whose rows are skipped; their schema is kept |
| `SCHEDULE`, `VERIFY_SCHEDULE` | cron expressions (a seconds field is accepted); the defaults are `@daily` and no verification |
| `HC_PING_URL`, `HC_VERIFY_PING_URL` (or `*_ENV`) | pinged after a successful run; `/fail` is appended on failure or timeout |
| `KEEP_MINS`, `KEEP_DAYS`, `KEEP_WEEKS`, `KEEP_MONTHS` | retention per tier, defaults 1440, 7, 4, 6 |
| `GZIP_LEVEL` | 1 to 9, default 6 |
| `TIMEOUT` | seconds before a run is killed, default 3600 |

Dump names are shared by all targets in `/backups`, so they must be unique across targets. The container refuses to start when two targets would write the same dump name.

Target files are read once, when the container starts: it validates every target, builds the crontab and keeps its own copy of each file, so editing a file in a running container changes nothing until the container is restarted. A target that fails validation (unknown key, a key set both directly and as `*_ENV`, an invalid name or schedule, a schedule the scheduler rejects, an unset environment variable) is skipped: the reason is logged, its ping URL receives `/fail` when it can be read, and the container reports itself unhealthy while the other targets keep running. Two targets that would write the same dump name stop the container, and so does having no valid target at all. `BACKUP_ON_START=TRUE` runs every target once at start. Every target defaults to `@daily` and the runs start together at midnight, so give targets their own `SCHEDULE` to stagger them. Schedules are cron expressions of 5 to 7 fields, or an `@` shortcut.

## Reaching the databases

The container must be able to open a connection to each database server. In Docker the usual way is a network shared with the database container. Prefer one network per database, marked `internal`, with this container attached to all of them: each database then sees only this container, and nothing on those networks has outbound access. Use the database container's name as `DB_HOST`; service names such as `db` or `postgres` repeat between projects and collide on a shared network.

## Database users

Give the backup its own user per database.

- **PostgreSQL:** `CREATE ROLE backup LOGIN PASSWORD '...' CREATEDB IN ROLE pg_read_all_data;`. `CREATEDB` is for the scratch database that verification restores into, and the role needs `CONNECT` on the `postgres` database. `pg_read_all_data` does not cover large objects: a database that uses them fails the dump with `permission denied for large object`. Dumps are written with `--no-owner --no-privileges`, so they restore under any role; ownership becomes the restoring user's. Only trusted extensions can be restored by a non-superuser (for example `pgcrypto` and `uuid-ossp` are, `pg_stat_statements` is not): check `\dx` on the server before relying on verification.
- **MySQL:** grant the three statements below. Without `TRIGGER` or `SHOW_ROUTINE` the dump silently leaves triggers and routines out, and nothing detects that, because a user without those privileges cannot see them either. With the binary log on (the MySQL 8.4 default), restoring triggers and functions also needs `log_bin_trust_function_creators=1` on the server; verification strips `DEFINER` clauses, and each verification restore is written to the binary log. After rows were excluded, verification also checks the scratch database for child rows whose parent rows are gone.

  ```sql
  GRANT SELECT, SHOW VIEW, TRIGGER ON db.* TO 'backup'@'%';
  GRANT SHOW_ROUTINE ON *.* TO 'backup'@'%';
  GRANT ALL ON `dbb\_verify\_%`.* TO 'backup'@'%';
  ```
- **SQLite:** mount the application's data **directory** read-write. The dump uses `VACUUM INTO`, which reads one consistent snapshot even while the application writes, produces a compacted copy and needs the `-wal` and `-shm` files next to the database; a read-only or single-file mount cannot open a WAL database.

## Excluding table rows

`EXCLUDE_TABLE_DATA=table1,table2` keeps each table's schema and skips its rows, for tables such as execution logs or history. A restore brings those tables back empty. PostgreSQL uses `--exclude-table-data`; MySQL uses `--ignore-table-data=<db>.<table>`; for SQLite the rows are deleted in the dump copy with the table's triggers suspended, so no other table is altered. For SQLite and MySQL, verification then fails if the exclusion left child rows whose parent rows are gone.

## Output

```
/backups/last/<name>-<yyyymmdd-hhmmss>.<sql|db|tar>.gz
/backups/daily/<name>-<yyyymmdd>...   weekly/ (ISO week)   monthly/ (yyyymm)
/backups/<tier>/<name>-latest...      symlink to the newest file of the tier
/backups/last/<file>.tables           table count of that dump
```

The four tiers are hardlinks of one file, so keeping all of them costs one copy. Retention is counted from the stamp in each file name, not from file times: `last` drops files older than `KEEP_MINS` minutes, `daily` files stamped before today minus `KEEP_DAYS` days, `weekly` ISO weeks that started before today minus `KEEP_WEEKS` weeks, and `monthly` months that started before the first of this month minus `KEEP_MONTHS` months. A dump is written to a temporary file and moved into place only after it completed, so a failed or killed run never leaves a dump that looks valid; temporary files older than an hour are removed at the start of each backup. A dump that contains no tables is treated as a failure.

## Verification

`VERIFY_SCHEDULE` restores the newest dump into a scratch database (`dbb_verify_<name>`, or a temporary file for SQLite) and requires the same table count as the dump. A truncated, corrupt or unreplayable dump fails and pings `/fail`. This is a restore test, not just a checksum.

## Alerting

Each target pings its own endpoint, so the absence of a ping is the alert for a hung or stopped container as well as for a failed dump. The container health check only reports that the scheduler is alive; it cannot see a hung job. Pings never fail a dump.

## Restoring

```
gunzip -c app-latest.sql.gz | psql -d newdb           # PostgreSQL, as the user who should own the objects
gunzip -c shop-latest.sql.gz | mariadb newdb          # MySQL (strip DEFINER=`...`@`...` if the user lacks the privilege to set it)
gunzip -c notes-latest.db.gz > notes.db               # SQLite: put it in place with the application stopped
tar -xzf data-latest.tar.gz                           # extra paths
```

## Limits

A run is killed after `TIMEOUT` seconds and pings `/fail`. In central mode, runs of the same target (backup and verify) are serialised with a lock and different targets run concurrently. A restart interrupts any dump in progress; the next run of that target removes its own temporary files older than an hour.

## Security

- **It runs as root by default.** It has to read other applications' data files, and the dumps it writes are readable only by root (mode 0600). If every database file belongs to one user, run it as that user (`user: "1000:1000"`) and give that user write access to the backup directory and, for SQLite, to the data directories (a WAL database needs `-wal` and `-shm` files created next to it).
- **Hardening that works** (tested with a SQLite target: backup and verify both pass): a read-only root file system, a temporary `/tmp`, no new privileges, and only the file-access capabilities.

  ```yaml
  read_only: true
  tmpfs:
    - /tmp
  security_opt:
    - no-new-privileges:true
  cap_drop:
    - ALL
  cap_add:
    - DAC_OVERRIDE
    - DAC_READ_SEARCH
    - FOWNER
    - CHOWN
  ```

- **No ports are opened.** The container only makes outbound connections: to the databases, and to the ping URLs.
- **Secrets.** Passwords are given as the name of an environment variable (or a file), never inside a target file, and each target runs in a clean environment holding only its own values. A target file can still name any variable of the container, so treat the targets directory as trusted configuration. The dumps contain your data; protect the backup directory accordingly.
- **MySQL connections do not verify the server certificate**, because MySQL's default certificate is self-signed and carries no host name. Keep database traffic on a private network.
- **The image** is scanned with Trivy in CI, which fails on any HIGH or CRITICAL vulnerability that has a fix, and it is rebuilt from scratch every week so that fixed packages arrive. Operating-system vulnerabilities that Debian has not fixed yet are not mitigated here.

## Image

Debian with the PostgreSQL 16 client (`pg_dump` for PostgreSQL 16 servers; a server of another major version needs its client added to the `Dockerfile`), `mariadb-dump`, `sqlite3` and `supercronic`, plus the tool itself: one statically linked Go binary, installed as `db-backup`, `db-backup-run` and `entrypoint`. About 218 MB uncompressed; idle memory about 15 MiB; a 205 MB SQLite database backs up and compresses under a 64 MB limit.

## Development

`go test ./...` runs the unit tests (retention, configuration, schedules, process handling, adapters) without Docker. `tests/run.sh [filter]` builds the image and runs every `test_*` against real PostgreSQL, MySQL and SQLite containers with a mock ping endpoint. It needs Docker with Compose v2. The scheduler (`supercronic`) is built from source in the `Dockerfile` at a pinned version and verified by the Go checksum database; Renovate bumps that version and the builder image.

## Licence

MIT. Parts of the dump layout and retention logic are adapted from a project distributed under the MIT licence; see `LICENSE.upstream` and `NOTICE`.
