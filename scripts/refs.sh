#!/usr/bin/env bash
# refs.sh: clone the reference implementations read-only into third_party/refs.
# These are never vendored or copied; they exist so the build loop in
# docs/research/implementation_plan.md section 8 can read them locally.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/third_party/refs"
mkdir -p "$DEST"
REPOS=(
  micromdm/nanomdm
  micromdm/nanohub
  micromdm/micromdm
  micromdm/nanodep
  micromdm/nanoaxm
  micromdm/nanocmd
  micromdm/scep
  micromdm/plist
  micromdm/mdmutil
  jessepeterson/kmfddm
  jessepeterson/mdmb
  jessepeterson/admgen
  jessepeterson/mdmcommands
  jessepeterson/cfgprofiles
  korylprince/go-adm
  korylprince/dep-webview-oidc
  fleetdm/fleet
  zentralopensource/zentral
  brandonweeks/nanoca
  smallstep/scep
  smallstep/pkcs7
  hslatman/ios-acme-simulator
  vbnin/Apple-JSON-discovery-server
  macadmins/contour
)
for repo in "${REPOS[@]}"; do
  name="${repo##*/}"
  if [ -d "$DEST/$name/.git" ]; then
    echo "updating $repo"; git -C "$DEST/$name" fetch -q --depth=1 origin && git -C "$DEST/$name" reset -q --hard FETCH_HEAD
  else
    echo "cloning $repo"; git clone -q --depth=1 "https://github.com/$repo" "$DEST/$name"
  fi
  printf '%s %s\n' "$repo" "$(git -C "$DEST/$name" rev-parse HEAD)"
done > "$DEST/COMMITS.txt"
cat "$DEST/COMMITS.txt"
