# Local lab acceptance testing

This document assesses the repository's current acceptance-test layers and specifies a local
lab harness. The harness runs `dmserver` in Docker and drives a headed guestweave macOS
virtual machine, or a designated physical Mac, through the managed-device lifecycle. It
writes an HTML report for local review. It covers purpose, current state, architecture,
module model, target matrix, constraints and a phased implementation plan.

Items described as *proposed* do not exist yet. Everything else describes current behavior
and cites its source.

## Purpose and scope

The existing layers prove server behavior against simulators and fixtures. The lab
establishes a different claim: on a specific Apple OS build and hardware class, a real
device enrolls in the reference server, receives APNs wakes and acts on MDM commands and
declarations with observable native effects.

- **Runs device targets locally** on an Apple silicon host with Docker and guestweave. VM and
  physical targets need Apple credentials (an MDM push certificate) and substantial disk
  space and time, so they are not CI jobs. The simulator target runs in CI.
- **Runs sequentially.** Modules execute in lifecycle order against one target at a time,
  because later stages depend on device state established earlier.
- **Scopes every result.** A result is valid only for its recorded target (platform, OS
  version, build, model, virtual or physical) and server revision. A pass on one target is
  not a pass for its schema family or for another OS release.
- **Is designed to extend.** New themes, OS releases and platforms (iOS, iPadOS) are added as
  modules, matrix entries and target drivers, without changing the runner.
