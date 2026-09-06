#!/usr/bin/env bash
# schema-update-body.sh: compose the pull request body for a schema update.
#
# Reads what the workflow already produced, so the body says what actually
# happened rather than what was expected to: the version table, the identifier
# counts, and the output of each check that ran. Separate from the workflow
# because a heredoc of this size inside YAML is unreadable and untestable.
#
# Inputs, all set by the workflow: PINNED, RELEASE, SHORT, NEWEST, ADDED,
# REMOVED, CONFORMANCE, VERIFY, HANDLED, and the files under /tmp.
set -euo pipefail

ok() { [ "${1:-1}" = "0" ] && echo "passed" || echo "**failed**"; }
block() { # block <file> <language>
  if [ -s "$1" ]; then printf '```%s\n' "${2:-}"; tail -40 "$1"; printf '```\n'; fi
}

cat <<EOF
Apple moved \`apple/device-management\` on the \`release\` branch, and this is the regenerated tree.

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
  echo "The newest version each OS family gains a schema in. This is what the update *is*, in the terms a deployment cares about."
  echo
  echo "| OS | was | now |"
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
  echo "Apple renamed or withdrew these. \`make verify\` fails until each has a line in \`schema/ALLOWED_REMOVALS.md\` saying why it may go, which is a deliberate decision and the reason this is not automatic:"
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
  echo "Where Apple stages the next OS. Reported only: a seed is not a release and must not become the pin."
  echo
  echo "| Branch | YAML files ahead |"
  echo "|---|---|"
  cat seeds.md
fi

cat <<'EOF'

---

Opened by the Device Management Schema Update workflow. Re-running it force-pushes this branch and updates this description.
EOF
