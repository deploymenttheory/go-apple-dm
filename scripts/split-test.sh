#!/usr/bin/env bash
# Disposable container integration fixture. Both roles share one database.
# Invoked by testdb.sh; all credentials are generated test material.
set -euo pipefail

state="${TEST_SPLIT_ENV_FILE:-/tmp/go-apple-dm-split.env}"
action="${1:-}"

load_name() {
  if [ -z "${TEST_SPLIT_NAME:-}" ] && [ -f "$state" ]; then
    TEST_SPLIT_NAME="$(sed -n 's/^export TEST_SPLIT_NAME=//p' "$state")"
  fi
  case "${TEST_SPLIT_NAME:-}" in
    dm-test-split-*) ;;
    *) echo "No split fixture selected; set TEST_SPLIT_ENV_FILE or TEST_SPLIT_NAME" >&2; exit 2 ;;
  esac
}

cleanup() {
  for role in mdm ddm postgres; do
    docker rm -fv "${TEST_SPLIT_NAME}-${role}" >/dev/null 2>&1 || true
  done
  docker volume rm "${TEST_SPLIT_NAME}-data" >/dev/null 2>&1 || true
  docker network rm "$TEST_SPLIT_NAME" >/dev/null 2>&1 || true
}

case "$action" in
  env) cat "$state"; exit ;;
  down) load_name; cleanup; exit ;;
  logs)
    load_name
    for role in mdm ddm postgres; do docker logs "${TEST_SPLIT_NAME}-${role}" 2>&1 || true; done
    exit ;;
  up) ;;
  *) echo "usage: split-test.sh up|down|env|logs" >&2; exit 2 ;;
esac

backend="${E2E_STORE:-sqlite}"
case "$backend" in sqlite|postgres) ;; *) echo "split fixture needs sqlite or postgres" >&2; exit 2 ;; esac
fixture="$(mktemp -d "${TMPDIR:-/tmp}/dm-split.XXXXXX")"
TEST_SPLIT_NAME="dm-test-split-${backend}-$(openssl rand -hex 6)"
image="${TEST_SPLIT_IMAGE:-go-apple-dm:test}"
umask 077
mkdir "$fixture/mount"
chmod 755 "$fixture/mount"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj /CN=Split-Test-CA \
  -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign \
  -keyout "$fixture/ca.key" -out "$fixture/mount/ca.crt" >/dev/null 2>&1
openssl req -new -newkey rsa:2048 -nodes -subj /CN=localhost \
  -keyout "$fixture/mount/server.key" -out "$fixture/server.csr" >/dev/null 2>&1
cat > "$fixture/server.ext" <<'EOF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,DNS:ddm,DNS:mdm,IP:127.0.0.1
EOF
openssl x509 -req -in "$fixture/server.csr" -CA "$fixture/mount/ca.crt" \
  -CAkey "$fixture/ca.key" -CAcreateserial -days 2 -extfile "$fixture/server.ext" \
  -out "$fixture/mount/server.crt" >/dev/null 2>&1
# Only the TLS key is mounted; the issuer key stays in the private host directory.
chmod 644 "$fixture/mount/ca.crt" "$fixture/mount/server.crt" "$fixture/mount/server.key"
send="$(openssl rand -hex 32)"
recv="$(openssl rand -hex 32)"
token="$(openssl rand -hex 32)"
storage_key="$(openssl rand -hex 32)"
docker build -t "$image" "$(cd "$(dirname "$0")/.." && pwd)" >&2
trap 'cleanup' ERR
docker network create "$TEST_SPLIT_NAME" >/dev/null

wait_healthy() {
  local container="$1"
  for _ in $(seq 1 90); do
    if [ "$(docker inspect -f '{{.State.Health.Status}}' "$container")" = healthy ]; then return; fi
    if [ "$(docker inspect -f '{{.State.Running}}' "$container")" != true ]; then break; fi
    sleep 1
  done
  docker logs "$container" >&2
  echo "$container did not become healthy" >&2
  return 1
}

