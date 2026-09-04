# 0043: `DM_` names the configuration, `MDM` names the protocol

Status: accepted
Date: 2026-09-04
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement>
- Doc: <https://developer.apple.com/documentation/devicemanagement/declarative-management>

Apple's umbrella for this area is "device management": the documentation set is
`devicemanagement`, and it covers both the MDM protocol and declarative management. `MDM` inside
it names one specific protocol — the check-in and command transport — not the whole subject.

## References read

- `micromdm/micromdm@904493b` `cmd/micromdm/serve.go`
- `micromdm/nanomdm@d61174c` `cmd/nanomdm/main.go`, `storage/mysql/mysql_test.go`
- `jessepeterson/kmfddm@7f06151` `cmd/kmfddm/main.go`, `storage/mysql/mysql_test.go`
- `micromdm/nanodep@ae047ad` `cmd/depserver/main.go`, `storage/pgsql/pgsql_test.go`
- `fleetdm/fleet@2330565` `server/service/`

## Known pitfalls found

- Every reference prefixes with the **project** name, never the protocol name, and none of them
  had to move: `NANOMDM_` stayed correct when NanoMDM grew declarative management support,
  because the prefix never claimed to describe the protocol in the first place. A prefix that
  names a protocol is a prefix that goes stale when a second protocol arrives — which is exactly
  what happened here.
- Splitting one product across two prefixes is the failure this project already has: the server
  reads `MDM_*` and the CLI reads `MDMCTL_*`, with no shared constant and no way to enumerate
  either set. `nanocmd` reads `NANODEP_MYSQL_STORAGE_TEST_DSN` as well as its own
  `NANOCMD_MYSQL_STORAGE_TEST_DSN` for the same reason: `nanocmd`'s profile store test reads
  NanoDEP's variable (`subsystem/profile/storage/mysql/mysql_test.go:13`), so which prefix
  configures a given store depends on which project first needed it.

## What they do

- **MicroMDM**: one `MICROMDM_` prefix; flags and environment are paired by a helper so a
  variable cannot exist without a flag; the prefix is the product name.
- **NanoMDM / NanoDEP / KMFDDM**: environment is used almost only for test DSNs, all prefixed
  with the project name; runtime configuration is flags, so there is no large env surface to
  keep consistent with the documentation.
- **Fleet**: `FLEET_` on everything operator-facing, plus a second reserved namespace
  (`FLEET_VAR_`) for values interpolated into profiles; the prefix names the product and the
  sub-namespace carries the meaning.

## What we do better

1. The prefix names the domain the project actually covers. `DM_` is correct for the MDM
   protocol, for declarative management, and for the DEP, ACME and Business Manager surfaces
   that belong to neither; `MDM_` was only ever correct for one of them. It also agrees with the
   module path, `github.com/deploymenttheory/go-apple-dm`.
2. `MDM` survives wherever it is load-bearing, and the boundary is written down rather than left
   to judgement. The role value stays `DM_ROLE=mdm`, `app.RoleMDM` stays `"mdm"`, the `mdm/`
   package keeps its name, and Apple's own vocabulary is untouched:
   `MDM_CAN_REQUEST_SOFTWARE_UPDATE` and `MDM_CAN_REQUEST_PSSO_CONFIG` in `MachineInfo`,
   `MDM_SERVICE_DISCOVERY_URL_REQUIRED` and `MDM_SERVICE_DISCOVERY_URL_NOT_VALID` in the DEP
   error codes, and the `*_MDM_MIGRATION_DEADLINE` Business Manager activity types. Renaming any
   of those would change bytes on the wire, which is the test that separates the two cases.
3. One prefix per product, not one per binary. `DM_` for the server and `DMCTL_` for the CLI are
   the same word plus the tool's own name, rather than the unrelated pair `MDM_`/`MDMCTL_`. The
   binaries follow the same rule: `mdmserver` and `mdmctl` became `dmserver` and `dmctl`, and
   `internal/mdmctl` became `internal/dmctl`. The server half landed first and the CLI half in
   the change after it.
4. The tool's own name is not Apple's. `micromdm`'s CLI is also called `mdmctl`, so before this
   change the two projects' tools collided on `$PATH` and in prose: decision record 0035 has to
   say "micromdm's `cmd/mdmctl`" to disambiguate from ours in the same paragraph. `dmctl` removes
   the collision, and the references to micromdm's tool keep their spelling because they name
   someone else's program.

The exception the rule keeps is the same one everywhere else: `internal/dmctl/mdmverbs.go` holds
the verbs that drive the MDM protocol -- enrollments, commands, push, push certificates -- as
against `verbs.go`, which drives the admin plane. That file keeps its name because "mdm" there
names the protocol, not the tool.
5. No compatibility shim. `ParseEnv` takes an injected getter, so a dual-read fallback would have
   been cheap to write and permanent to carry: two documented spellings for every value and a
   legacy map nobody deletes. The rename is a `BREAKING CHANGE` on a pre-1.0 reference server
   instead, which release-please records.

## Verified by

1. `app.TestParseEnv` and `app.TestACMEEnvKeys` (prove claim 1; every case keys off the `Env*`
   constants, so they pass only if the constants and the parser agree).
2. `app.TestAdminAPIOnTheMDMRole` and `app.TestBuildSQLRoles` (prove claim 2 for the role value;
   a blanket rename would have made it `"dm"` and role dispatch would stop resolving).
3. `axm.TestActivities`, `ade.TestSoftwareUpdate`, `dep.TestError`, and
   `other.TestConformanceRoundTrip` / `other.TestConformanceValidate` (prove claim 2 for Apple's
   vocabulary; these compare literal wire values against the pinned YAML and fail if one of those
   constants is renamed).
4. `dmctl.TestDefaultConfigPathHomeFallback` and `dmctl.TestConfigTokenSources` (prove claim 3;
   the config path they assert is `go-apple-dm/dmctl.json`, so the rename cannot half-land).
   `dmctl.TestCmdMainStaysThin` parses `cmd/dmctl/main.go` by path and fails if the binary moves
   without its test following (proves claim 4).
5. `make test-e2e` with `scripts/testdb.sh` (proves claim 5 end to end: the `ddm` role runs in a
   container configured only by `-e DM_*`, so any variable missed by the rename fails the
   split-deployment hop rather than silently taking a default).

## Rejected alternatives

- `APPLEDM_` or `GOAPPLEDM_`: the project-name prefix every reference uses, and the most
  collision-proof option. Rejected as too long for a surface of 60-odd variables that appears in
  every container definition and documentation example; `DM_` keeps the same property in
  practice because nothing else in an MDM deployment claims it.
- Keeping `MDM_`: zero churn, and defensible while the transport really is called MDM. Rejected
  because it disagrees with the module path and reads as though declarative management were a
  subordinate feature of MDM rather than a peer — the misreading 0039 exists to prevent.
- `ADM_` for "Apple device management": collides with Amazon Device Messaging, which is a push
  service, in a project whose subject includes push.
- Composing the names from a single `envPrefix` constant (`EnvRole = envPrefix + "ROLE"`):
  removes the 62 repetitions of the prefix, but an operator grepping the source for
  `DM_ADMIN_TOKEN` would find nothing. The names are an operator-facing contract; they stay
  greppable.
- A dual-read fallback with a deprecation warning: see claim 4.
