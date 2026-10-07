# db-backup

Scheduled, restore-tested backups for PostgreSQL, MySQL and SQLite, in one small container.

- **Standard dumps.** Output is plain `pg_dump`, `mariadb-dump` and `sqlite3` output, so a dump restores with the stock tools and nothing from this project.
- **Retention.** Keeps `last`, `daily`, `weekly` and `monthly` copies as hardlinks of one file, so keeping all four costs one copy.
- **Restore test.** On a schedule it restores the newest dump into a scratch database and compares the table count. A dump that compresses fine but cannot be replayed is caught.
- **Alerting.** Pings a Healthchecks-style URL after each run, with `/fail` appended on failure. A ping error never fails a dump.

## Run it

```yaml
services:
  db-backup:
    image: ghcr.io/roleme/db-backup:latest
    restart: unless-stopped
    environment:
      DRIVER: postgres
      DB_HOST: postgres
      DB_USER: backup
      DB_PASSWORD: ${DB_PASSWORD}
      DATABASES: app
      SCHEDULE: "20 1 * * *"
      VERIFY_SCHEDULE: "0 5 * * 0"
      HC_PING_URL: ${HC_PING_URL}
    volumes:
      - ./backups:/backups
```

## Configuration

| Variable | Meaning |
|---|---|
| `DRIVER` | `postgres`, `mysql` or `sqlite` (required) |
| `DB_HOST`, `DB_PORT`, `DB_USER` | connection (postgres, mysql) |
| `DB_PASSWORD` or `DB_PASSWORD_FILE` | password, or a file that holds it |
| `DATABASES` | comma-separated database names (postgres, mysql) |
| `SQLITE_PATHS` | comma-separated database files; the file name without extension names the dump. Mount the application's data **directory** read-write, not the single file |
| `EXTRA_PATHS` | comma-separated directories archived as `.tar.gz` beside the dumps |
| `EXTRA_OPTS` | extra flags for `pg_dump` or `mariadb-dump` (no `-Z`, compression is `GZIP_LEVEL`) |
| `SCHEDULE`, `VERIFY_SCHEDULE` | cron expressions; default `@daily`, verification off unless set |
| `BACKUP_ON_START` | `TRUE` runs one dump when the container starts |
| `KEEP_MINS`, `KEEP_DAYS`, `KEEP_WEEKS`, `KEEP_MONTHS` | retention per tier, defaults 1440, 7, 4, 6 |
| `GZIP_LEVEL` | 1 to 9, default 6 |
| `HC_PING_URL`, `HC_VERIFY_PING_URL` | pinged on success; `/fail` is appended on failure |

The backup user needs `CREATEDB` on PostgreSQL, and `ALL` on `` `dbb\_verify\_%`.* `` on MySQL, for the scratch database that verification restores into.

## Output

```
/backups/last/<name>-<yyyymmdd-hhmmss>.<sql|db|tar>.gz
/backups/daily/   /backups/weekly/   /backups/monthly/      same names, one per period
/backups/<tier>/<name>-latest...                            symlink to the newest file
```

A dump is written to a temporary file and moved into place only when it is complete, so a failed or killed run never leaves something that looks valid. A dump with no tables counts as a failure.

## Restore

```
gunzip -c app-latest.sql.gz | psql -d newdb        # PostgreSQL
gunzip -c app-latest.sql.gz | mariadb newdb        # MySQL
gunzip -c app-latest.db.gz > app.db                # SQLite, with the application stopped
```

## Working on this repository

- `tests/run.sh [filter]` builds the image and runs every `test_*` function whose name contains the filter, against real PostgreSQL, MySQL and SQLite containers. It needs Docker with Compose v2.
- Write the failing test first. Every behaviour here has one.
- Lint with `shellcheck --severity=warning` on every `*.sh` file and `hadolint` on the `Dockerfile`.
- No explanatory comments in code; put the reasoning in the commit message.
- This repository is public and generic. Do not add names, hosts, paths or anything else belonging to one deployment. `tests/cases/90_public_hygiene.sh` checks for common leaks (home paths, e-mail addresses, private network addresses). To forbid names specific to your own deployment, put a regular expression in the `DBB_HYGIENE_EXTRA` environment variable when you run the tests, and keep that list outside the repository.

## Licence

MIT. Parts of the dump layout and retention logic are adapted from a project under the MIT licence; see `LICENSE.upstream` and `NOTICE`.