storage_args=()
if [ "$backend" = sqlite ]; then
  docker volume create "${TEST_SPLIT_NAME}-data" >/dev/null
  storage_args=(-v "${TEST_SPLIT_NAME}-data:/data" -e DM_DSN=/data/shared.db)
else
  password="$(openssl rand -hex 24)"
  docker run -d --name "${TEST_SPLIT_NAME}-postgres" --network "$TEST_SPLIT_NAME" \
    --network-alias postgres -e POSTGRES_USER=dm -e "POSTGRES_PASSWORD=$password" -e POSTGRES_DB=dm \
    --health-cmd 'pg_isready -U dm' --health-interval 1s --health-timeout 3s --health-retries 60 \
    postgres:17 >/dev/null
  wait_healthy "${TEST_SPLIT_NAME}-postgres"
  storage_args=(-e "DM_DSN=postgres://dm:${password}@postgres:5432/dm?sslmode=disable")
fi

# These exact arguments are used for both roles, including the keyring and DB.
common=(--network "$TEST_SPLIT_NAME" -v "$fixture/mount:/test-tls:ro"
  -e DM_TLS_CERT_FILE=/test-tls/server.crt -e DM_TLS_KEY_FILE=/test-tls/server.key
  -e DM_CA_FILE=/test-tls/ca.crt -e DM_LISTEN=:8080 -e "DM_STORAGE=$backend"
  -e DM_STORAGE_KEYS=e2e -e "DM_STORAGE_KEY_E2E=$storage_key" -e "DM_ADMIN_TOKEN=$token")
docker run -d --name "${TEST_SPLIT_NAME}-ddm" --network-alias ddm \
  -p "127.0.0.1:${TEST_DDM_PORT:-0}:8080" "${common[@]}" "${storage_args[@]}" \
  -e DM_ROLE=ddm -e "DM_DDM_RECV_KEY=$send" -e "DM_DDM_SEND_KEY=$recv" "$image" >/dev/null
wait_healthy "${TEST_SPLIT_NAME}-ddm"
docker run -d --name "${TEST_SPLIT_NAME}-mdm" --network-alias mdm \
  -p "127.0.0.1:${TEST_MDM_PORT:-0}:8080" "${common[@]}" "${storage_args[@]}" \
  -e DM_ROLE=mdm -e DM_DDM_URL=https://ddm:8080/ddm -e DM_DDM_ROOT_CA_FILE=/test-tls/ca.crt \
  -e "DM_DDM_SEND_KEY=$send" -e "DM_DDM_RECV_KEY=$recv" "$image" >/dev/null
wait_healthy "${TEST_SPLIT_NAME}-mdm"
mdm_port="$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "${TEST_SPLIT_NAME}-mdm")"
ddm_port="$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "${TEST_SPLIT_NAME}-ddm")"
# %q preserves paths safely when the caller sources the generated shell file.
{
  printf 'export TEST_SPLIT_NAME=%q\n' "$TEST_SPLIT_NAME"
  printf 'export TEST_SPLIT_BACKEND=%q\n' "$backend"
  printf 'export TEST_MDM_URL=%q\n' "https://127.0.0.1:$mdm_port"
  printf 'export TEST_DDM_URL=%q\n' "https://127.0.0.1:$ddm_port"
  printf 'export TEST_DDM_CA_FILE=%q\n' "$fixture/mount/ca.crt"
  printf 'export TEST_SPLIT_CA_KEY_FILE=%q\n' "$fixture/ca.key"
  printf 'export TEST_DDM_SEND_KEY=%q\n' "$send"
  printf 'export TEST_DDM_RECV_KEY=%q\n' "$recv"
  printf 'export TEST_DDM_ADMIN_TOKEN=%q\n' "$token"
} > "$state"
cat "$state"
