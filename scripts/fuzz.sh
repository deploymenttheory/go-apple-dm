#!/usr/bin/env bash
# fuzz.sh: run every Fuzz* target in the module for the given duration each.
set -euo pipefail
DURATION="${1:-20s}"
found=0
while IFS= read -r line; do
  pkg="${line%% *}"; fn="${line#* }"
  found=1
  echo "==> $pkg $fn ($DURATION)"
  go test -run '^$' -fuzz="^${fn}\$" -fuzztime="$DURATION" "$pkg"
done < <(go list -f '{{$p := .ImportPath}}{{range .TestGoFiles}}{{$p}} {{.}}{{"\n"}}{{end}}' ./... \
  | while read -r pkg file; do
      dir="$(go list -f '{{.Dir}}' "$pkg")"
      grep -hoE '^func (Fuzz[A-Za-z0-9_]+)\(' "$dir/$file" 2>/dev/null | sed -E "s/^func (Fuzz[A-Za-z0-9_]+)\(/$pkg \1/" || true
    done | sort -u)
if [ "$found" -eq 0 ]; then
  echo "no fuzz targets found"
fi
