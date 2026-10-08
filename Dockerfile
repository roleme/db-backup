FROM golang:1.26 AS supercronic

RUN CGO_ENABLED=0 GOBIN=/out go install github.com/aptible/supercronic@v0.2.49

FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/db-backup ./cmd/db-backup \
    && ln /out/db-backup /out/db-backup-run \
    && ln /out/db-backup /out/entrypoint

FROM debian:trixie-slim AS mariadb-dump

RUN apt-get update \
    && apt-get install -y --no-install-recommends mariadb-client \
    && rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim AS pg-client

COPY pgdg.asc /usr/share/keyrings/pgdg.asc
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && echo "deb [signed-by=/usr/share/keyrings/pgdg.asc] https://apt.postgresql.org/pub/repos/apt trixie-pgdg main" > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends postgresql-client-16 \
    && rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libpq5 liblz4-1 libreadline8t64 libzstd1 mariadb-client-core procps sqlite3 tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=pg-client /usr/lib/postgresql/16 /usr/lib/postgresql/16
RUN ln -s /usr/lib/postgresql/16/bin/psql /usr/local/bin/psql

COPY --from=mariadb-dump /usr/bin/mariadb-dump /usr/bin/mariadb-dump
COPY --from=supercronic /out/supercronic /usr/local/bin/supercronic
COPY --from=build /out/ /usr/local/bin/

COPY LICENSE.upstream NOTICE /usr/share/doc/db-backup/

VOLUME /backups

HEALTHCHECK --interval=5m --timeout=3s CMD ["/usr/local/bin/db-backup", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/entrypoint"]
