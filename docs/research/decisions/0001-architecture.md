# 0001: Library-first architecture with a generated schema core

Status: accepted
Date: 2026-09-01
Phase: 0

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement>
- Doc: <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>
- YAML: `third_party/device-management/**` at commit `67045e2f` (see `schema/PROVENANCE.json`)

## References read

- `micromdm/nanomdm` `service/service.go`, `storage/*.go`, `mdm/checkin.go`, `mdm/command.go`, `mdm/mdm.go`
- `jessepeterson/kmfddm` `storage/*.go`, `ddm/*.go`
- `fleetdm/fleet` `server/mdm/nanomdm/README.md`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `deploymenttheory/go-sdk-appleservices` `device_management/**` (survey, see plan)
- Full survey: `docs/research/reference_projects.md`

## Known pitfalls found

- NanoMDM #260: `ClearQueue` becomes very slow on Postgres (no status index, row-by-row).
- NanoMDM #86: bootstrap tokens are not migrated between backends.
- NanoMDM #73: no tolerance for PKCS7 signing time skew during enrollment.
- NanoMDM #71: bootstrap and unlock tokens are not cleared on re-enrollment.
- KMFDDM #41: status report data is not cleared on un-enrollment or re-enrollment.
- KMFDDM #6, #5, #2, #11, #96: no pagination, no last-seen, no status webhook, notifier does not scale, no Postgres.
- go-sdk-appleservices: emit-only, no decoder, no check-in or status types, regenerated weekly with possible renames on v0.x.

## What they do

- **NanoMDM**: hides `context.Context` inside `mdm.Request`; check-in service returns raw `[]byte` for DDM; a single webhook service is the only integration point; storage is one `AllStorage` union; DDM is proxied over HTTP to a separate process.
- **KMFDDM**: separate binary; hashes declaration JSON as stored (not canonical); file, diskv, inmem, MySQL backends; Python helper scripts for set management.
- **Fleet**: vendors NanoMDM and NanoDEP as forks; DDM handled by cron reconcilers; one unconditional activation per configuration; status subscription values discarded.

## What we do better

1. Context-first service interfaces with typed errors and a hook chain; every state change is a typed event on an in-process bus, so webhooks, audit, metrics, and reconcilers are subscribers rather than special cases.
2. Storage interfaces split by concern with pagination, last-seen, transactional re-enrollment cleanup, an indexed command queue, and a migration store that carries bootstrap and unlock tokens.
3. All protocol types (commands with responses, check-in, profiles, declarations, status, protocol, errors) generated in-repo from Apple's YAML with a naming lock so regeneration never silently renames, and runtime OS and channel support metadata.
4. DDM engine embedded in-process with content-addressed tokens, dynamic membership, retained status values, and a NanoMDM `-dm` compatible proxy adapter for drop-in use.
5. Four storage backends behind one contract suite from the start.

## Verified by

1. `TestServiceHooksAndEvents` (phase 2), `TestWebhookIsASubscriber` (phase 2).
2. `storagetest.RunCommandQueueSuite/ClearIndexed` and `BenchmarkClear100k` (phase 4), `RunEnrollmentSuite/ReenrollClearsTokens` (phase 2).
3. `TestRegistryCoversEveryYAML`, `TestRenameGuard` (phase 1).
4. `TestTokenStableUnderKeyReorder`, `TestProxyServerInteropNanoMDM` (phase 5).
5. `storagetest` suites green on inmem, sqlite, postgres, mysql (phase 4).

## Rejected alternatives

- Depend on `deploymenttheory/go-sdk-appleservices` for types: emit-only and unstable; user reversed this choice.
- Depend on `korylprince/go-adm` generated packages: external single maintainer, requires a go-yaml fork, no naming contract.
- Fork NanoMDM: would inherit the untyped, context-hidden API and the single-webhook integration model.
