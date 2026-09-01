#!/usr/bin/env bash
# fuzz.sh: run every Fuzz* target in the module for the given duration each.
set -euo pipefail
DURATION="${1:-20s}"
found=0
targets="$(go list -f '{{$d := .Dir}}{{$p := .ImportPath}}{{range .TestGoFiles}}{{$p}} {{$d}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$p}} {{$d}}/{{.}}{{"\n"}}{{end}}' ./... \
  | while read -r pkg file; do
      [ -f "$file" ] || continue
      awk -v pkg="$pkg" '/^func Fuzz[A-Za-z0-9_]+\(/ { name = $2; sub(/\(.*/, "", name); print pkg, name }' "$file"
    done | sort -u)"
while IFS= read -r line; do
  [ -z "$line" ] && continue
  pkg="${line%% *}"; fn="${line#* }"
  found=1
  echo "==> $pkg $fn ($DURATION)"
  go test -run '^$' -fuzz="^${fn}\$" -fuzztime="$DURATION" "$pkg"
done <<< "$targets"
if [ "$found" -eq 0 ]; then
  echo "no fuzz targets found"
fi
