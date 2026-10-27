# Apple Device Management Schema Updates

This workflow checks whether the generator on main can process upcoming Apple Device
Management Schema commits and produce valid Go packages. A confirmed failure opens
an incident explaining the schema change, generator limitation, relevant code and
required regression test. A passing Apple release is proposed as one adoption pull
request that moves the production pin and regenerates the library.

## Inputs and checks

Discovery records the project commit, the published schema pin and Apple's live
release and `seed*` branch heads. The published pin is the control. Candidate heads
already contained in that pin are excluded, as are seed heads already promoted to
the live release. Discovery uses Git ancestry and branch roles, without an OS version
list. Archived source branches are retained for reproduction but are not rediscovered
as upcoming changes.

The workflow preserves control and candidate commits under immutable
`schema-source/<ref>/<commit>` refs. Every assessment checks out its recorded project
and Apple commits in a temporary workspace. Generation retains the earlier major
releases recorded in the published provenance. A candidate whose schema version
starts a new major is assessed with the published pin as its newest retained release,
as its adoption would generate it.

Each assessment performs:

1. Strict schema decoding, type construction and Go generation with `schemagen`.
2. Regeneration verification against the newly generated output.
3. Compilation of `devicemanagement/schema/...`, including its handwritten helpers.

The temporary output starts without the production exported-identifier lock. Removing
an identifier from a candidate is not itself a generator failure. Normal `make verify`
continues to enforce the published API lock in ordinary CI.

This workflow does not test server behaviour, device support, feature inventories or
application installation. It changes the production pin only through a reviewed
[adoption pull request](#release-adoption). Published feature contracts run separately
through `scripts/device-management-schema-contracts.py` in ordinary CI.

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

## Release adoption

The adoption job proposes Apple's release head on the `schema/adopt-release` branch when
discovery is complete, the control passes and that release passes every assessment stage.
Seeds are never adopted. In an isolated clone of the project commit, the job checks out the
retained candidate snapshot in `third_party/apple-device-management/current`, then runs
`go generate ./...`, `schemagen generate` and `schemagen verify` with the production
exported-identifier lock. The title,
`feat(schema): adopt Apple device management schema release <version>`, takes its version from
the regenerated `os_versions` provenance.

Within the published major schema version, the retained releases beside `current` are
unchanged. When the candidate starts a new major, the job first retains the previous major's
final release: the newest commit on Apple's release branch before the candidate whose schema
version belongs to that major, normally the published pin. It is added as a fixed submodule
named for that version, such as `third_party/apple-device-management/27.4`, beside the existing
retained releases, and generation includes it. Schema versions come from `schemagen versions`,
not from OS release numbers. The job never removes a retained release.

The job then runs `TestSeedOS27CoverageInventory` with `-update-coverage`, which records the
pinned schema's macOS 27 boundaries in `test-lab/apple-features/macos27-coverage.json`.
Existing native test plans are kept, new sources receive a pending plan, and sources whose
boundaries no longer exist are removed. The loader's expected family counts are derived
from the checkout. An adoption pull request therefore needs no hand edits to pass CI; merge
it after CI passes. Replacing pending plans is separate native-acceptance work.

The pull request records the Apple, control, project and pushed head commits in a
`schema-adoption` marker. It includes Apple's release notes, changed files, added and removed
schema files, stage results and the macOS 27 boundaries it recorded.

Each run selects one action:

- Skip a commit that an adoption pull request already proposes, whether it is open, merged
  or closed. Closing an unmerged proposal declines that commit.
- Rebuild and update an open proposal when Apple's release moves, using a lease on the
  recorded head. A proposal carrying reviewer commits is not replaced; the newer release is
  proposed after it closes.
- Create a pull request when no proposal is open.
- Close an open proposal when the published pin already contains Apple's release.

A failed step, such as the removal of a locked exported identifier or a coverage contract that
recording cannot satisfy, opens a draft containing only the Apple checkout changes and that
step's error.
Draft state is set when the pull request is created. Dependency and network failures are monitoring failures and push nothing.

The job authenticates with the release-please GitHub App (`RP_APP_ID`,
`RP_APP_PRIVATE_KEY`), scoped to contents and pull-request writes. Pull requests opened with
the workflow token do not trigger CI.

## Running and reproducing

The workflow runs daily and supports manual dispatch. Dispatch defaults to report-only:
source snapshots, assessment artifacts and the proposed adoption are retained, but issue
publication and adoption pushes are disabled.
Its unit and integration tests also run on pull requests that change its scripts or workflow.

```sh
python3 .github/scripts/device_management_schema_update.py discover \
  --output /tmp/device-management-discovery.json
python3 .github/scripts/device_management_schema_update.py capture \
  --manifest /tmp/device-management-discovery.json \
  --output /tmp/device-management-capture.json
python3 .github/scripts/device_management_schema_update.py assess \
  --manifest /tmp/device-management-discovery.json --key ASSESSMENT_KEY \
  --output /tmp/device-management-assessments
python3 .github/scripts/device_management_schema_update.py publish \
  --manifest /tmp/device-management-discovery.json \
  --output /tmp/device-management-assessments --report-only
python3 .github/scripts/device_management_schema_update.py adopt \
  --manifest /tmp/device-management-discovery.json \
  --output /tmp/device-management-assessments --report-only
```

`capture` writes immutable Git refs and requires repository contents permission.
`assess` writes only temporary checkouts and local reports. `adopt` builds in a temporary
clone; without `--report-only` it pushes `schema/adopt-release` and writes pull requests. To reproduce an existing
assessment, download the `device-management-schema-discovery` artifact and use
its manifest and the assessment key recorded in the incident. Existing captured refs
make another capture unnecessary.

Artifacts are named `device-management-schema-discovery`,
`device-management-schema-assessment-<key>`,
`device-management-schema-summary` and `device-management-schema-adoption`.
They retain complete file inventories, diagnostics, per-stage logs, planned issue actions,
publication status and the adoption proposal for 30 days.

## Incident consolidation

The explicit `consolidate` action selects only legacy parse incidents for the reviewed
`--historical-commit` containing the single-object `examples` diagnostic. It retains the earliest issue as the historical
record, links duplicate file records, preserves notes and closes them as not planned.
The closure explains that historical parsing has not been repaired. It does not close
unrelated incidents or create future-schema incidents from historical observations.

```sh
python3 .github/scripts/device_management_schema_update.py consolidate \
  --historical-commit REVIEWED_HISTORICAL_SHA \
  --output /tmp/device-management-consolidation --report-only
```

Review `consolidation.json`; omit `--report-only` to apply those changes. The operation
is idempotent and uses the same paced, rate-limit-aware GitHub client.

## Sources and implementation

Apple publishes the [Apple Device Management Schema](https://github.com/apple/device-management)
and its [schema format](https://github.com/apple/device-management/blob/release/docs/schema.md).
The [workflow](../.github/workflows/device-management-schema-update.yml),
[update script](../.github/scripts/device_management_schema_update.py) and
[diagnostic classifier](../.github/scripts/device_management_schema_update_diagnostics.py)
implement the project's discovery, assessment and incident policy. The
[published contract runner](../scripts/device-management-schema-contracts.py)
verifies shipped features separately from candidate code generation.
