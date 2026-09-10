#!/usr/bin/env bash
# Exercise the repository's actual Docker ignore rules using harmless sentinels.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
context="$(mktemp -d)"
trap 'rm -rf "$context"' EXIT
cp "$root/.dockerignore" "$context/.dockerignore"
mkdir -p "$context/test-lab/local/testdata" "$context/pki/example/testdata" "$context/.codex"
for path in .env.audit device.key identity.p12 identity.pfx client-private.pem state.db state.db-wal server.log test-lab/local/testdata/credential .codex/state pki/example/testdata/private.key; do
  echo 'harmless security test sentinel' > "$context/$path"
done
echo 'public build input' > "$context/source.go"
cat > "$context/Dockerfile" <<'DOCKERFILE'
FROM golang:1.27
COPY . /context
RUN test -f /context/source.go && test ! -e /context/.env.audit && test ! -e /context/device.key && test ! -e /context/identity.p12 && test ! -e /context/identity.pfx && test ! -e /context/client-private.pem && test ! -e /context/state.db && test ! -e /context/state.db-wal && test ! -e /context/server.log && test ! -e /context/test-lab/local/testdata/credential && test ! -e /context/.codex/state && test ! -e /context/pki/example/testdata/private.key
DOCKERFILE
docker build --no-cache "$context"
