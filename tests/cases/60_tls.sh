#!/usr/bin/env bash
# shellcheck shell=bash

tls_dir() {
  printf '%s' "${TMPDIR:-/tmp}/dbbtest_tls_$$"
}

mysql_ca_file() {
  local dir
  dir=$(tls_dir)
  mkdir -p "$dir"
  compose exec -T mysql cat /var/lib/mysql/ca.pem > "$dir/mysql-ca.pem"
  chmod 644 "$dir/mysql-ca.pem"
  printf '%s' "$dir/mysql-ca.pem"
}

mysql_fingerprint() {
  compose exec -T mysql openssl x509 -in /var/lib/mysql/server-cert.pem -noout -fingerprint -sha256 | cut -d= -f2 | tr -d '\r'
}

test_mysql_tls_fingerprint_pins_the_server() {
  local bk out fp
  bk=$(new_volume mytlsfp_bk)
  fp=$(mysql_fingerprint)
  local -a env=(-e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=shop)
  dbb "$bk" "${env[@]}" -e "DB_SSL_FINGERPRINT=$fp" -- backup > /dev/null || fail "backup with the right fingerprint failed"
  dbb "$bk" "${env[@]}" -e "DB_SSL_FINGERPRINT=$fp" -- verify > /dev/null || fail "verify with the right fingerprint failed"
  if out=$(dbb "$bk" "${env[@]}" -e "DB_SSL_FINGERPRINT=00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF" -- backup 2>&1); then
    fail "backup accepted a server with the wrong fingerprint"
  fi
  assert_contains "$out" "Fingerprint validation" "wrong fingerprint message"
  pass "mysql tls fingerprint pins the server"
}

test_mysql_tls_ca_checks_the_host_name() {
  local bk out ca
  bk=$(new_volume mytlsca_bk)
  ca=$(mysql_ca_file)
  if out=$(dbb "$bk" -e DRIVER=mysql -e DB_HOST=mysql -e DB_USER=bkp -e DB_PASSWORD=bkppw -e DATABASES=shop -e DB_SSL_CA=/run/ca.pem -v "$ca:/run/ca.pem:ro" -- backup 2>&1); then
    fail "backup accepted a certificate that carries no matching host name"
  fi
  assert_contains "$out" "Hostname verification failed" "host name message"
  pass "mysql tls ca checks the host name"
}

test_postgres_tls_ca_refuses_a_server_without_tls() {
  local bk out ca
  bk=$(new_volume pgtlsno_bk)
  ca=$(mysql_ca_file)
  local -a env=(-e DRIVER=postgres -e DB_HOST=postgres -e DB_USER=dbb -e DB_PASSWORD=pgpass -e DATABASES=app)
  if out=$(dbb "$bk" "${env[@]}" -e DB_SSL_CA=/run/ca.pem -v "$ca:/run/ca.pem:ro" -- backup 2>&1); then
    fail "backup connected to a server without TLS although DB_SSL_CA is set"
  fi
  assert_contains "$out" "does not support SSL" "no TLS message"
  pass "postgres tls ca refuses a server without tls"
}

test_postgres_tls_ca_verifies_the_server_certificate() {
  local bk out dir name wrong
  bk=$(new_volume pgtls_bk)
  dir=$(tls_dir)
  mkdir -p "$dir/pg"
  name=dbbtest_pgtls_$$
  wrong=$(mysql_ca_file)
  docker run --rm -v "$dir/pg:/out" --entrypoint sh mysql:8.4 -c '
    cd /out
    openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -subj /CN=dbbtest-ca -days 2 2>/dev/null
    openssl req -newkey rsa:2048 -nodes -keyout server.key -out server.csr -subj /CN=pgtls 2>/dev/null
    printf "subjectAltName=DNS:pgtls" > ext.cnf
    openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.crt -days 2 -extfile ext.cnf 2>/dev/null
    chown 70:70 server.key server.crt
    chmod 600 server.key
    chmod 644 ca.crt' || fail "could not generate the test certificates"
  docker run -d --name "$name" --network "$NETWORK" --network-alias pgtls -v "$dir/pg:/certs:ro" \
    -e POSTGRES_USER=dbb -e POSTGRES_PASSWORD=pgpass -e POSTGRES_DB=app postgres:16-alpine \
    -c ssl=on -c ssl_cert_file=/certs/server.crt -c ssl_key_file=/certs/server.key > /dev/null
  wait_for 60 "docker exec $name pg_isready -h 127.0.0.1 -U dbb -d app > /dev/null 2>&1" || fail "the TLS postgres did not start"
  sleep 2
  docker exec "$name" psql -h 127.0.0.1 -U dbb -d app -c 'create table t (x int)' > /dev/null || fail "could not seed the TLS postgres"
  local -a env=(-e DRIVER=postgres -e DB_HOST=pgtls -e DB_USER=dbb -e DB_PASSWORD=pgpass -e DATABASES=app)
  dbb "$bk" "${env[@]}" -e DB_SSL_CA=/run/ca.crt -v "$dir/pg/ca.crt:/run/ca.crt:ro" -- backup > /dev/null || fail "backup with the right CA failed"
  dbb "$bk" "${env[@]}" -e DB_SSL_CA=/run/ca.crt -v "$dir/pg/ca.crt:/run/ca.crt:ro" -- verify > /dev/null || fail "verify with the right CA failed"
  if out=$(dbb "$bk" "${env[@]}" -e DB_SSL_CA=/run/ca.crt -v "$wrong:/run/ca.crt:ro" -- backup 2>&1); then
    fail "backup trusted a certificate that the given CA did not sign"
  fi
  assert_contains "$out" "certificate verify failed" "wrong CA message"
  docker rm -fv "$name" > /dev/null 2>&1 || true
  pass "postgres tls ca verifies the server certificate"
}
