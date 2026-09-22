#!/usr/bin/env bash
# guestweave.sh: fetch, build and verify the guestweave CLI that drives lab virtual Macs.
#
# Usage: guestweave.sh <checkout-dir> <repository-url> [ref]
#
# The default ref is the latest release tag. guestweave must be code-signed with the
# virtualization entitlements or every VM call fails, so the signature is verified here
# rather than at the first confusing runtime error. The repository is private: fetching it
# needs git credentials or an authenticated gh.
set -euo pipefail

dir="${1:?checkout directory}"
repo="${2:?repository URL}"
ref="${3:-}"

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  echo "guestweave: virtual Macs need an Apple silicon host" >&2
  exit 1
fi

if [ ! -d "$dir/.git" ]; then
  mkdir -p "$(dirname "$dir")"
  git clone --quiet "$repo" "$dir"
fi
git -C "$dir" fetch --quiet --tags origin

if [ -z "$ref" ]; then
  ref="$(git -C "$dir" tag --list 'v*' --sort=-v:refname | head -1)"
  [ -n "$ref" ] || ref="origin/main"
fi
git -C "$dir" checkout --quiet --detach "$ref"

make -C "$dir" build >/dev/null
binary="$dir/guestweave"
[ -x "$binary" ] || binary="$dir/weave"

entitlements="$(codesign -d --entitlements - --xml "$binary" 2>/dev/null || true)"
case "$entitlements" in
  *com.apple.security.virtualization*) ;;
  *)
    echo "guestweave: $binary lacks com.apple.security.virtualization; rebuild and re-sign" >&2
    exit 1
    ;;
esac

printf '%s\t%s\t%s\n' "$binary" "$ref" "$(git -C "$dir" rev-parse --short HEAD)"
