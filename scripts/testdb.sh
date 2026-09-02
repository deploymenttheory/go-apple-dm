#!/usr/bin/env bash
# testdb.sh: PostgreSQL and MySQL in Docker for the storage and e2e suites.
#
# Usage: scripts/testdb.sh up|down|env
#   up    start (or reuse) both containers, wait for readiness, print exports
#   down  remove both containers
#   env   print the export lines matching .github/workflows/go-test.yml
set -euo pipefail

PG=mdm-test-postgres
MY=mdm-test-mysql

print_env() {
  echo "export TEST_POSTGRES_DSN='postgres://mdm:mdm@127.0.0.1:5432/mdm?sslmode=disable'"
  echo "export TEST_MYSQL_DSN='mdm:mdm@tcp(127.0.0.1:3306)/mdm?parseTime=true&multiStatements=true'"
}

# ensure NAME ARGS...: reuse a running container, start a stopped one, else create it.
ensure() {
  local name="$1"; shift
  local state
  state="$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || true)"
  case "$state" in
    true) echo "$name: already running" >&2 ;;
    false) echo "$name: starting" >&2; docker start "$name" >/dev/null ;;
    *) echo "$name: creating" >&2; docker run -d --name "$name" "$@" >/dev/null ;;
  esac
}

# wait_for LABEL CMD...: poll CMD until it succeeds or 90s elapse.
wait_for() {
  local label="$1"; shift
  for _ in $(seq 1 90); do
    if "$@" >/dev/null 2>&1; then echo "$label: ready" >&2; return 0; fi
    sleep 1
  done
  echo "$label: not ready after 90s" >&2
  return 1
}

case "${1:-}" in
  up)
    ensure "$PG" -e POSTGRES_USER=mdm -e POSTGRES_PASSWORD=mdm -e POSTGRES_DB=mdm \
      -p 5432:5432 postgres:17
    ensure "$MY" -e MYSQL_ROOT_PASSWORD=mdm -e MYSQL_DATABASE=mdm -e MYSQL_USER=mdm \
      -e MYSQL_PASSWORD=mdm -p 3306:3306 mysql:8.4
    wait_for "$PG" docker exec "$PG" pg_isready -U mdm
    wait_for "$MY" docker exec "$MY" mysqladmin ping -h 127.0.0.1 -uroot -pmdm --silent
    print_env
    ;;
  down)
    docker rm -f "$PG" "$MY" >/dev/null 2>&1 || true
    ;;
  env)
    print_env
    ;;
  *)
    echo "usage: $0 up|down|env" >&2
    exit 2
    ;;
esac
