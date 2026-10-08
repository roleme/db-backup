# db-backup

Scheduled, restore-tested backups for PostgreSQL, MySQL and SQLite, in one small container.

- **Standard dumps.** Output is plain `pg_dump`, `mariadb-dump` and `sqlite3` output, so a dump restores with the stock tools and nothing from this project.
- **Retention.** Keeps `last`, `daily`, `weekly` and `monthly` copies as hardlinks of one file, so keeping all four costs one copy.
- **Restore test.** On a schedule it restores the newest dump into a scratch database and compares the table count. A dump that compresses fine but cannot be replayed is caught.
- **Alerting.** Pings a Healthchecks-style URL after each run, with `/fail` appended on failure. A ping error never fails a dump.
- **One container, many databases.** Each database is a small target file; targets run isolated from each other, with their own lock, timeout and ping.

## Run it

```yaml
services:
  db-backup:
    image: ghcr.io/roleme/db-backup:1.0.0
    restart: unless-stopped
    environment:
      APP_DB_PASSWORD: ${APP_DB_PASSWORD}
      APP_PING_URL: ${APP_PING_URL}
    volumes:
      - ./targets.d:/config/targets.d:ro
      - ./backups:/backups
```

`targets.d/app.env`, one file per database (the file name is the target name):

```
DRIVER=postgres
DB_HOST=app-postgres
DB_USER=backup
DB_PASSWORD_ENV=APP_DB_PASSWORD
DATABASES=app
SCHEDULE=20 1 * * *
VERIFY_SCHEDULE=0 5 * * 0
HC_PING_URL_ENV=APP_PING_URL
```

There is no plain password key: a key ending in `_ENV` names an environment variable of the container, and its value is passed to that target only. Ping URLs can be given the same way (`HC_PING_URL_ENV`) or directly (`HC_PING_URL`). Target files are parsed, not executed, and an unknown key is an error.

With no target files the container runs a single database configured from plain environment variables (`DRIVER`, `DB_HOST`, `DB_USER`, `DB_PASSWORD`, `DATABASES`, `HC_PING_URL`, ...). That mode has no `*_ENV` indirection, `TIMEOUT` or per-target lock.

## Versions

Images are tagged `X.Y.Z`, with `X.Y`, `X` and `latest` following the newest release. Pin `X.Y.Z`: a release tag is never moved. Releases are cut on demand, not on every merge.

## More

[docs/reference.md](docs/reference.md) has every key, the database privileges to grant, how to reach the databases, excluding table rows, the output layout, verification, restoring, and the security notes (it runs as root by default; a hardening recipe is there).

## Working on this repository

- `go test ./...` runs the Go unit tests; no Docker needed.
- `tests/run.sh [filter]` builds the image and runs every `test_*` function whose name contains the filter, against real PostgreSQL, MySQL and SQLite containers. It needs Docker with Compose v2.
- To release, run the `release` workflow from the Actions tab: choose patch, minor or major (the first release is 1.0.0), or give an explicit version. It builds, scans and tests the head of `main`, pushes the image, then creates the tag and the GitHub release.
- Write the failing test first. Every behaviour here has one.
- Lint with `gofmt`, `go vet`, `shellcheck --severity=warning` on the shell test files and `hadolint` on the `Dockerfile`.
- No explanatory comments in code; put the reasoning in the commit message.
- This repository is public and generic. Do not add names, hosts, paths or anything else belonging to one deployment. `tests/cases/90_public_hygiene.sh` checks for common leaks (home paths, e-mail addresses, private network addresses). To forbid names specific to your own deployment, put a regular expression in the `DBB_HYGIENE_EXTRA` environment variable when you run the tests, and keep that list outside the repository.

## Licence

MIT. Parts of the dump layout and retention logic are adapted from a project under the MIT licence; see `LICENSE.upstream` and `NOTICE`.