- **Is the single acceptance approach.** The lab replaces `dmctl lab`, the separate
  `server/acceptance` scenario loop, the Python lab helpers and the manual live runbooks. A
  simulator target keeps today's simulated acceptance running in CI through the same runner,
  catalogue and report. See [Consolidation](#consolidation-one-acceptance-approach).

## Current state

### Test layers

| Layer | Entry point | Device | Proves |
|---|---|---|---|
| Unit | `make test` | none | Component behavior and error handling |
| Contract | `make test-contract` | none | Storage implementations obey shared interfaces |
| Component regression | `make test-e2e` (`server/e2e`, tag `e2e`) | simulator | Internal assertions: fake clocks, persistence, protocol faults |
| Acceptance | `make test-acceptance` (`server/acceptance`, tag `acceptance`) | simulator | The module catalogue against the embedded runtime and built `dmserver` processes |
| Live acceptance | `make lab-run` in a live workspace | enrolled Mac | LIVE-001 to LIVE-006 against real APNs and a real device |

See [the testing guide](../testing/lab.md) and
[decision 0048](decisions/0048-reference-server-bench.md).

### Lab model

The lab lives in the public `server/lab` package, with the device contract in
`server/lab/target`. It is public so a separate private device lab can register its own
modules and drivers against the same types and reuse this runner, result model and
report; see [decision 0048](decisions/0048-reference-server-bench.md).

- `Module` (`module.go`) has `ID`, `Name`, `Theme`, `Stage`, `Modes`, `Requires`, `Tags`,
  `Gate`, `Applies`, `Settings`, `Prerequisites` and ordered `Steps`. `Catalogue()`
  (`catalog.go`) is an explicit, stable-ordered slice; themes without an explicit stage take
  their default from `themeStages`.
- `Run` classifies each outcome as `passed`, `failed`, `blocked` (errors wrapping
  `ErrBlocked`) or `unsupported`, records per-step results and lists the module's evidence
  files. A module with `Settings` gets an isolated nested server.
- `RunAll` orders modules by stage and blocks later modules when a `Gate` module does not
  pass.
- `Target` (`target/target.go`) is `Describe`, `Exec`, `UserScript`, `OpenURL`, `Screenshot`,
  `Checkpoint`, `Restore` and `Capabilities`. `Unsupported` supplies the refusals a driver
  does not implement. `Simulator` and `Attached` are the current drivers.
- `WriteReports` (`report.go`) writes `results.json`, `junit.xml` and a self-contained
  `report.html`; `ReadResults` and `WriteHTML` back `dmctl lab report`.
- `Markdown()` generates [the module catalogue](../testing/lab-catalogue.md), and
  `make lab-docs-check` detects drift.

The live modules contain reusable device logic:

- **Tracked command:** `liveCommand` (`live.go`) queues a command, requires APNs acceptance of
  the push, then polls `…/commands/{uuid}/result` for 45 seconds. It fails on `Error` or
  timeout.
- **Inventory:** `liveInventory` requires a completed TokenUpdate before querying
  DeviceInformation.
- **Enrollment:** `liveEnrollment` (`enrollment.go`) requires recorded ACME/SCEP issuance and
  the installing user's channel.
- **DDM and Blueprints:** `liveDDM` (`live_ddm.go`) and `liveBlueprints`
  (`live_blueprints.go`) assign, wait for fresh native status, then remove and wait for
  removal.

Enrollment profiles are issued by `lab.ProfileWithIdentity` and by enrollment links, both
through `App.ExportEnrollmentProfile` (`server/internal/app/adminbench.go`), which calls
`profileForDevice` (`server/internal/app/acmetarget.go`). ACME hardware binding and
attestation are disabled for Intel Macs, macOS earlier than 14 and unresolved hardware; T2
Macs lose attestation only.

### Remaining gaps for a device-driven lab

1. **No device driver.** Profile installation, trust and every native observation are
   manual; the `attached` target observes a device only through the server.
2. **No target matrix.** One run drives one target, and there is no cross-target report.
3. **Untested from a guest.** The container stack is verified from the host only. Reaching
   it from a virtual machine's network is P4.

Closed since this document was written: the device-facing enrollment page (P1), the single
acceptance approach with a module catalogue and target contract, and the HTML report (P2),
and the container stack with device-facing HTTPS names (P3).

### Scratch-lab lessons

The untracked scratch material under `test-lab/local/apple27` (Python, AppleScript and
JavaScript for Automation (JXA) scripts) drove a physical Mac and several guestweave VMs.
These ideas carry forward:

- **Tracked command evidence:** queue, push, poll, then retain the request and response
  plists and the push outcome for each command.
- **One feature at a time:** assign, wait for valid and active status, observe the native
  effect, remove, confirm the status is removed, then audit for leftover declarations, sets
  and statuses.
- **Host safety guard:** refuse to act if the target's `IOPlatformUUID` equals the lab host's.
- **Attended checkpoints:** when a human must observe something, stop, record the question,
  the answer and a screenshot, then resume.
- **VM images and evidence:** window-scoped screenshots, a golden snapshot plus per-run
  clones, and a guard against running out of disk space.
- **Data-driven feature fixtures:** [`test-lab/apple-features`](../../test-lab/apple-features/README.md)
  has a manifest of about 32 DDM declarations, and `macos27-coverage.json` maps schema
  boundaries to native plans.

These practices are not carried forward:

- hard-coded guest IP addresses, OS builds and `/private/tmp` wrappers
- clicking at pixel coordinates
- plaintext `admin`/`admin` credentials
- SSH reverse tunnels as the network model

### Observed platform limits

- **macOS 27 guests have no APNs.** macOS 27.0 (26A428) and a 27.2 beta guest installed the
  enrollment profile but never sent TokenUpdate. Guest logs reported that key generation
  failed and no BAA certificate could be obtained. A 26.6.2 guest on the same host
  acknowledged commands in 1–3 seconds. These observations are limited to the tested
  configurations ([runbook](../operations/mac-enrollment-testing.md#feature-acceptance-and-vm-readiness)).
- **Profile install does not prove APNs readiness.** Successful HTTPS and profile
  installation do not establish APNs readiness. Only an independently pushed and
  acknowledged command does.
- **Snapshots and IPSWs need space.** Snapshots need about the VM's full disk size free, and
  IPSWs are about 26 GB.
- **Accessibility cannot be granted before enrollment.** UI scripting needs a manual,
  one-time Accessibility grant, because MDM can grant TCC only after enrollment.

### guestweave

[guestweave](https://github.com/deploymenttheory/guestweave-cli-macos) (private repository,
v1.1.0 at the time of writing) is a Go command-line interface over Virtualization.framework,
installed as `weave`. `make build` produces an ad-hoc-signed binary with the
`com.apple.security.virtualization` and `com.apple.security.hypervisor` entitlements. An
unsigned rebuild fails VM calls with `VZErrorDomain:10004`.

| Need | guestweave capability |
|---|---|
| Create from IPSW | `weave create NAME --from-ipsw latest\|PATH\|URL --disk-size N --net-profile nat` |
| Headed run | `weave run NAME` opens a window; `--no-graphics` is headless |
| Unattended setup | `--provisioning-opts file.json` (host and guest macOS 27+); `weave setup vm --mode preset\|agent` |
| Fresh identity | `weave clone SRC DST --regenerate-random-mac --regenerate-random-serial` |
| Checkpoints | `weave snapshot create\|list\|revert\|delete` (a running VM is supported) |
| Guest commands | `weave exec` over the vsock agent; `weave ssh`; `weave ip --wait` |
| Files | `--dir name:path[:ro]` (VirtioFS), SSH |
| Screen | `weave serve` REST/MCP: `weave_screenshot`, `weave_click_text`, `weave_type`, `weave_key` |
| Networking | `nat` (unprivileged), `vm-lab` (host-only), `bridged` (needs a notarized Developer ID entitlement) |

The legacy `weave-guestd` agent runs in the logged-in user's session. That session is where
UI scripting must run.

## Architecture (proposed)

```text
 Apple silicon lab host
 ┌───────────────────────────────────────────────────────────────────────────┐
 │ dmctl lab (orchestrator, Go)                                              │
 │   ├─ docker compose (deploy/lab/compose.yaml)                             │
 │   │    ├─ dmserver   HTTPS :8443  ── APNs (api.push.apple.com)            │
 │   │    └─ fixtures   HTTPS :9443  app pkgs + manifests, marker profiles,  │
 │   │                               webhook receiver                        │
 │   ├─ weave (guestweave)  ── headed VM, nat profile                        │
 │   │       ▲ exec / serve (screenshots, UI fallback)                       │
 │   └─ admin API client (lab Environment)                                  │
 │                                                                           │
 │  bridge100 192.168.64.1 ◄── published ports bound here and on loopback    │
 └──────────────────────┬────────────────────────────────────────────────────┘
                        │ NAT network
                ┌───────▼─────────────────────────┐
                │ macOS guest (15.8 / 26.6 / 27)  │
                │  /etc/hosts: mdm.lab.test       │
                │  Safari → /enroll/links/{token} │
                │  System Settings approval       │
                │  mdmclient ⇄ dmserver, APNs     │
                └─────────────────────────────────┘
```

### Components

- **Server:** a lab Compose stack at `deploy/lab/compose.yaml`, separate from the
  quick-start stack. Its `dmserver` image is built from the working tree with the
  `Dockerfile` `runtime` target and tagged with `git describe --always --dirty`. The run
  records the image digest.
- **Fixtures container:** an nginx image serving the workspace's `fixtures/` directory
  read-only over HTTPS with the lab identity: app packages and `InstallApplication`
  manifests, marker configuration profiles and other files a device fetches. The webhook
  receiver for event assertions arrives with stage 80 in P5.
- **Workspace:** the lab workspace with the `docker` server
  adapter. It keeps the retained CA,
  storage key, admin credential and live push identity (`mdm/push.pem`, `mdm/push.key`),
  mounted read-only into the container.

### Network

- The guest uses guestweave's `nat` profile, so no privileged or entitled networking is
  needed.
- The orchestrator reads the NAT gateway address from the host's bridge interface once the VM
  has started. Compose publishes `dmserver` and `fixtures` on that address and on loopback
  only; nothing is exposed on the LAN by default. Physical targets opt in to a named LAN
  address.
- Guests resolve a stable name, `mdm.lab.test` (the `.test` TLD is reserved), through an
  `/etc/hosts` entry written during provisioning. The workspace TLS leaf includes that name,
  the gateway address and any opted-in LAN address as SANs.
  `DM_PUBLIC_URL=https://mdm.lab.test:8443`.
- The container and the guest both need outbound access to APNs. `lab doctor` checks the
  container's reachability. The readiness gate proves the guest's.

### Run sequence

```text
lab tools     build/verify guestweave (latest release) and dmserver image
lab doctor    host prerequisites (blocked, never failed, when missing)
lab up        compose up → readyz → seed push identity → admin authorization
per target, sequentially:
  provision   clone golden → run headed → ip → hosts → identity capture → admission
  enroll      enrollment link → trust → Safari landing page → UI approval
  readiness   evidence (Authenticate, TokenUpdate) → APNs-woken DeviceInformation
  modules     inventory → configuration → apps → security/update → events/audit
  unenroll    remove MDM profile → CheckOut → leftover audit
  teardown    stop VM → delete clone (retained on failure when requested)
lab report    results.json → junit.xml → report.html → index.html (matrix)
lab down
```

## Enrollment links (decision 0058)

The device must fetch its profile from a web page it can reach. Decision 0048 excludes
fixture-only routes from the shipped server, so this is an operator capability with its own
contract, specified in [decision 0058](decisions/0058-enrollment-links.md) and the
[reference server API guide](../operations/reference-lab.md#enrollment-links).

| Route | Auth | Behavior |
|---|---|---|
| `POST /admin/v1/enrollment-links` | `issueEnrollmentProfile` | Creates a link bound to `DeviceID` and the optional profile fields, with a bounded `TTL`. Returns `{ID, URL, ExpiresAt}`; the token appears only in this response. |
| `GET /admin/v1/enrollment-links` | `issueEnrollmentProfile` | Lists metadata (state, binding, expiry, redemption), never tokens |
| `DELETE /admin/v1/enrollment-links/{id}` | `issueEnrollmentProfile` | Revokes an unredeemed link |
| `GET /enroll/links/{token}` | token | HTML landing page linking the trust profile and the enrollment profile; does not consume the link |
| `GET /enroll/links/{token}/profile` | token | Commits redemption, then issues through `ExportEnrollmentProfile`; publishes `enrollment-link-redeemed` |

Tokens are stored only as SHA-256 digests in the protocol state table. Unknown, expired,
revoked and redeemed tokens receive the same response. `dmctl enrollment-links
create|list|revoke` administers links. Bench scenario E2E-032 enrolls a simulator through a
link and requires reuse to be refused.

## Target drivers (proposed)

`server/lab/target` defines the device-side contract that modules use:

```go
type Target interface {
    Describe(ctx context.Context) (Info, error) // platform, OS, build, model, UDID, serial, virtual
    Exec(ctx context.Context, c Command) (Output, error) // as root or as the console user
    UserScript(ctx context.Context, osascript string) (Output, error) // in the Aqua session
    OpenURL(ctx context.Context, url string) error
    Screenshot(ctx context.Context, dst string) error
    Checkpoint(ctx context.Context, name string) error
    Restore(ctx context.Context, name string) error
    Capabilities() Capabilities // snapshots, destructive-safe, UI automation, attended
}
```

| Driver | Targets | Notes |
|---|---|---|
| `simulator` | Simulated devices | Wraps `devicemanagement/simulator` and the existing local fixtures: APNs, DEP, ABM, OIDC, attestation and admission. It runs today's simulated scenarios without Apple credentials and is the CI target. |
| `guestweave` | macOS VMs | `weave exec`, falling back to SSH; `weave serve` for screenshots and text-based click fallback; snapshots allow destructive modules |
| `ssh` | Physical Macs | A dedicated runner account with a pre-granted Accessibility permission and no snapshots; destructive modules are `unsupported` |
| `attended` | Any step | Pauses with an instruction, records the operator's answer and a screenshot. It is the fallback when automation fails, and the primary path for iOS and iPadOS until a driver exists. |
| iOS/iPadOS (later) | Physical devices | `cfgutil` where available, attended approval otherwise |

Every driver checks the host-UUID guard before its first action.

## Golden images and clones (proposed)

`dmctl lab image build -target mac-26.6-vm` prepares a golden image once per target:

1. **Download and create.** Download the pinned IPSW (URL and SHA-256 from the matrix),
   resuming if interrupted and verifying the digest. Then run
   `weave create --from-ipsw … --net-profile nat`.
2. **Complete Setup Assistant.** On macOS 27 this uses `--provisioning-opts`. On 15.8 and
   26.6 it uses `weave setup vm --mode preset` or documented manual steps. Setup creates a
   local account with auto-login.
3. **Prepare the guest.** Install the guest agent, enable Remote Login, and disable sleep,
   screen lock and software-update auto-install.
4. **Grant Accessibility.** An operator grants Accessibility to the scripting host once. The
   builder verifies the grant with a harmless System Events query.
5. **Snapshot.** Take a snapshot named `golden`, recording the IPSW digest and the guestweave
   commit.

Each run clones the golden image with `--regenerate-random-mac --regenerate-random-serial`,
so each enrollment has a new UDID and serial. The admission policy gains that UDID and serial
before enrollment and loses them at teardown. The clone is deleted after reporting unless
`-keep` is set or the run failed with `-keep-on-failure`.

## Enrollment through the device UI (proposed)

1. **Trust.** The harness copies the workspace CA's trust profile into the guest and opens
   it. Approval runs through the UI flow in step 3. Profile-installed roots are trusted for
   TLS, so Safari can load the landing page afterwards.
2. **Landing page.** The harness creates an enrollment link for the guest's UDID and opens
   it in Safari. It downloads the profile from the page, which exercises the real
   browser-download path.
3. **Approval.** `osascript` (System Events) opens
   `x-apple.systempreferences:com.apple.Profiles-Settings.extension` and selects the
   downloaded profile. It chooses Install, confirms, and enters the local password in the
   authorization dialog.
4. **Selectors.** UI steps locate elements by accessibility role and title from per-OS tables
   (`test-lab/lab/ui/macos-15.json`, `macos-26.json`, `macos-27.json`, locale `en-US`).
   Coordinates are never used. When a selector fails, the step captures the accessibility
   tree and a screenshot. It retries once through `weave_click_text`, then falls back to an
   attended checkpoint.

The installing user's `GeneratedUID` (from `dscl`) selects the user channel.

## Readiness gate (proposed)

Enrollment passes only when both of these hold:

- `GET /admin/v1/enrollments/device/{id}/enrollment-evidence` shows Authenticate, the
  certificate association and TokenUpdate for the device and the installing user.
- A fresh, independently pushed DeviceInformation command is acknowledged within its bound.
  This reuses the `liveCommand` logic.

On failure, the harness gathers `log show` output for `apsd`, `mdmclient` and
`mobileactivationd` for the current boot. Every later module is reported as
`blocked: apns-readiness`, not failed. With today's macOS 27 VMs this is the expected result,
and the harness re-tests it on each new IPSW.

## Modules (proposed)

A module is a themed, ordered group of steps:

```go
type Module struct {
    ID, Name, Theme string
    Stage    int                       // lifecycle position
    Applies  func(target.Info) string  // "" or the reason it is unsupported
    Requires []string                  // capabilities and prerequisite module IDs
    Tags     []string                  // destructive, attended, slow, supervised
    Steps    []Step
    Cleanup  func(context.Context, *Run) error
}
```

Modules are registered in an explicit, deterministic slice, like `Catalogue()`.
`dmctl lab list -format markdown` generates `docs/testing/lab-catalogue.md`, and
`make lab-docs-check` detects drift. `Applies` returns an explicit reason, such as `requires
supervision`, `macOS 26 or later` or `VM only`, that the report shows as `unsupported`.

There are two extension points:

1. **Go modules** for flows. A new theme is a package under
   `server/lab/modules/<theme>` plus one registry entry.
2. **Fixture manifests** for payloads. DDM feature modules are generated from
   `test-lab/apple-features/manifest.json`, so a new declaration is data, not code.

Existing `LIVE-00x` scenarios are wrapped as steps, so their assertions have one
implementation.

### Lifecycle catalogue

| Stage | Theme | Module content |
|---|---|---|
| 00 | preflight | Docker, weave signature and entitlements, disk space, IPSW cache, push certificate validity, server readiness |
| 10 | provision | Clone, headed boot, address, hosts entry, `Describe`, admission |
| 20 | enroll | Enrollment link, trust, Safari landing page, UI approval, enrollment evidence, user channel |
| 30 | readiness | APNs-woken DeviceInformation gate |
| 40 | inventory | DeviceInformation, SecurityInfo, CertificateList, ProfileList, InstalledApplicationList; `devices collect`; each value cross-checked against guest ground truth (`sw_vers`, `ioreg`, `system_profiler`, `profiles`) |
| 50 | configuration | InstallProfile, ProfileList and RemoveProfile with a marker payload verified under `/Library/Managed Preferences`; configuration profiles through DDM; one-at-a-time DDM features; Blueprints (LIVE-005, LIVE-006) |
| 60 | apps | InstallApplication with a fixture-hosted manifest, ManagedApplicationList, RemoveApplication; DDM package declaration; verified with `pkgutil --pkgs` and bundle presence |
| 70 | security, update | SecurityInfo, FileVault state, AvailableOSUpdates, ScheduleOSUpdateScan (no OS install by default) |
| 80 | events, audit | Webhook deliveries received for this run; `/admin/v1/audit` entries for each operator action |
| 85 | replacement | ACME and SCEP identity replacement (`lab replace`) |
| 90 | destructive | Opt-in and VM-only: DeviceLock, RestartDevice, EraseDevice, return to service; checkpoint before and restore after |
| 99 | unenroll | Remove the MDM profile, observe CheckOut, audit for leftovers (declarations, sets, queued commands) |

A step that fails does not stop later independent modules. A failed gate (stages 20 or 30)
blocks everything after it. Cleanup always runs and is reported separately.

## Target matrix (proposed)

The private file `test-lab/local/lab/matrix.json` defines targets. A committed
`test-lab/lab/matrix.example.json` documents its shape:

```json
{
  "targets": [
    {"id": "mac-15.8-vm",  "driver": "guestweave", "os": "15.8",
     "ipsw": {"url": "…", "sha256": "…"}, "golden": "golden"},
    {"id": "mac-26.6-vm",  "driver": "guestweave", "os": "26.6"},
    {"id": "mac-27-vm",    "driver": "guestweave", "os": "27.0"},
    {"id": "mac-26-physical", "driver": "ssh", "ssh": {"host": "…", "user": "labrunner"}},
    {"id": "mac-27-physical", "driver": "ssh", "ssh": {"host": "…", "user": "labrunner"}}
  ]
}
```

`dmctl lab run -target mac-26.6-vm,mac-26-physical -modules all|<stage>|<theme>|<id>` runs
targets one after another. Each result records the platform, the OS version and build, the
model identifier, the virtual flag and the server revision. The report places VM and
physical results for the same OS side by side, because they have diverged in practice.
New platforms and releases are added as matrix entries. Module `Applies` predicates decide
which modules each target runs.

## Results and report (proposed)

Each run writes to `test-lab/local/evidence/lab-<UTC timestamp>/`:

- `run.json`: host OS, guestweave commit, server image digest, revision, matrix
- `<target>/results.json`: lab `Result` fields plus target, module, stage and step
  records, each with evidence references
- `<target>/junit.xml`: for tooling
- `<target>/evidence/`: command request and response plists, declaration status snapshots,
  screenshots, UI trees and diagnostic logs
- `report.html`: one self-contained page per run (`html/template` plus `go:embed`, inline
  CSS, no network assets). It shows summary counts, a module and step timeline with
  durations, expandable evidence and linked screenshots.
- `index.html` in the evidence root: a module × target grid across runs, with differences
  from the previous run marked as regressed or fixed.

`dmctl lab report -run DIR` regenerates the HTML from JSON. The lab redaction rules apply
to every output: no credentials, raw APNs tokens, bootstrap tokens or enrollment-link
tokens.

## Tooling (proposed)

| Make target | Behavior |
|---|---|
| `lab-tools` | Fetch guestweave at its latest release tag (`GUESTWEAVE_REF` overrides it, for example `main`) into `test-lab/local/tools/guestweave`, then run `make build` and verify the entitlements with `codesign -d --entitlements -`. Build the `dmserver` image. |
| `lab-doctor` | Report missing prerequisites as blocked |
| `lab-image` | Build or refresh the golden image for `LAB_TARGET` |
| `lab-up` / `lab-down` | Start or stop the Compose stack |
| `lab-run` | Run `LAB_TARGETS` and `LAB_MODULES`; `LAB_DESTRUCTIVE=1` enables stage 90 |
| `lab-report` | Regenerate the HTML for `LAB_RUN` |
| `lab-docs`, `lab-docs-check` | Generate the module catalogue and check it for drift |

The guestweave repository is private, so fetching it requires authenticated `gh` or git
access.

## Consolidation: one acceptance approach

Acceptance previously ran through several overlapping paths: the `dmctl bench` catalogue,
the `server/acceptance` scenario loop that called it, live-only procedures in two operations
runbooks, Python fixture helpers and untracked scratch scripts. The lab replaces them with
one runner, one module catalogue, one result model and one report. The project is in alpha,
so the bench was removed outright rather than deprecated.

Two things vary independently within the lab:

- **Modes** are the service environment a module needs: `simulated` local Apple-service
  fixtures or `live` Apple credentials and a real device.
- **Targets** are where device behavior comes from: `simulator`, `attached` (an
  operator-enrolled device identified by `-device-id`), and later `guestweave`, `ssh` and
  `attended`.

**Server adapters** remain how `dmserver` runs: `inprocess` (embedded runtime), `process`
(built binary) or `docker` (the lab Compose stack).

### Principles

- **Keep module IDs.** E2E, APP and LIVE IDs are cited by `docs/testing/e2e-scenarios.md`,
  CI evidence and past PR records, so they are kept as module IDs. New lifecycle modules use
  a `LAB-` prefix. Each module keeps the `Regression` link to its detailed `server/e2e` test.
- **Rename and reuse.** Existing code moved; it was not rewritten. The workspace,
  supervisor, fixtures, admin client and live command logic became lab packages.
- **Keep one test layer.** Unit, contract and `server/e2e` component regressions stay as
  they are. They assert internals (fake clocks, persistence, protocol faults) that decision
  0048 deliberately keeps out of acceptance. `server/e2e` is component regression testing,
  not an acceptance layer.
- **Only acceptance changes location.** Library and simulator packages under
  `devicemanagement/` are unchanged.
- **No coverage gaps.** A component is removed only when the lab covers the same workflow.

### Disposition of existing work

| Existing | Disposition |
|---|---|
| `server/internal/bench` | **Moved to `server/lab`.** `Workspace` and `Environment` are the lab workspace and server adapters; the fixture builders back the `simulator` target. Each `Scenario` became a `Module` with steps, a theme and a lifecycle stage. `Result` gained target, stage, step and evidence fields, and `WriteReports` gained HTML. |
| `dmctl bench` | **Removed.** `dmctl lab` has every verb: `init`, `doctor`, `preflight`, `trust`, `list`, `up`, `run`, `report`, `profile`, `replace`, `status`, `down`. `bench run -scenario` became `lab run -modules`. |
| `make bench-*`, `BENCH_*` | **Renamed** to `make lab-*` and `LAB_*`. `bench-docs-check` became `lab-docs-check`. |
| Workspace document `bench.json` | Written as `lab.json`. `Load` still reads a legacy `bench.json` so existing live workspaces keep their identities, database and storage-key names. The retained storage-key name is unchanged. |
| `server/acceptance` | **Kept as a thin Go test wrapper.** It runs `lab.Run` against the `simulator` target with the `inprocess` and `process` adapters, and writes `report.html` beside JSON and JUnit. `cli_test.go` covers `dmctl lab`. |
| `dmctl setup adopt -from-bench` | Renamed `-from-lab`; it still adopts a live workspace, including a legacy one. |
| LIVE-001 to LIVE-006 | Run today against the `attached` target. P5 turns them into lifecycle modules driven by a real target instead of a hand-supplied `-device-id`. |
| APP-001 to APP-003 and `test-lab/host-app` | Kept as the `apppush` theme. The host app stays a device-side fixture; a later module installs and launches it on the target. |
| `test-lab/apple-features` data | Kept in place as the fixture source for manifest-driven DDM modules. `features_seed_test.go` and `os27_coverage_test.go` continue to read them. |
| `test-lab/apple-features/prepare.py`, `prepare_test.py`, `check-host.py` | **Retire** in P5, once the Go manifest loader, override handling and the preflight module replace them. |
| `docs/testing/bench.md`, `docs/testing/bench-catalogue.md`, `docs/operations/reference-bench.md` | Renamed to `docs/testing/lab.md`, the generated `docs/testing/lab-catalogue.md` and `docs/operations/reference-lab.md`. `e2e-scenarios.md` stays. |
| `docs/operations/mac-enrollment-testing.md` | Kept for now; P6 merges the manual procedure into the lab runbook as attended steps. |
| Decision 0048 | **Amended in place** (same number and filename) to describe the lab as the single acceptance approach: modes, targets, stages, gates and one evidence format. |
| Diagrams, `README.md`, `docs/architecture.md`, `docs/getting-started/*` | Updated to lab terminology. The two diagram sources keep their pinned historical revisions. |
| CI (`go-test.yml`, `release-check.yml`) | Point at `make test-acceptance lab-docs-check` and the lock test in `./lab`. |
| `test-lab/local/apple27` and other untracked scratch material | Not tracked. Operators archive any evidence they need outside the repository and delete the scripts. |
| `deploy/quickstart` and `make test-quickstart` | Kept. This is an installation smoke test for operators, not acceptance. The lab Compose stack reuses the same `runtime` image target. |

### Acceptance after consolidation

| Command | Mode | Target | Server adapter | Where it runs |
|---|---|---|---|---|
| `make test-acceptance` | simulated | `simulator` | `inprocess`, `process` | CI and locally |
| `make lab-run` in a simulated workspace | simulated | `simulator` | `process` | Locally |
| `make lab-run LAB_DEVICE_ID=…` in a live workspace | live | `attached`, later `guestweave` and `ssh` | `process`, later `docker` | Locally, with the lifecycle matrix |

All of them produce the same `results.json`, `junit.xml` and `report.html` and read from the
same catalogue.

## Constraints and risks

- **macOS 27 VM APNs.** macOS 27 VMs are expected to stop at the readiness gate until guest
  BAA key generation works. Physical macOS 27 targets cover the same OS in the meantime.
- **Supervision and ADE.** Manual enrollment is user-approved but not supervised, and VMs
  cannot use ADE. Modules that need supervision report `unsupported` with that reason.
- **ACME on VMs.** `profileForDevice` resolves Mac hardware before choosing ACME options.
  VirtualMac attestation behavior must be verified. VM targets default to SCEP until ACME
  attestation is shown to work on them.
- **UI drift.** System Settings layout and strings change between releases and locales.
  Selector tables are per OS major version and pinned to `en-US`, and failures capture the
  accessibility tree for repair.
- **TCC.** Accessibility for the scripting host is a manual, one-time golden-image step.
  After enrollment, modules may test PPPC declarations separately.
- **App fixtures.** InstallApplication needs a signed distribution package and manifest. If
  no Developer ID Installer identity or pinned vendor package is configured, stage 60 is
  blocked.
- **Resources.** IPSWs are about 26 GB, and each golden image and snapshot takes the VM's
  full disk size. `lab doctor` checks free space against the selected targets.
- **Docker runtimes.** The design assumes Docker Desktop or OrbStack, both of which publish
  container ports on host addresses. `lab doctor` verifies that the gateway address serves
  `/readyz`.
- **Harness coverage.** `server/lab` follows the 95% per-package gate. Tests use a
  fake `Target` and the embedded lab runtime. No new coverage exemptions are planned.

## Implementation plan

Each phase ends with passing `make lint`, `make test`, `make docs-check`, `make coverage`, and
its own exit criteria.

| Phase | Status | Scope | Exit criteria |
|---|---|---|---|
| P1 | Done | Decision 0058 and enrollment links: routes, protocol-state storage, redemption event, `dmctl enrollment-links`, module E2E-032 | A simulator enrolls through a link; unknown, consumed, expired and revoked links are refused identically |
| P2 | Done | Amend decision 0048. Move `server/internal/bench` to `server/lab`: target contract, `simulator` and `attached` targets, module registry with stages and gates, results with an HTML report. Replace `dmctl bench` with `dmctl lab` and `make bench-*` with `make lab-*`; rename the docs; move CI and the release-check lock test. | **Parity:** `make test-acceptance` runs every simulated module ID with the same outcomes and writes `report.html`; `lab-docs-check` replaces `bench-docs-check`; `dmctl setup adopt -from-lab` still reads a legacy workspace document |
| P3 | Done | `deploy/lab/compose.yaml` with `dmserver` and `fixtures`, the `docker` server adapter with container path translation, `lab init -adapter/-hosts`, `lab tls` reissue, `lab-tools`, container prerequisites in `lab doctor` | Verified on the host: both containers healthy, `https://127.0.0.1:<port>/readyz` and `https://mdm.lab.test:<port>/readyz` return 200 with the lab CA, and the device-facing `DM_PUBLIC_URL` is the configured host. The guest half is verified in P4. |
| P4 | | `guestweave` driver, golden-image builder, UI selector tables for macOS 26, the enroll module and the readiness gate | A macOS 26.6 VM enrolls unattended through the landing page and passes the readiness gate |
| P5 | | Stages 40–80 and 99 for macOS 26.6 VMs. LIVE-001 to LIVE-006 and APP-001 and APP-002 become lifecycle modules. Go manifest loader replaces `prepare.py`, `prepare_test.py` and `check-host.py`. | Full lifecycle run with an HTML report; every non-pass has a scoped reason; no live module depends on a hand-supplied `-device-id` |
| P6 | | `ssh` physical driver, 15.8 and 27 selector tables, matrix runs, cross-target `index.html` with run differences; merge the Mac enrollment runbook into the lab runbook | The matrix runs sequentially across VM and physical targets |
| P7 | | Stages 85 and 90, software-update scheduling, the `attended` driver, iOS and iPadOS driver design | Destructive modules restore their checkpoint; the iOS design is recorded as a decision |

P2 completed the consolidation: the structure changed without changing results, and the
bench was removed rather than deprecated because the project is in alpha. The phases that
follow add the device side, then the matrix.

## References

- [Decision 0048: reference server acceptance lab](decisions/0048-reference-server-bench.md)
- [Decision 0009: enrollment profiles](decisions/0009-enrollment-profiles.md)
- [Decision 0025: reference server roles and container](decisions/0025-reference-server-roles-and-container.md)
- [Decision 0034: admin API and authorization](decisions/0034-admin-api-and-authorization.md)
- [Decision 0055: Blueprint composition](decisions/0055-blueprint-composition.md)
- [Decision 0057: agentless device inventory](decisions/0057-agentless-device-inventory.md)
- [Testing through the reference server](../testing/lab.md)
- [Bench scenario catalogue](../testing/lab-catalogue.md)
- [Mac enrollment testing runbook](../operations/mac-enrollment-testing.md)
- [Reference server APIs and lab configuration](../operations/reference-lab.md)
- [Apple feature fixtures](../../test-lab/apple-features/README.md)
- [Lab runbook](../../test-lab/README.md)
- [Apple: Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device)
- [guestweave](https://github.com/deploymenttheory/guestweave-cli-macos) (private)
