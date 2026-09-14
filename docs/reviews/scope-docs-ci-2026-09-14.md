# Scope, documentation and CI review — 14 September 2026

The [extension review](../research/extension-proposals-2026-09-14.md) retains P1–P18
and separates bounded future protocol candidates from current maintenance. No
proposed feature is implemented by this maintenance change. Current README,
architecture, package docs, decisions, event operations and CI guidance now describe
the existing SQL outbox, DDM queries and relevant scope boundaries. Broken links to
removed historical documents now point to their last retained Git revisions.
The pre-existing local edits to `docs/testing/enrollment-validation.md` are excluded.

## Repairs and local evidence

- Bench shutdown drains the control HTTP response before closing runtimes and waits
  for the serving goroutine. The restart test probes readiness and cancels/joins
  after failure before workspace removal. Its startup and drain deadlines remain
  bounded, with headroom for native Windows runners.
- Generated-output verification detects stale generated files without deleting them.
  Existing tests still cover changed/missing output and locked public identifiers.
  CI runs verification once; a missing generator now fails instead of reporting
  success. PostgreSQL E2E no longer repeats the SQLite-only embedded catalogue.
- Release previews skip server Markdown and manifest-only changes. Thirty-three
  local path-selection cases covered documentation, changelogs, mixed code/docs,
  dependencies, generator and workflow changes; dry runs confirmed both E2E package
  selections. Candidate/published installation, packaged binary checks, SQL suites,
  process acceptance and coverage requirements remain distinct.
- Standard Go dependency resolution/cache behavior is retained. The observed proxy
  transport failures do not justify a separate downloader or shared retry action.

Local checks passed: `actionlint`, `make verify` (55 Python tests, workflow security
and schema verification), root and server `golangci-lint`, and race-enabled suites
for bench, eventstore, application composition, DDM, certificate lifecycle,
schemagen and module layout. Targeted restart/startup-failure/process-exit and schema
verification cases also passed three repeated race-enabled runs. Changed Markdown
local links and `git diff --check` passed. SQL-service, full cross-platform and
process acceptance results are recorded by the pull request's CI checks.

## Diagram artifact and browser evidence

Both sources were delivered through the repository's Archify 2.17 reading profile.
Each passed all nine showcase artifact checks with zero composition errors and
warnings; neither required a geometry correction (`correction_rounds: 0`). The
service-layer architecture pins its code evidence to `80ea482`; the server workflow
reflects existing runtime/application shutdown code.

| Diagram type and output | Specification SHA-256 / bytes | Artifact SHA-256 / bytes |
|---|---|---|
| architecture: [service-layer.html](../diagrams/service-layer.html) | `2aca206a164cfce8da391bf1fe46b511b5126dd0766d00042521557669f2a296` / 10713 | `3bde1af1c584a3bf0688dd2b9b6469df58e88fdafda83fee123a3ebe5f157c73` / 747895 |
| workflow: [reference-server.html](../diagrams/reference-server.html) | `83acc4bf9710eda841cb43f74cbaddf0d3493ce6f9fb848c92da0ac131843bb2` / 5586 | `fb333c17b3223f6d26baa5a8d828e114c1c9f249ab33e258cab4b28f2433ce96` / 736420 |

For each exact artifact, upstream `visual-check` completed at 1440×900,
1600×1000, 1920×1080 and 2048×1320 and captured light/dark screenshots at both endpoint
sizes. Its result is **`browser_evidence: failed`** because it rejects vertical
page scrolling. All viewport measurements passed horizontal containment,
readability and viewer-control clearance. Under the repository's documented
[reading policy](../../scripts/diagrams/README.md), vertical page scrolling is
intentional and is preserved; the upstream result is not relabelled as a pass.

Supplemental checks against those same artifact hashes passed theme switching,
search/focus closure, guided views where present, all purpose-colour filters and
keyboard behavior. Four exports per diagram (SVG/PNG in each theme) passed canonical
content, colour and control-cleanliness checks. Endpoint screenshots and full-page
light-theme captures were visually inspected: labels, routes and explanatory cards
are readable and unclipped. **`visual_review: passed`** under the repository reading
policy. This perceptual assessment is separate from the upstream browser result.

Local receipts are in `/tmp/dm-service-layer-delivery.json`,
`/tmp/dm-reference-server-delivery.json`, `/tmp/dm-service-layer-browser.json`,
`/tmp/dm-reference-server-browser.json` and `/tmp/dm-scope-viewer-exports.json`.
Browser sidecars are ignored build evidence, not additional published diagrams.
