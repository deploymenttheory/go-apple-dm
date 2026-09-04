#!/usr/bin/env bash
# testdb.sh: PostgreSQL and MySQL in Docker for the storage and e2e suites,
# and our own reference server in the ddm role for the split-deployment e2e.
#
# Usage: scripts/testdb.sh up|down|env|ddm-up|ddm-down|ddm-env
#   up        start (or reuse) both database containers, wait for readiness, print exports
#   down      remove both database containers
#   env       print the database export lines matching .github/workflows/go-test.yml
#   ddm-up    build go-apple-dm:test from this repository, run it as the ddm role, print exports
#   ddm-down  remove the ddm container
#   ddm-env   print the ddm export lines (TEST_DDM_URL and the shared keys)
set -euo pipefail

PG=mdm-test-postgres
MY=mdm-test-mysql
DDM=mdm-test-ddm
DDM_IMAGE=go-apple-dm:test
DDM_PORT="${TEST_DDM_PORT:-8090}"
# Shared secrets for the test hop between the roles; CI sets the same values.
DDM_SEND_KEY="${TEST_DDM_SEND_KEY:-mdm-to-ddm-test-key}"
DDM_RECV_KEY="${TEST_DDM_RECV_KEY:-ddm-to-mdm-test-key}"
DDM_ADMIN_TOKEN="${TEST_DDM_ADMIN_TOKEN:-admin-test-token}"
# The container writes a persistent sqlite store, which seals its secret columns
# and so needs a key. Test material, never a deployment value.
DDM_STORAGE_KEY_NAME="${TEST_DDM_STORAGE_KEY_NAME:-e2e}"
DDM_STORAGE_KEY="${TEST_DDM_STORAGE_KEY:-e2e-storage-key-of-sufficient-length}"

print_ddm_env() {
  echo "export TEST_DDM_URL='http://127.0.0.1:${DDM_PORT}'"
  echo "export TEST_DDM_SEND_KEY='${DDM_SEND_KEY}'"
  echo "export TEST_DDM_RECV_KEY='${DDM_RECV_KEY}'"
  echo "export TEST_DDM_ADMIN_TOKEN='${DDM_ADMIN_TOKEN}'"
}

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

# wait_for LABEL CMD...: poll CMD until it succeeds or 90s elapse. LABEL is
# the container name; when it stops running the wait ends early with its logs.
wait_for() {
  local label="$1"; shift
  for _ in $(seq 1 90); do
    if "$@" >/dev/null 2>&1; then echo "$label: ready" >&2; return 0; fi
    if [ "$(docker inspect -f '{{.State.Running}}' "$label" 2>/dev/null)" != "true" ]; then
      echo "$label: exited" >&2
      docker logs "$label" 2>&1 | tail -n 20 >&2
      return 1
    fi
    sleep 1
  done
  echo "$label: not ready after 90s" >&2
  docker logs "$label" 2>&1 | tail -n 20 >&2
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
  ddm-up)
    docker build -t "$DDM_IMAGE" "$(cd "$(dirname "$0")/.." && pwd)" >&2
    docker rm -f "$DDM" >/dev/null 2>&1 || true
    # The mdm role signs with SEND and verifies with RECV, so the ddm role receives
    # with the mdm role's SEND key and signs with its RECV key.
    docker run -d --name "$DDM" -p "${DDM_PORT}:8080" \
      -e MDM_ROLE=ddm -e MDM_LISTEN=:8080 -e MDM_STORAGE=sqlite -e MDM_DSN=/data/ddm.db \
      -e MDM_DDM_RECV_KEY="$DDM_SEND_KEY" -e MDM_DDM_SEND_KEY="$DDM_RECV_KEY" \
      -e MDM_STORAGE_KEYS="$DDM_STORAGE_KEY_NAME" \
      -e "MDM_STORAGE_KEY_$(printf '%s' "$DDM_STORAGE_KEY_NAME" | tr '[:lower:].-' '[:upper:]__')=$DDM_STORAGE_KEY" \
      -e MDM_ADMIN_TOKEN="$DDM_ADMIN_TOKEN" "$DDM_IMAGE" >/dev/null
    wait_for "$DDM" curl -fsS "http://127.0.0.1:${DDM_PORT}/healthz"
    print_ddm_env
    ;;
  ddm-down)
    docker rm -f "$DDM" >/dev/null 2>&1 || true
    ;;
  ddm-env)
    print_ddm_env
    ;;
  *)
    echo "usage: $0 up|down|env|ddm-up|ddm-down|ddm-env" >&2
    exit 2
    ;;
esac
