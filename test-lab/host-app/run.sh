#!/bin/bash
set -euo pipefail
umask 077
LAB_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
exec "$LAB_ROOT/test-lab/local/app/DeviceWeavePushLab.app/Contents/MacOS/DeviceWeavePushLab" \
  --output "$LAB_ROOT/test-lab/local/app"
