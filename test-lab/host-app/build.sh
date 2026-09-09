#!/bin/bash
set -euo pipefail
umask 077
LAB_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LAB_APP_DIR="$LAB_ROOT/test-lab/local/app"
LAB_BUNDLE="$LAB_APP_DIR/DeviceWeavePushLab.app"
mkdir -p "$LAB_BUNDLE/Contents/MacOS" "$LAB_APP_DIR/module-cache"
cp "$LAB_ROOT/test-lab/host-app/Info.plist" "$LAB_BUNDLE/Contents/Info.plist"
xcrun swiftc -swift-version 5 -module-cache-path "$LAB_APP_DIR/module-cache" \
  -framework AppKit -framework UserNotifications -framework Security \
  "$LAB_ROOT/test-lab/host-app/main.swift" -o "$LAB_BUNDLE/Contents/MacOS/DeviceWeavePushLab"
if [[ "${1:-}" == "--unsigned" ]]; then
  echo "Compiled only. APNs registration requires provisioning and signing."
  exit 0
fi
: "${LAB_SIGN_IDENTITY:?Set LAB_SIGN_IDENTITY to your Apple Development signing identity}"
security cms -D -i "$LAB_APP_DIR/embedded.provisionprofile" > "$LAB_APP_DIR/provisioning.plist"
python3 - "$LAB_APP_DIR" <<'PY'
import datetime, pathlib, plistlib, sys
directory = pathlib.Path(sys.argv[1])
profile = plistlib.loads((directory / 'provisioning.plist').read_bytes())
if profile['ExpirationDate'] <= datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None):
    raise SystemExit('The provisioning profile has expired.')
entitlements = profile['Entitlements']
app_id = entitlements.get('com.apple.application-identifier', entitlements.get('application-identifier', ''))
if not app_id.endswith('.com.weaveplatform.deviceweave'):
    raise SystemExit('Profile does not authorize com.weaveplatform.deviceweave.')
if entitlements.get('com.apple.developer.aps-environment') not in ('development', 'production'):
    raise SystemExit('Profile lacks the macOS APNs entitlement.')
(directory / 'entitlements.plist').write_bytes(plistlib.dumps(entitlements))
PY
cp "$LAB_APP_DIR/embedded.provisionprofile" "$LAB_BUNDLE/Contents/embedded.provisionprofile"
codesign --force --sign "$LAB_SIGN_IDENTITY" --entitlements "$LAB_APP_DIR/entitlements.plist" "$LAB_BUNDLE"
codesign --verify --strict "$LAB_BUNDLE"
echo "Signed app ready. Run test-lab/host-app/run.sh from this repository."
