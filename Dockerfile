FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/db-backup ./cmd/db-backup \
    && CGO_ENABLED=0 GOBIN=/out go install github.com/aptible/supercronic@v0.2.49

FROM debian:trixie-slim AS mariadb-dump

RUN apt-get update \
    && apt-get install -y --no-install-recommends mariadb-client \
    && rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc -o /usr/share/keyrings/pgdg.asc \
    && echo "deb [signed-by=/usr/share/keyrings/pgdg.asc] https://apt.postgresql.org/pub/repos/apt trixie-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends mariadb-client-core postgresql-client-16 procps sqlite3 tzdata \
    && apt-get purge -y --auto-remove curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=mariadb-dump /usr/bin/mariadb-dump /usr/bin/mariadb-dump
COPY --from=build /out/supercronic /usr/local/bin/supercronic
COPY --from=build /out/db-backup /usr/local/bin/db-backup
RUN ln /usr/local/bin/db-backup /usr/local/bin/db-backup-run \
    && ln /usr/local/bin/db-backup /usr/local/bin/entrypoint

COPY LICENSE.upstream NOTICE /usr/share/doc/db-backup/

VOLUME /backups

HEALTHCHECK --interval=5m --timeout=3s CMD ["/usr/local/bin/db-backup", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/entrypoint"]
