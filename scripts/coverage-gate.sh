#!/usr/bin/env bash
# coverage-gate.sh: merge Go coverage data directories and enforce a minimum.
#
# Usage: COVERAGE_MIN=95 scripts/coverage-gate.sh cover
#
# Expects cover/<layer>/ directories produced with -test.gocoverdir (unit is
# required; every other layer directory, e.g. storage, e2e-sqlite, and
# e2e-postgres, is merged when present). Fails when overall
# statement coverage or any non-exempt package is below COVERAGE_MIN.
# Exemptions are regular expressions in scripts/coverage-exempt.txt, one per
# line, matched against the package import path.
set -euo pipefail

COVER_DIR="${1:-cover}"
MIN="${COVERAGE_MIN:-95}"
EXEMPT_FILE="$(dirname "$0")/coverage-exempt.txt"
MODULE="$(go list -m)"

inputs=()
for dir in "$COVER_DIR"/*/; do
  dir="${dir%/}"
  if ls "$dir"/covmeta.* >/dev/null 2>&1; then
    inputs+=("$dir")
  fi
done
if [ ${#inputs[@]} -eq 0 ] || ! ls "$COVER_DIR/unit"/covmeta.* >/dev/null 2>&1; then
  echo "coverage-gate: no coverage data under $COVER_DIR (run make test first)" >&2
  exit 1
fi

merged="$COVER_DIR/merged.out"
go tool covdata textfmt -i="$(IFS=,; echo "${inputs[*]}")" -o "$merged"
go tool cover -html="$merged" -o "$COVER_DIR/merged.html"

# Per-package statement coverage from the text profile.
# Lines: file:startLine.startCol,endLine.endCol numStatements count
pkg_report="$COVER_DIR/packages.txt"
awk -v module="$MODULE" '
  NR == 1 { next }
  {
    split($1, a, ":"); file = a[1]
    n = split(file, parts, "/"); pkg = ""
    for (i = 1; i < n; i++) pkg = pkg (i > 1 ? "/" : "") parts[i]
    stmts = $2; count = $3
    total[pkg] += stmts
    if (count > 0) covered[pkg] += stmts
    gtotal += stmts
    if (count > 0) gcovered += stmts
  }
  END {
    for (p in total) printf "%s %.2f\n", p, (total[p] ? 100 * covered[p] / total[p] : 100)
    printf "TOTAL %.2f\n", (gtotal ? 100 * gcovered / gtotal : 0)
  }' "$merged" | sort > "$pkg_report"

# COVERAGE_EXEMPT_EXTRA adds patterns for one run, e.g. the database-backed
# packages when no server is available locally. CI never sets it.
exempt_patterns=()
for pat in ${COVERAGE_EXEMPT_EXTRA:-}; do
  exempt_patterns+=("$pat")
done
if [ -f "$EXEMPT_FILE" ]; then
  while IFS= read -r line; do
    line="${line%%#*}"; line="$(echo "$line" | xargs || true)"
    [ -n "$line" ] && exempt_patterns+=("$line")
  done < "$EXEMPT_FILE"
fi

is_exempt() {
  local pkg="$1"
  for pat in "${exempt_patterns[@]:-}"; do
    [ -z "$pat" ] && continue
    if [[ "$pkg" =~ $pat ]]; then return 0; fi
  done
  return 1
}

fail=0
printf "%-70s %8s\n" "package" "cover%"
while read -r pkg pct; do
  if [ "$pkg" = "TOTAL" ]; then continue; fi
  mark=""
  if is_exempt "$pkg"; then
    mark=" (exempt)"
  elif (( $(echo "$pct < $MIN" | bc -l) )); then
    mark=" FAIL"; fail=1
  fi
  printf "%-70s %8s%s\n" "$pkg" "$pct" "$mark"
done < "$pkg_report"

total="$(awk '$1 == "TOTAL" {print $2}' "$pkg_report")"
echo "overall: ${total}% (minimum ${MIN}%)"
if (( $(echo "$total < $MIN" | bc -l) )); then
  echo "coverage-gate: overall coverage below ${MIN}%" >&2; fail=1
fi

echo "least covered functions:"
go tool cover -func="$merged" | grep -v '^total:' | sort -k3 -n | awk 'NR <= 10' # awk drains stdin: no SIGPIPE under pipefail

if [ "$fail" -ne 0 ]; then
  echo "coverage-gate: FAILED" >&2
  exit 1
fi
echo "coverage-gate: OK"
