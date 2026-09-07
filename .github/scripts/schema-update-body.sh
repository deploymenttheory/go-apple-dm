#!/usr/bin/env bash
# schema-update-body.sh: compose the pull request body for a schema update.
#
# Summarizes generated versions, exported-name changes and check results.
# Kept separate from workflow YAML to simplify review of the Markdown template.
#
# Inputs, all set by the workflow: PINNED, RELEASE, SHORT, NEWEST, ADDED,
# REMOVED, CONFORMANCE, VERIFY, HANDLED, and the files under /tmp.
set -euo pipefail

ok() { [ "${1:-1}" = "0" ] && echo "passed" || echo "**failed**"; }
block() { # block <file> <language>
  if [ -s "$1" ]; then printf '```%s\n' "${2:-}"; tail -40 "$1"; printf '```\n'; fi
}

cat <<EOF
Regenerate against the updated \`apple/device-management\` release-branch commit.

| | |
|---|---|
| pin | \`${PINNED:0:12}\` → \`${RELEASE:0:12}\` |
| newest OS version | ${NEWEST:-unchanged} |
| exported identifiers | +${ADDED:-0} / -${REMOVED:-0} |
EOF

if [ -s versions.md ]; then
  echo
  echo "## Apple software versions"
  echo
  echo "Maximum schema version for each OS family before and after regeneration."
  echo
  echo "| OS | Previous | Updated |"
  echo "|---|---|---|"
  cat versions.md
fi

echo
echo "## Checks"
echo
echo "| Check | Result |"
echo "|---|---|"
echo "| Conformance (\`make test-conformance\`) | $(ok "${CONFORMANCE:-1}") |"
echo "| Removal guard (\`make verify\`) | $(ok "${VERIFY:-1}") |"
echo "| Every check-in message handled | $(ok "${HANDLED:-1}") |"
echo
echo "The full test suite, the coverage gate and the tier tests run on this pull request like any other."

if [ "${REMOVED:-0}" != "0" ]; then
  echo
  echo "## :warning: Identifiers are no longer generated"
  echo
  echo "These identifiers are absent from regenerated output. \`make verify\` fails until each has a line in \`schema/ALLOWED_REMOVALS.md\` documenting an intentional removal:"
  echo
  block /tmp/removed
fi

if [ "${HANDLED:-0}" != "0" ]; then
  echo
  echo "## :warning: A check-in message is not handled"
  echo
  echo "Apple defines a check-in the service does not dispatch, so a device sending it would get a 400. Implement it, or record why not in \`knownUnhandledCheckin\`:"
  echo
  block /tmp/handled.log
fi

if [ "${VERIFY:-0}" != "0" ]; then
  echo
  echo "<details><summary>Removal guard output</summary>"
  echo
  block /tmp/verify.log
  echo "</details>"
fi

if [ "${CONFORMANCE:-0}" != "0" ]; then
  echo
  echo "<details><summary>Conformance output</summary>"
  echo
  block /tmp/conformance.log
  echo "</details>"
fi

if [ -s /tmp/commits.log ]; then
  echo
  echo "<details><summary>Upstream commits</summary>"
  echo
  block /tmp/commits.log
  echo "</details>"
fi

if [ -s seeds.md ]; then
  echo
  echo "## Seed branches ahead of \`release\`"
  echo
  echo "Seed-branch changes are reported for review; this workflow keeps the pin on the release branch."
  echo
  echo "| Branch | YAML files ahead |"
  echo "|---|---|"
  cat seeds.md
fi

cat <<'EOF'

---

Opened by the Device Management Schema Update workflow. Re-running it force-pushes this branch and updates this description.
EOF
