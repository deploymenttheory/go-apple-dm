# Device Management Client Schema Code Generation Monitor

The monitor checks whether the generator on main can process upcoming Apple Device
Management Client Schemas and produce valid Go packages. A confirmed failure opens
an incident explaining the schema change, generator limitation, relevant code and
required regression test.

## Inputs and checks

Discovery records the project commit, the published schema pin and Apple's live
release and `seed*` branch heads. The published pin is the control. Candidate heads
already contained in that pin are excluded, as are seed heads already promoted to
the live release. Discovery uses Git ancestry and branch roles, without an OS version
list. Archived source branches are retained for reproduction but are not rediscovered
as upcoming changes.

The workflow preserves control and candidate commits under immutable
`schema-source/<ref>/<commit>` refs. Every assessment checks out its recorded project
and Apple commits in a temporary workspace. The production compatibility-history
input remains pinned to the generator's recorded provenance.

Each assessment performs:

1. Strict schema decoding, type construction and Go generation with `schemagen`.
2. Regeneration verification against the newly generated output.
3. Compilation of `devicemanagement/schema/...`, including its handwritten helpers.

The temporary output starts without the production exported-identifier lock. Removing
an identifier from a candidate is not itself a generator failure. Normal `make verify`
continues to enforce the published API lock in ordinary CI.

This workflow does not test server behaviour, device support, feature inventories or
application installation. It does not create schema-adoption PRs or alter production
pins. Published feature contracts run separately through
`scripts/device-management-schema-contracts.py` in ordinary CI.

## Diagnosis and incidents

Failed generation retains the exact diagnostic and, when possible, collects strict
parse failures across the candidate tree. The publisher groups observations by cause
across files and candidate snapshots before creating an incident. Incidents include:

- Immutable generator, control and candidate commits, plus reproduction commands.
- The failed phase and representative schema changes with source links.
- The relevant generator file and function.
- Evidence-based implementation guidance and a regression-test requirement.

Known unsupported fields, YAML shapes and scalar types receive specific guidance.
Malformed YAML is identified as a possible upstream-input problem. Unrecognised
failures retain their diagnostics and stage location, with the exact fix explicitly
left for investigation. These recommendations are not automatically verified patches.

A passing control is required before attributing failures to future schemas. A control
failure, missing checkout, missing artifact or dependency-download failure is reported
as monitoring failure. Candidate failures, control failures and publication failures
make the workflow fail; artifact uploads run regardless of assessment outcome.

Issue identities exclude file names, line numbers and workflow run URLs. An unchanged
assessment produces no issue writes. Engineer notes outside the managed evidence
section survive updates. A moved candidate retains earlier unresolved input commits;
closure requires all affected commits to pass. A newer passing tip or a retired branch
alone cannot verify a fix. To verify an older affected commit, replay its retained
manifest with the new project commit recorded explicitly and retain that new evidence.

Writes are serial and paced. Each publication performs at most 10 new issue creations
and 50 issue writes. Rate-limit responses honour retry headers with bounded backoff.
`publication.json` records completed and deferred actions; a later run reconciles
against GitHub again before resuming. No repeated creation is attempted after an
ambiguous transport failure within the same run.

## Running and reproducing

The workflow runs daily and supports manual dispatch. Dispatch defaults to report-only:
source snapshots and assessment artifacts are retained, but issue publication is disabled.
Monitor unit and integration checks also run on pull requests changing its scripts or workflow.

```sh
python3 .github/scripts/device_management_client_schema_monitor.py discover \
  --output /tmp/device-management-discovery.json
python3 .github/scripts/device_management_client_schema_monitor.py capture \
  --manifest /tmp/device-management-discovery.json \
  --output /tmp/device-management-capture.json
python3 .github/scripts/device_management_client_schema_monitor.py assess \
  --manifest /tmp/device-management-discovery.json --key ASSESSMENT_KEY \
  --output /tmp/device-management-assessments
python3 .github/scripts/device_management_client_schema_monitor.py publish \
  --manifest /tmp/device-management-discovery.json \
  --output /tmp/device-management-assessments --report-only
```

`capture` writes immutable Git refs and requires repository contents permission.
`assess` writes only temporary checkouts and local reports. To reproduce an existing
assessment, download the `device-management-client-schema-discovery` artifact and use
its manifest and the assessment key recorded in the incident. Existing captured refs
make another capture unnecessary.

Artifacts are named `device-management-client-schema-discovery`,
`device-management-client-schema-assessment-<key>` and
`device-management-client-schema-summary`. They retain complete file inventories,
diagnostics, per-stage logs, planned issue actions and publication status for 30 days.

## Historical incident consolidation

The explicit `consolidate` action selects only legacy parse incidents for the reviewed
`--historical-commit` containing the single-object `examples` diagnostic. It retains the earliest issue as the historical
record, links duplicate file records, preserves notes and closes them as not planned.
The closure explains that historical parsing has not been repaired. It does not close
unrelated incidents or create future-schema incidents from historical observations.

```sh
python3 .github/scripts/device_management_client_schema_monitor.py consolidate \
  --historical-commit REVIEWED_HISTORICAL_SHA \
  --output /tmp/device-management-consolidation --report-only
```

Review `consolidation.json`; omit `--report-only` to apply those changes. The operation
is idempotent and uses the same paced, rate-limit-aware GitHub client.
