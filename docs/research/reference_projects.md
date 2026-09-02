# Apple MDM and DDM: open source reference projects

Research date: 2026-09-01. All repository facts (language, license, archived flag, last push, stars)
were read from the GitHub API on that date. Apple documentation URLs were fetched and confirmed to
resolve. Anything that could not be verified was left out or is explicitly flagged.

## Purpose

`go-apple-mdm` is a Go library for the Apple Mobile Device Management (MDM) protocol and
Declarative Device Management (DDM). This document is the prior-art map: where Apple's primary
sources are, which open source projects implement which parts of the protocol, which building
blocks (plist, PKCS7, SCEP, APNs, DEP) the Go ecosystem has already settled on, and where the gaps
are. It is a reference list, not a design document.

## How to read this document

Each project entry has the same shape:

```markdown
### Project name
- Repo: url · Language · License · Status (last push YYYY-MM) · Stars
- Implements: capabilities from the checklist in section 3
- Notes: what it is, what it builds on, why it matters as a reference
```

Status legend:

| Status | Meaning |
|---|---|
| Active | Pushed within the last 12 months |
| Maintenance | Pushed within 1 to 3 years, or the maintainer has declared it maintenance-only |
| Historical | No push in more than 3 years, or archived on GitHub |
| Experimental | Author describes it as a proof of concept, playground, or work in progress |

Go projects are listed first in every section because this repository is Go. Projects in other
languages are included for protocol knowledge, not for code reuse.

---

## 1. Apple primary sources

### 1.1 Device Management documentation hub

<https://developer.apple.com/documentation/devicemanagement>

The pages render through JavaScript. The reliable way to enumerate them programmatically is the
DocC JSON behind each page, at
`https://developer.apple.com/tutorials/data/documentation/devicemanagement/<slug>.json`.
The hub is organised as follows (all paths are under `/documentation/devicemanagement/`).

**MDM protocol**

| Page | URL | Useful for |
|---|---|---|
| Check-in | <https://developer.apple.com/documentation/devicemanagement/check-in> | The device-to-server check-in channel: `Authenticate`, `UserAuthenticate`, `CheckOut`, `GetToken`, `TokenUpdate`, `GetBootstrapToken`, `SetBootstrapToken`, `ReturnToService`, plus the four DDM check-in messages `DeclarativeManagement`, `declaration-items`, `status`, and `tokens`. |
| Commands and queries | <https://developer.apple.com/documentation/devicemanagement/commands-and-queries> | Every MDM command and its response, grouped: declarative management, profile management, device details, device state, managed apps, managed media, accounts, passwords, lost device, Recovery Lock, content caching, AirPlay mirroring, eSIM, managed settings, lights-out management, security, extensions, enhanced logging, user management. Each command lives at `<name>-command`. |
| Removed commands and profiles | <https://developer.apple.com/documentation/devicemanagement/removed-commands-and-profiles> | The legacy software update commands (`AvailableOSUpdates`, `OSUpdateStatus`, `ScheduleOSUpdate`, `ScheduleOSUpdateScan`) and the `SoftwareUpdate` profile. Apple states these stop functioning on all 27.0 operating systems, so DDM is the only software update path going forward. |

**Declarative management**

| Page | URL | Useful for |
|---|---|---|
| Declarations | <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations> | The declaration catalogue. Configurations (48 types at time of writing, including `LegacyProfile`, `SoftwareUpdateEnforcementSpecific`, `Package`, `SecurityIdentity`, the VPN and DNS families), Activations (`ActivationSimple`), Assets (data, credential ACME/certificate/identity/SCEP/username-password, user identity), Credentials, Management (`ManagementOrganizationInformation`, `ManagementProperties`, `ManagementServerCapabilities`), and `DeclarationBase`. |
| Status items | <https://developer.apple.com/documentation/devicemanagement/status-items> | `StatusReport`, `StatusReason`, and every status item a device can report: account lists, apps and packages, content cache, device properties, enhanced logging, management (declarations and client capabilities), MDM protocol state (enrollment type, push token, push magic, awaiting configuration), migration assistant, passcode and security, software update, and test items. |
| Declarative Management request | <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest> | The endpoint shape for `declaration/activation|asset|configuration|management/{identifier}`, `declaration-items`, `status`, and `tokens`. |
| Integrating declarative management | <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management> | Apple's prose on how DDM coexists with MDM v1 on the same enrollment. |
| Leveraging the declarative management data model | <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices> | The activation, configuration, asset, management model and predicate-driven autonomy. |

**Implementing device management (guides)**

| Page | URL | Useful for |
|---|---|---|
| Device management essentials | <https://developer.apple.com/documentation/devicemanagement/device-management-essentials> | Connectivity, certificates, inactive devices and invalid push tokens, APNs setup, sending commands, handling `NotNow`. |
| Device enrollment | <https://developer.apple.com/documentation/devicemanagement/device-enrollment> | Enrollment profiles, Automated Device Enrollment web-view authentication, Platform SSO enrollment, and the account-driven enrollment flows (simple auth, OAuth 2, enrollment SSO, and the `.well-known/com.apple.remotemanagement` service discovery endpoint). |
| Identity management | <https://developer.apple.com/documentation/devicemanagement/identity-management> | Passcodes, Platform Single Sign-on, Managed Device Attestation validation, macOS users. |
| Content management | <https://developer.apple.com/documentation/devicemanagement/content-management> | Deploying apps with DDM, provisioning profiles. |
| Device life cycle | <https://developer.apple.com/documentation/devicemanagement/device-life-cycle> | Software updates via DDM, enforcement phases, beta enrollment, Return to Service, device migration. |
| Managing connections | <https://developer.apple.com/documentation/devicemanagement/managing-connections> | Contains the five enrollment error pages that correspond to `mdm/errors/*.yaml` in the schema repo. There is no standalone error codes page any more. |

**Configuration profiles**

| Page | URL | Useful for |
|---|---|---|
| Profile-specific payload keys | <https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys> | `TopLevel`, `CommonPayloadKeys`, and every payload type (accounts, certificates including `SCEP` and `ACMECertificate`, FileVault, login, mail, networking, restrictions, and so on). |

**Deployment services (web APIs)**

| Page | URL | Useful for |
|---|---|---|
| Device assignment | <https://developer.apple.com/documentation/devicemanagement/device-assignment> | This is the public DEP web service documentation. OAuth 1.0a session auth, `fetch-devices`, `sync-devices`, `device-details`, `disown-devices`, activation lock, profile define/fetch/assign/remove, account-driven enrollment service discovery assignment, and the `Device`, `MachineInfo`, `Profile` objects. |
| App, Book, and Subscription Management | <https://developer.apple.com/documentation/devicemanagement/app-book-and-subscription-management> | The Apps and Books (VPP) API. |
| Roster management | <https://developer.apple.com/documentation/devicemanagement/roster-management> | Apple School Manager rosters. |
| Apple School Manager and Apple Business APIs | <https://developer.apple.com/documentation/apple-school-and-business-manager-api> | Hub for the newer OAuth 2 APIs at `/documentation/applebusinessapi` and `/documentation/appleschoolmanagerapi` (devices, MDM servers, activities, users, blueprints). The slug is `applebusinessapi`, not `applebusinessmanagerapi`. |

### 1.2 Apple device management schema repository

- Repo: <https://github.com/apple/device-management> · YAML · MIT · Active (last push 2026-08) · 971 stars
- Default branch `release` tracks shipping OS versions (26.4 at time of writing). Beta content
  lands on `seed_*` branches; `seed_OS_27_0` is current.
- Layout and file counts:

| Directory | Files | Contents |
|---|---|---|
| `mdm/checkin` | 9 | Authenticate, TokenUpdate, CheckOut, GetToken, Set/GetBootstrapToken, UserAuthenticate, ReturnToService, DeclarativeManagement |
| `mdm/commands` | 65 | One YAML per command with request and response keys |
| `mdm/profiles` | 127 | Configuration profile payloads |
| `mdm/errors` | 5 | Enrollment error codes |
| `declarative/declarations` | 52 | Activations, assets, configurations, management, `declarationbase.yaml` |
| `declarative/status` | 48 | Status items |
| `declarative/protocol` | 3 | `declarationitemsresponse`, `statusreport`, `tokensresponse` |
| `other` | 5 | `esso`, `machineinfo`, `manifesturl`, `passwordhash`, `skipkeys` |
| `docs` | 3 | `schema.yaml` (the meta-schema), `schema.md`, `errata.md` |

- Apple does not accept pull requests; feedback goes through Feedback Assistant.
- This repository is the machine-readable source that every code generator in section 5 consumes.

### 1.3 Apple Platform Deployment guide

| Page | URL |
|---|---|
| Guide root | <https://support.apple.com/guide/deployment/welcome/web> |
| Enrollment methods for Apple devices | <https://support.apple.com/guide/deployment/intro-to-apple-device-enrollment-types-dep08f54fcf6/web> |
| Intro to declarative device management | <https://support.apple.com/guide/deployment/intro-to-declarative-device-management-depb1bab77f8/web> |
| Use declarative device management | <https://support.apple.com/guide/deployment/declarative-device-management-manage-apple-depc30268577/web> |
| Software update enforcement declaration | <https://support.apple.com/guide/deployment/software-update-declarative-configuration-depca14ecd4d/web> |
| Software update settings declaration | <https://support.apple.com/guide/deployment/software-update-settings-declarative-dep0578d8b8a/web> |
| WWDC26 device management updates | <https://support.apple.com/guide/deployment/device-management-updates-depd638aa061/web> |

The last page is the pre-release feature list for OS 27: VPN, DNS and relay declarations, legacy
profiles as assets, enrollment and health status items, and remote log collection commands.

### 1.4 WWDC sessions

| Year | Session | Title | URL |
|---|---|---|---|
| 2021 | 10131 | Meet declarative device management | <https://developer.apple.com/videos/play/wwdc2021/10131/> |
| 2022 | 10046 | Adopt declarative device management | <https://developer.apple.com/videos/play/wwdc2022/10046/> |
| 2022 | 10045 | What's new in managing Apple devices | <https://developer.apple.com/videos/play/wwdc2022/10045/> |
| 2023 | 10040 | What's new in managing Apple devices | <https://developer.apple.com/videos/play/wwdc2023/10040/> |
| 2024 | 10143 | What's new in device management | <https://developer.apple.com/videos/play/wwdc2024/10143/> |
| 2025 | 258 | What's new in Apple device management and identity | <https://developer.apple.com/videos/play/wwdc2025/258/> |
| 2025 | 203 | Get to know the ManagedApp Framework | <https://developer.apple.com/videos/play/wwdc2025/203/> |
| 2026 | 206 | What's new in managing Apple devices | <https://developer.apple.com/videos/play/wwdc2026/206/> |

### 1.5 Community schema for Apple web services

- Repo: <https://github.com/micromdm/apple-device-services> · JSON Schema · MIT · Active (2026-09) · 20 stars
- Hand-built JSON Schemas for the ABM/ASM API, the DEP device assignment API, Apps and Books,
  and GDMF. Apple publishes no machine-readable schema for these web services, so this fills the
  gap that `apple/device-management` does not cover. Explicitly incomplete.

---

## 2. Protocol surface checklist

These are the capability labels used in the entries and the matrix in section 8.

| Label | Meaning |
|---|---|
| Check-in | MDM v1 check-in channel: Authenticate, TokenUpdate, CheckOut, GetToken, bootstrap tokens |
| Commands | Command queue, `Idle` polling on the server URL, command responses, `NotNow` handling |
| APNs push | Sending the MDM wake-up push (empty payload with the `mdm` key and push magic) |
| Profile enrollment | Serving an enrollment profile with an MDM payload (manual, OTA, or BYOD) |
| DEP/ADE | Automated Device Enrollment: DEP web service client, profile assignment, sync cursor |
| User Enrollment | User channel, managed Apple Accounts, BYOD enrollment |
| Account-driven | Account-driven User or Device Enrollment including service discovery |
| DDM server | Serving declarations, declaration-items, tokens, and consuming status reports |
| DDM client | Simulating the device side of DDM |
| DDM codegen | Generating or validating types from the Apple YAML schema |
| SCEP | SCEP server or client for the enrollment identity |
| ACME | ACME with `device-attest-01` for Managed Device Attestation |
| Signed payloads | Verifying the `Mdm-Signature` header and CMS signing of profiles |
| Webhooks | Publishing check-in and command events to an external service |

---

## 3. Full MDM servers

### 3.1 Go

### NanoMDM
- Repo: <https://github.com/micromdm/nanomdm> · Go · MIT · Active (2026-08) · 640 stars
- Implements: Check-in, Commands, APNs push, User Enrollment, Account-driven, DDM server (proxy only), Signed payloads, Webhooks
- Notes: The minimalist, stateless, horizontally scalable MDM core and the most-cited Go reference.
  It deliberately excludes SCEP, TLS termination, enrollment profile generation, and DEP. Storage
  backends are `filekv` (default), `inmem`, `mysql`, `pgsql`, and an `allmulti` fan-out. It does not
  store declarations; the `-dm` flag forwards DDM check-in messages to a separate DDM server with
  optional HMAC signing of the body. The handoff lives in `service/nanomdm/dm.go`. Ships as a
  library (`service`, `storage`, `push`, `mdm` packages) and a binary, plus `nano2nano` for
  migrating enrollments between backends. Basis for NanoHUB, Fleet, Cairn, Micromanage, and
  Local MDM.

### NanoHUB
- Repo: <https://github.com/micromdm/nanohub> · Go · MIT · Active (2026-09) · 52 stars
- Implements: everything NanoMDM does, plus Commands orchestration (NanoCMD) and DDM server (KMFDDM)
- Notes: Unifies NanoMDM, NanoCMD, and KMFDDM in one process with `/api/v1/nanomdm/`,
  `/api/v1/nanocmd/`, and `/api/v1/ddm/` routes. It does not include NanoDEP. Storage is `file`,
  `mysql` 8.0.19+, or `inmem`. Intended as a library (`nanohub`, `ddmadapter`, `enqueue`,
  `cmdservice`) with a reference `cmd/nanohub`. This is the simplest way to stand up a complete
  DDM-capable open source MDM today. `macadmins/nanohubctl` (Go, MIT, 2026-03) is a CLI for its API.

### Fleet
- Repo: <https://github.com/fleetdm/fleet> · Go · Mixed (MIT core, `ee/` under the Fleet EE License, `docs/` CC BY-SA 4.0) · Active (2026-09) · 6,799 stars
- Implements: Check-in, Commands, APNs push, Profile enrollment (manual, OTA, BYOD), DEP/ADE, User Enrollment, Account-driven, DDM server, DDM client, SCEP, ACME, Signed payloads, plus VPP, Platform SSO, and Windows, Android and Linux management
- Notes: Fleet's Apple MDM is built on vendored forks rather than imported modules:
  - `server/mdm/nanomdm/` was copied in January 2024 from the `apple-mdm` branch of
    `fleetdm/nanomdm` and re-synced in November 2024 to upstream commit `825f297`. Its
    `service/nanomdm/dm.go` is the DDM proxy shim.
  - `server/mdm/nanodep/` was vendored with `UPSTREAM_COMMIT` `4c207e8`.
  - `server/mdm/scep/` came from `fleetdm/scep` and was re-synced in September 2024.
  - The standalone `fleetdm/nanomdm`, `fleetdm/nanodep`, and `fleetdm/scep` forks are dormant
    (last pushes 2023-07, 2022-12, and 2024-01).
  - `go.mod` still imports `micromdm/micromdm` v1.9.0, `micromdm/nanolib`, `micromdm/plist`
    v0.3.0, `howett.net/plist`, `smallstep/pkcs7`, and `smallstep/scep`.
  - Fleet-specific Apple logic is in `server/mdm/apple/` (commander, profile matcher, processor
    and verifier, reconcile, `apple_bm.go` for Apple Business Manager, GDMF, VPP, PSSO, app
    manifests, mobileconfig helpers) and `server/mdm/acme/`.
  - The DDM server implementation is in MIT-licensed code: `server/service/apple_mdm.go`,
    `server/service/apple_mdm_declarations_batched.go`, `server/mdm/apple/reconcile.go`,
    `server/datastore/mysql/apple_mdm.go`, and `server/worker/apple_mdm.go`. Migrations show
    the evolution: DDM tables (2024-03), token rename (2024-12), scoping (2025-06), DDM
    variables (2026-04), DDM assets (2026-07), custom activations (2026-07).
  - Premium-only glue (team-scoped batch apply, GitOps) is in `ee/server/service/mdm.go` and
    `ee/server/service/apple_mdm.go` under the EE license.
  - Architecture docs worth reading: `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
    (lifecycle, delivery, verification, database), `docs/Contributing/product-groups/mdm/`
    (overview, account-driven user enrollment, APNs mock, custom SCEP, end user auth), and
    `tools/mdm/apple/glossary-and-protocols.md`.
  - Test and simulation tooling is covered in section 6.
  - Storage is MySQL plus Redis. Fleet is a product, not a library, but it is the largest
    production DDM server with open code.

### MicroMDM
- Repo: <https://github.com/micromdm/micromdm> · Go · MIT · Maintenance (2026-08) · 2,668 stars
- Implements: Check-in, Commands, APNs push, Profile enrollment, DEP/ADE, SCEP, Signed payloads, Webhooks, DDM server (proxy only since v1.11)
- Notes: The original open source Apple MDM in Go. Single process with a local BoltDB store, so no
  high availability, with the `mdmctl` CLI for the API and for push certificate workflows
  (`mdmctl mdmcert vendor`, `mdmctl mdmcert.download`). Officially in maintenance mode; the README
  states support ends at the end of 2025 and points to NanoMDM. Still the reference that
  MDMDirector and Fleet's go.mod build on. Packages under `platform/*` cover apns, blueprint,
  command, dep, device, profile, and queue.

### KMFDDM
- Repo: <https://github.com/jessepeterson/kmfddm> · Go · MIT · Active (2026-09) · 85 stars
- Implements: DDM server
- Notes: Listed here because it is the DDM half of a full server. See section 4 for detail.

### MDMDirector
- Repo: <https://github.com/mdmdirector/mdmdirector> · Go · Apache-2.0 · Active (2026-09) · 120 stars
- Implements: Commands orchestration, profile state management, `InstallApplication`, profile signing
- Notes: A webhook consumer for MicroMDM that keeps a desired-state model of profiles per device
  and re-pushes on OS build changes. PostgreSQL plus Redis. REST API and Prometheus metrics. Not
  usable without MicroMDM.

### NanoCMD
- Repo: <https://github.com/micromdm/nanocmd> · Go · MIT · Active (2026-09) · 17 stars
- Implements: Commands orchestration (workflow engine), not a check-in server
- Notes: Library and reference server that turns MDM commands into multi-step workflows with
  response pairing, timeouts, and exclusivity, driven by MDM webhooks. Built-in workflows:
  `certprof`, `cmdplan`, `devinfolog`, `fvenable`, `fvrotate`, `inventory`, `lock`, `profile`.
  Storage: `diskv`, `inmem`, `kv`, `mysql`. Uses `mdmcommands` for typed commands and includes
  `utils/mobileconfig`, a profile parser. The closest thing to a reusable command queue helper.

### Cairn-MDM
- Repo: <https://github.com/nickpdawson/Cairn-MDM> · Go · MIT · Experimental (2026-08) · 0 stars
- Implements: Check-in, Commands, APNs push, Profile enrollment (OTA), SCEP, Signed payloads
- Notes: Single-binary homelab MDM that embeds NanoHUB, a SCEP CA, and an admin UI over SQLite.
  DDM and ADE are explicitly not yet supported and the author flags a device certificate trust
  weakness before 1.0. Useful only as an example of embedding NanoHUB as a library.

### Local MDM
- Repo: <https://github.com/Malcolm/local-mdm> · Go · MIT · Experimental (2026-05) · 0 stars
- Implements: Apple MDM via embedded NanoMDM, SCEP, DEP/ADE via NanoDEP, plus Windows OMA-DM
- Notes: Multi-tenant control plane with an HTMX UI, Keycloak OIDC, and PostgreSQL. Another
  example of wrapping NanoMDM and NanoDEP inside a larger product.

### VEx
- Repo: <https://github.com/roperzh/VEx> · Go · No license · Experimental (2026-02) · 0 stars
- Implements: Check-in, APNs push, DDM server
- Notes: Described as a "minimalistic playground for Apple MDM with full DDM support" in flat Go
  files over SQLite. The author is a Fleet MDM engineer. Unlicensed personal playground.

### 3.2 Other languages

### Zentral
- Repo: <https://github.com/zentralopensource/zentral> · Python (Django) · Mixed (Apache-2.0 core, `ee/` under the Zentral Pro Evaluation License) · Active (2026-09) · 879 stars
- Implements: Check-in, Commands, APNs push, Profile enrollment (OTA with SCEP), DEP/ADE, User Enrollment, Account-driven, DDM server, SCEP and ACME issuers, Signed payloads, FileVault escrow, software update enforcement, Apps and Books, push CSR signing
- Notes: The MDM module is `zentral/contrib/mdm` and sits in the Apache-2.0 core, not under `ee/`.
  Files worth reading: `apns.py`, `dep.py`, `dep_client.py`, `push_csr_signers.py`,
  `software_updates.py`, `cert_issuer_backends/`, and the `declarations/` package (see section 4).
  It vendors Apple's YAML under `zentral/contrib/mdm/schema_data/` with `update.sh` and
  `reference.txt` pinning the commit, and validates declarations against it. PostgreSQL. The most
  complete non-Go DDM server, usable only as a whole platform.

### Commandment
- Repo: <https://github.com/cmdmnt/commandment> · Python (Flask, SQLAlchemy) · MIT · Historical (2023-04) · 324 stars
- Implements: Check-in, Commands, APNs push, Profile enrollment, DEP/ADE, SCEP (own PKI), VPP, Signed payloads
- Notes: Modules for `apns`, `dep`, `enroll`, `pki`, `vpp`, `profiles`, with a React UI. No commits
  in over three years, but the clearest Python reading of the pre-DDM protocol. Note the org is
  `cmdmnt`, not `mosen`.

### Micromanage
- Repo: <https://github.com/liemeldert/Micromanage> · Python · No license · Experimental (2026-08) · 5 stars
- Implements: Profile enrollment (OTA with bundled step-ca), DEP/ADE, DDM server (device channel only), compliance, escrow, all on top of NanoMDM
- Notes: Work-in-progress infrastructure-as-code controller and web UI over NanoMDM. Implements
  its own DDM endpoints and does OS update enforcement DDM-only. Single maintainer, no license.

### oreore-ios-mdm
- Repo: <https://github.com/YusukeIwaki/oreore-ios-mdm> · Ruby (Rails) · MIT · Experimental (2026-06) · 1 star
- Implements: Check-in, Commands, APNs push, Profile enrollment, DDM server
- Notes: Personal Rails MDM with `Ddm::Configuration` and `Ddm::Activation` models and a `/ddm`
  UI. The only Ruby DDM implementation found.

### apple-mdm-poc (Rust)
- Repo: <https://github.com/maulanasdqn/apple-mdm-poc> · Rust (Axum, sqlx, OpenSSL CMS) · No license · Experimental (2026-06) · 0 stars
- Implements: Check-in (Authenticate, TokenUpdate, CheckOut), Commands, Signed enrollment profile; SCEP and APNs are stubbed
- Notes: The only Rust implementation found. Cargo workspace with an `mdm` library crate, a
  gateway binary, and a Cloudflare Workers port. Self-described scaffold verified only against a
  simulated device.

### TDS MDM
- Repo: <https://github.com/thomasdye12/TDSMDM> · JavaScript front end, PHP back end · No license · Experimental (2026-08) · 4 stars
- Implements: DDM server as a MicroMDM `-dm` backend, status subscriptions
- Notes: Hobby project. The only PHP DDM implementation found.

### apple-mdm-system
- Repo: <https://github.com/JieAnthony/apple-mdm-system> · PHP (Laravel, Filament) · No license · Active (2026-06) · 11 stars
- Implements: DEP/ADE and Commands (activation lock, lost mode, restrictions, app inventory) via NanoMDM, NanoDEP, and micromdm/scep
- Notes: Chinese management panel for device rental scenarios. A control plane over the Nano
  stack, not a protocol implementation.

### nanomdm-ui, Apple-Dep, CDMH-Scep
- Repos: <https://github.com/cidumh/nanomdm-ui> (PHP, MIT, 2026-06), <https://github.com/cidumh/Apple-Dep> (Python, MIT, 2026-06), <https://github.com/cidumh/CDMH-Scep> (Python, MIT, 2026-06) · 1 star each
- Implements: Commands UI via NanoMDM webhook; DEP/ADE (certificate generation, S/MIME token decryption, OAuth 1 session, reverse proxy to `mdmenrollment.apple.com`); SCEP
- Notes: Lightweight Chinese stack. Apple-Dep is a small Python re-implementation of what
  NanoDEP's `depserver` does, which makes it a readable second view of the DEP auth flow.

---

## 4. Declarative Device Management implementations

The DDM space is small. Every real implementation found is listed, including experimental ones.

### KMFDDM
- Repo: <https://github.com/jessepeterson/kmfddm> · Go · MIT · Active (2026-09) · 85 stars
- Implements: DDM server
- Notes: The canonical open source DDM server. It is not an MDM: NanoMDM (`-dm` flag) or
  MicroMDM (v1.11+) proxy the device's DDM check-in messages to it over HTTP with an
  `X-Enrollment-ID` header and optional HMAC. It manages declarations, sets, enrollment
  associations, `ServerToken` hashing and versioning, the declaration-items and tokens endpoints,
  status report storage with value queries, and a notifier that enqueues the
  `DeclarativeManagement` command back to the MDM. Packages: `ddm/` (parsing), `storage/`
  (`file`, `filekv`, `inmem`, `mysql`, `shard`, `multi`), `http/`, `notifier/`, `jsonpath/`. Ships
  Python helpers `tools/ideclr.py` (declaration generator) and `tools/syncdir.py`. README still
  says experimental. Best reference for the server-side token model.

### NanoHUB
- See section 3.1. Composes KMFDDM into one process via `ddmadapter`.

### Fleet
- See section 3.1 for file paths. Fleet is the largest production DDM server with open code and
  the only project with an open DDM client simulator (section 6).

### Zentral
- DDM module: `zentral/contrib/mdm/declarations/` (Apache-2.0). `protocol.py` dispatches on the
  `declaration/<type>/<identifier>` endpoints; `declaration.py`, `cert_asset.py`, `data_asset.py`,
  `legacy_profile.py`, `management.py` (status subscriptions), `software_update.py`,
  `status_report.py`, and `linkers.py`. A good reference for wrapping legacy profiles as
  `LegacyProfile` configurations and for mapping status reports into an inventory model.

### go-adm
- Repo: <https://github.com/korylprince/go-adm> · Go · MIT · Active (2026-08) · 6 stars
- Implements: DDM codegen (commands, profiles, declarations, status), YAML schema parser
- Notes: The most complete Go generator over `apple/device-management`. Parses the YAML into an
  AST (`yamlschema`, `schema`) and generates Go via `cmdgen`, `profilegen`, `declgen`,
  `structgen`, and `yamlschemagen`, with pre-generated packages under `generated/{mdm,declarative,other}`
  pinned by `GENERATE_SHA`. Relies on a go-yaml fork to handle Apple's recursive YAML. Unlike
  `mdmcommands` it covers declarations and status items. Directly relevant for typed DDM structs.

### mdmcommands and admgen
- Repos: <https://github.com/jessepeterson/mdmcommands> (Go, Unlicense, 2026-08, 6 stars) and <https://github.com/jessepeterson/admgen> (Go, Unlicense, 2023-07, 11 stars)
- Implements: Codegen for MDM commands only, not DDM declarations
- Notes: `admgen` provides `admgencmd`, which generates Go request and response structs from
  `mdm/commands/*.yaml`; `mdmcommands` is the regenerated output with the schema repo as a git
  submodule. It includes the `DeclarativeManagement` command but nothing from `declarative/`.
  Zero third-party dependencies. Consumed by NanoCMD and NanoHUB.

### go-sdk-appleservices
- Repo: <https://github.com/deploymenttheory/go-sdk-appleservices> · Go · MIT · Active (2026-09) · 3 stars
- Implements: DDM codegen (typed, spec-validated MDM command plists, profiles, and DDM JSON), ABM/ASM API client, notarization, update CDN
- Notes: Same organisation as this repository, so treat as a sibling rather than an external
  reference. `device_management/` has `mdm/`, `ddm/`, `validate/`, `metadata/`, and `cmd/`.
  Early stage, no external importers yet. Cloned by `make refs` since 2026-09-02; its `axm/`
  package and acceptance tests are the behavioural reference for the live Apple Business
  Manager API (record 0030). Study only: the repo rules forbid depending on it.

### Contour
- Repo: <https://github.com/macadmins/contour> · Rust · Apache-2.0 · Active (2026-06) · 85 stars
- Implements: DDM codegen (validator and generator), Signed payloads (unsign and normalise profiles)
- Notes: CLI that generates and validates `.mobileconfig` files and DDM JSON declarations against
  Apple's schema embedded in the binary (`crates/mdm-schema`, `contour-core`, `contour-profiles`).
  Recipe-driven and MDM-agnostic. The only Rust DDM tooling found and a good reference for
  schema-driven validation. `headmin/config-generated` (Apache-2.0, 2026-08) is a companion dump of
  generated declarations including beta types.

### mobileconfig-builder
- Repo: <https://github.com/dantecatalfamo/mobileconfig-builder> · JavaScript · AGPL-3.0 · Experimental (2026-05) · 3 stars
- Implements: DDM codegen (browser-side declaration builder)
- Notes: Vite app with the schema repo as a submodule; a script converts the YAML to JSON and the
  UI emits `declarations.json`. The only TypeScript or JavaScript consumer of the schema found.

### Sample declarations and deployment kits
- <https://github.com/macadmins/ddm_examples> · JSON · MIT · Historical, archived (2024-07) · 18 stars. Real
  configurations, assets, activations, management declarations and sets laid out for KMFDDM's
  `syncdir.py`. Still the most readable set of example declarations.
- <https://github.com/macadmins/ddm_infra> · Shell · MIT · Historical, archived (2024-07) · 8 stars. NanoMDM
  plus KMFDDM deployment scripts.
- <https://github.com/openmac-org/nanohub-acme-docker>, <https://github.com/openmac-org/nanomdm-acme-docker>,
  <https://github.com/openmac-org/nanomdm-scep-docker> · MIT · 2026-06. Compose stacks for
  NanoHUB or NanoMDM plus KMFDDM with step-ca (ACME) or SCEP and MySQL.

### DDM tools that are not open source implementations
- <https://github.com/Jamf-Concepts/ddm-explorer> · App Store app; the repo holds only docs and a custom
  license · 50 stars. Browses declaration and status schemas including seed branches and views a
  device's status reports through Jamf Pro. Not open source.
- <https://github.com/huexley/DDMStatus> (Swift, MIT, 2026-02, 19 stars) and
  <https://github.com/dan-snelson/DDM-OS-Reminder> (Shell, MIT, 2026-08, 115 stars) are device-side
  end user tooling for software update enforcement, not protocol code.

---

## 5. Libraries and building blocks

### 5.1 Dependency facts from go.mod files

| Project | plist | PKCS7 | SCEP | APNs | Other |
|---|---|---|---|---|---|
| nanomdm | micromdm/plist | smallstep/pkcs7 | none (external) | own `push/nanopush` plus a buford adapter | nanolib |
| micromdm | micromdm/plist | smallstep/pkcs7 | micromdm/scep/v2 | buford | cfgprofiles, go4, go-macos-pkg |
| nanocmd | micromdm/plist | smallstep/pkcs7 | none | none | mdmcommands, nanolib |
| nanodep | none | smallstep/pkcs7 | none | none | nanolib |
| fleet | micromdm/plist, howett.net/plist, groob/plist (legacy path) | smallstep/pkcs7 (mozilla and secDre4mer indirect) | smallstep/scep | buford | micromdm/micromdm, nanolib |

### 5.2 Go MDM libraries and shared utilities

### mdmcommands
- Repo: <https://github.com/jessepeterson/mdmcommands> · Go · Unlicense · Active (2026-08) · 6 stars
- Category: Go MDM library
- Notes: Generated Go structs and helpers for every MDM command and response, one file per
  Apple category (`cmd_device.go`, `cmd_profile.go`, `cmd_update.go`, and so on). No third-party
  dependencies. Ten importers on pkg.go.dev including NanoCMD. The lowest-risk building block for
  typed command plists.

### go-adm
- See section 4. The generator that also covers profiles and declarations.

### nanolib
- Repo: <https://github.com/micromdm/nanolib> · Go · MIT · Active (2026-08) · 2 stars
- Category: Shared utilities
- Notes: Daemon plumbing shared by the Nano suite: `envflag`, `http`, `log`, `storage`. Only
  dependency is `peterbourgon/diskv`. Required by nanomdm, nanocmd, nanodep, and Fleet. Not MDM
  protocol specific. `jessepeterson/nanolib-x` (WTFPL, 2026-08) holds extended `http` and
  `storage` packages.

### go4
- Repo: <https://github.com/micromdm/go4> · Go · MIT · Maintenance (2024-02) · 7 stars
- Category: Shared utilities
- Notes: Older MicroMDM standard library (`env`, `httputil`, `version`). Frozen; nanolib is the
  successor.

### mdmutil
- Repo: <https://github.com/micromdm/mdmutil> · Go · MIT · Active (2026-09) · 4 stars
- Category: Go MDM library, push certificate tooling
- Notes: New in 2026. A `mdmutil` CLI with packages `mdmcsr` (generate and vendor-sign the Apple
  MDM CSR plist for the Push Certificates Portal) and `passwd` (SALTED-SHA512-PBKDF2 hashes for
  `AccountConfiguration` and `SetAutoAdminPassword`). Supersedes the archived `jessepeterson/mdmpasswd`.
  The maintained Go implementation of the vendor signing step.

### cfgprofiles
- Repo: <https://github.com/jessepeterson/cfgprofiles> · Go · Unlicense · Active (2025-01) · 9 stars
- Category: Profiles
- Notes: Hand-written Go structs for `.mobileconfig` profiles and common payloads (certificate,
  SCEP, MDM) with marshal and unmarshal via micromdm/plist. Used by MicroMDM to build enrollment
  profiles. go-adm's `profilegen` is the generated alternative.

### micro2nano
- Repo: <https://github.com/micromdm/micro2nano> · Go · MIT · Active (2026-08) · 2 stars
- Category: Migration
- Notes: MicroMDM to NanoMDM enrollment migration. Useful for seeing the minimal enrollment
  state an MDM must persist: topic, push token, push magic, identity certificate.

### 5.3 plist

### micromdm/plist (formerly groob/plist)
- Repo: <https://github.com/micromdm/plist> · Go · BSD-style (GitHub reports NOASSERTION) · Active (2026-08) · 63 stars
- Notes: XML and binary plist encoder and decoder derived from DHowett/go-plist with an
  `encoding/json` style API and streaming decoders. This is the plist library used by nanomdm,
  micromdm, nanocmd, cfgprofiles, and Fleet (v0.3.0). `groob/plist` redirects here. 53 importers.

### DHowett/go-plist (howett.net/plist)
- Repo: <https://github.com/DHowett/go-plist> · Go · BSD-2-Clause-Views · Active (2026-08) · 468 stars
- Notes: The most widely used pure Go plist transcoder (543 importers) supporting XML, binary,
  OpenStep and GNUStep. Used by Fleet alongside micromdm/plist; not used by the Nano projects.

### 5.4 PKCS7 and CMS

### smallstep/pkcs7
- Repo: <https://github.com/smallstep/pkcs7> · Go · MIT · Active (2026-07, v0.2.3) · 25 stars
- Notes: The maintained fork of fullsailor/pkcs7 by way of the Mozilla fork. Used by nanomdm,
  micromdm, nanocmd, nanodep, and Fleet for verifying the `Mdm-Signature` header, DEP profile
  handling, and SCEP envelopes. Recommended for any new Go MDM library.

### mozilla-services/pkcs7 (go.mozilla.org/pkcs7)
- Repo: <https://github.com/mozilla-services/pkcs7> · Go · MIT · Active but self-described deprecated (2026-07, v0.10.0) · 73 stars
- Notes: Repository description reads "DEPRECATED"; still has 154 importers. Only an indirect
  dependency in Fleet. Historical for new work.

### fullsailor/pkcs7
- Repo: <https://github.com/fullsailor/pkcs7> · Go · MIT · Historical (2024-01, last release 2019) · 127 stars
- Notes: The original library and ancestor of both forks. Effectively unmaintained.

### 5.5 APNs and push certificates

### nanomdm push packages
- Repo: <https://github.com/micromdm/nanomdm> (`push/nanopush`, `push/buford`, `mdm/push.go`)
- Notes: `nanopush` is a self-contained MDM-specific APNs HTTP/2 client that imports only
  `net/http`, `crypto/tls`, and `golang.org/x/net/http2`. The best reference for the minimal MDM
  push flow.

### RobotsAndPencils/buford
- Repo: <https://github.com/RobotsAndPencils/buford> · Go · MIT · Historical, archived (2023-02) · 473 stars
- Notes: HTTP/2 APNs library that is still a direct dependency of micromdm, nanomdm, and Fleet.
  Archived, so treat as a risk for new code.

### sideshow/apns2
- Repo: <https://github.com/sideshow/apns2> · Go · MIT · Active (2025-07, v0.25.0 in 2024-10) · 3,187 stars
- Notes: The most popular general Go APNs HTTP/2 client with certificate and token auth. Not used
  by any surveyed MDM server, but a maintained alternative to buford.

### Push certificate CSR tooling
- <https://github.com/grinich/mdmvendorsign> · Python · MIT · Maintenance (2021-02) · 160 stars. The
  original script that signs a customer push CSR with an MDM vendor certificate. Widely copied.
- <https://github.com/micromdm/mdmutil> (see 5.2) is the maintained Go equivalent (`mdmcsr-sign`).
- <https://github.com/korylprince/fleetapns> · Go · MIT · Maintenance (2024-02) · 4 stars. Submits a push
  CSR to Fleet's free vendor signing service, replacing `mdmctl mdmcert.download`. Useful for the
  CSR request shape.
- <https://github.com/petarov/apns-push-cmd> · Go · MIT · Active (2026-08) · 5 stars. CLI for firing
  pushes at devices when debugging a server.
- `mdmcert.download` is a closed-source web service with no public repository; MicroMDM's
  `mdmctl mdmcert.download` is its client.

### 5.6 SCEP, ACME, and PKI

### smallstep/scep
- Repo: <https://github.com/smallstep/scep> · Go · MIT · Active (2026-08) · 48 stars
- Notes: The maintained SCEP protocol library (PKIMessage parse, decrypt, sign, client and server
  helpers) extracted from micromdm/scep. 22 importers including micromdm/scep, Fleet, and step-ca.
  Use this for an embedded SCEP endpoint.

### micromdm/scep
- Repo: <https://github.com/micromdm/scep> · Go · MIT · Active (2026-01, v2.3.0) · 390 stars
- Notes: SCEP server, client, and `depot` library (Bolt or file CA, `-csrverifierexec` hook).
  Since v2 the protocol code lives in smallstep/scep. README says the server is basic and unlikely
  to be supported in future, pointing to step-ca. Still bundled in MicroMDM.

### smallstep/certificates (step-ca)
- Repo: <https://github.com/smallstep/certificates> · Go · Apache-2.0 · Active (2026-08) · 8,799 stars
- Notes: Production CA with a SCEP provisioner (requires an RSA intermediate) and an ACME
  provisioner supporting `device-attest-01` for Managed Device Attestation. Docs warn it trusts any
  Apple device without extra policy. The main open source ACME-for-attestation implementation.

### nanoca
- Repo: <https://github.com/brandonweeks/nanoca> · Go · MIT · Active (2026-08) · 34 stars
- Notes: A lightweight ACME CA with device attestation support that provides only the HTTP
  handlers, intended to be integrated into NanoMDM or another service. Storage, signing,
  authorisation, and logging are pluggable interfaces. Directly relevant as an embeddable ACME
  design for a Go MDM library.

### mysqlscepserver
- Repo: <https://github.com/jessepeterson/mysqlscepserver> · Go · MIT · Active (2026-09) · 6 stars
- Notes: Small SQL-backed SCEP server on the smallstep SCEP libraries; the Nano author's
  replacement for the micromdm/scep server.

### ios-acme-simulator
- Repo: <https://github.com/hslatman/ios-acme-simulator> · Go · No license · Experimental (2025-08) · 6 stars
- Notes: Simulates an iOS device doing `device-attest-01` with fake Apple attestation certificates
  against step-ca. Useful for understanding the attestation ACME flow.

### Non-Go SCEP (for interop testing)
- <https://github.com/certnanny/sscep> · C · Maintenance (2024-08) · 198 stars. Classic SCEP CLI client.
- <https://github.com/mosen/SCEPy> · Python · Historical (2018-07) · 31 stars. Python SCEP server.
- Full CAs with SCEP endpoints: <https://github.com/openxpki/openxpki> (Perl, Apache-2.0),
  <https://github.com/Keyfactor/ejbca-ce> (Java, LGPL-2.1), <https://github.com/dogtagpki/pki> (Java, GPL-2.0).

### 5.7 DEP, ADE, and Apple Business Manager

### NanoDEP
- Repo: <https://github.com/micromdm/nanodep> · Go · MIT · Active (2026-08) · 44 stars
- Notes: DEP token PKI, OAuth 1 session handling, a transparently authenticating reverse proxy
  (`depserver`), `depsyncer` for the device sync cursor, `deptokens`, and shell tools. Go libraries
  `godep` (endpoints) and `client` (auth). Storage: `file`, `diskv`, `inmem`, `kv`, `mysql`,
  `pgsql`. Successor to `micromdm/dep`; vendored into Fleet.

### NanoAXM
- Repo: <https://github.com/micromdm/nanoaxm> · Go · MIT · Active (2026-08) · 4 stars
- Notes: Same shape as NanoDEP for the newer OAuth 2 Apple School and Business Manager API:
  config and reverse-proxy server plus `goaxm` and `client` libraries.

### dep-webview-oidc
- Repo: <https://github.com/korylprince/dep-webview-oidc> · Go · MIT · Active (2025-07) · 6 stars
- Notes: Library and server implementing the DEP `configuration_web_url` web-view authentication
  flow with OIDC. The only open source Go implementation of that flow found.

### Apple-JSON-discovery-server
- Repo: <https://github.com/vbnin/Apple-JSON-discovery-server> · Apache config and JSON · MIT · Maintenance (2024-07) · 17 stars
- Notes: A minimal `/.well-known/com.apple.remotemanagement` service discovery endpoint routing
  Macs to account-driven Device Enrollment and iPhones to account-driven User Enrollment. The
  clearest reference for the discovery step.

### Other ABM/ASM clients
- <https://github.com/hitoshiichikawa/apple-business-go> · Go · MIT · Active (2026-08) · 0 stars. Hand-written
  ABM/ASM SDK with ES256 JWT auth and pagination. New and unproven.
- <https://github.com/neilmartin83/terraform-provider-axm> · Go · MPL-2.0 · Active (2026-08) · 25 stars.
  Terraform provider with its own Go client for the ABM/ASM API.
- <https://github.com/petarov/apple-mdm-clients> · Java 21 · Apache-2.0 · Active (2026-08) · 2 stars. DEP
  device assignment and Apps and Books clients; the only maintained non-Go DEP client found.

### 5.8 Configuration profiles

### ProfileManifests
- Repo: <https://github.com/ProfileManifests/ProfileManifests> · Manifest data · No license file · Active (2026-09) · 526 stars
- Notes: Community preference manifests describing payload keys for OS and third-party domains.
  Consumed by ProfileCreator, iMazing Profile Editor (closed source), and Jamf's
  `ProfileManifestsMirror` JSON schema mirror. A good validation and UI data source for a profile
  builder.

### ProfileCreator
- Repo: <https://github.com/ProfileCreator/ProfileCreator> · Swift · MIT · Maintenance (2025-01) · 1,505 stars
- Notes: macOS GUI for authoring profiles from ProfileManifests. Low activity now.

### mobileconfig-signer
- Repo: <https://github.com/hslatman/mobileconfig-signer> · Go · No license · Maintenance (2023-10) · 3 stars
- Notes: Example CLI that CMS-signs a `.mobileconfig`. Small but a direct reference for profile
  signing in Go. Unlicensed, so read-only reference.

### Related
- `micromdm/nanocmd` contains `utils/mobileconfig`, a Go profile parser (see 3.1).
- <https://github.com/deploymenttheory/go-settings-catalog> · Go · MIT · Active (2026-08). Same organisation
  as this repository; contains a mobileconfig-to-Terraform converter.

---

## 6. Clients, simulators, and test tooling

### mdmb
- Repo: <https://github.com/jessepeterson/mdmb> · Go · MIT · Active (2026-03) · 92 stars
- Implements: DDM client: no. MDM v1 client: Profile enrollment (installs the enrollment profile including the SCEP payload), Check-in (Authenticate and TokenUpdate with fake push token and magic), Commands (polling and responses)
- Notes: Simulates many fake devices with a keychain and profile store. Subcommands
  `devices-create`, `devices-profiles-install`, `devices-connect`, `devices-list`. The standard
  functional and load test client for NanoMDM and MicroMDM. No APNs, no ADE, no DDM. Note the
  owner is `jessepeterson`, not `micromdm`.

### Fleet test client and simulators
- Repo: <https://github.com/fleetdm/fleet> · Go · MIT (outside `ee/`) · Active (2026-09)
- Implements: MDM v1 client, DDM client, APNs mock, DEP and ABM tooling
- Notes:
  - `pkg/mdm/mdmtest/apple.go`: `TestAppleMDMClient` runs SCEP or ACME enrollment, Authenticate,
    TokenUpdate, re-enrollment, user channel enrollment, DEP, OTA, BYOD and manual profile flows,
    and has `DeclarativeManagement()` and `UserDeclarativeManagement()` helpers. `psso.go` covers
    Platform SSO and `scep_exchange.go` the SCEP handshake.
  - `cmd/osquery-perf`: simulates enrolled macOS, iOS, and iPadOS hosts with MDM, user channel,
    BYOD, and PSSO probabilities, `NotNow` and error injection, and in `ddm.go` a DDM client that
    fetches tokens, declaration-items, and declarations and posts status reports. This is the only
    open DDM client simulator found.
  - `cmd/apple-apns-mock` plus `pkg/mdm/apnsmock`: a Redis-coordinated APNs mock that pushes to
    simulated devices over server-sent events at very high connection counts.
  - `tools/mdm/apple/`: `apnspush`, `applebmapi`, `appmanifest`, `loadtest`,
    `macos-vm-auto-enroll`, `setupexperience`, `throttle.go`, and troubleshooting notes.
  - Tightly coupled to Fleet's API, but the protocol pieces are readable in isolation.

### Local development harnesses
- <https://github.com/sheshenia/nanostarter> · Go · MIT · Historical (2022-07) · 13 stars. Starts nanomdm,
  scep, and ngrok, generates `Enroll.mobileconfig`, and shows the enrolled device.
- <https://github.com/discentem/nanomdmsandbox> · HCL · MIT · Maintenance (2023-02) · 14 stars. Terraform
  sandbox on ECS for nanomdm and scep with RDS MySQL.
- <https://github.com/korylprince/kmfddm-docker> · Shell · No license · Historical (2023-07). KMFDDM compose.
- Docker compose stacks from `openmac-org` are listed in section 4.

---

## 7. Historical and archived projects

Worth reading for protocol behaviour that newer code assumes, but not for reuse.

### imdmtools (Intrepidus Group)
- Repo: <https://github.com/intrepidusgroup/imdmtools> · Python 2.7 · No license · Historical (2022-05) · 188 stars
- Implements: Check-in, Commands, APNs push, Profile enrollment, vendor CSR signing
- Notes: The Black Hat 2011 iOS MDM research server that seeded nearly every "sample iOS MDM
  server" on GitHub. `project-imas/mdm-server` (Python, no license, 2015-12, 606 stars) and the
  archived `macadmins/mdm-server` are forks of it, not independent implementations.

### Apple-iOS-MDM-Server
- Repo: <https://github.com/vineetchoudhary/Apple-iOS-MDM-Server> · Python · MIT · Historical, archived (2022-03) · 78 stars
- Notes: Same lineage as imdmtools with a tutorial on creating a verified MDM profile.

### IOS-MDM-Server (Java)
- Repo: <https://github.com/zuoyy/IOS-MDM-Server> · Java (Spring MVC) · No license · Historical (2015-03) · 51 stars
- Notes: The only Java protocol implementation found. Dormant for eleven years.

### activeMDM
- Repo: <https://github.com/abstractec/activeMDM> · PHP · Apache-2.0 · Historical (2015-04) · 17 stars
- Notes: Early abandoned PHP attempt covering enrollment, push, and device lock.

### WSO2 IoT Server
- Repo: <https://github.com/wso2/product-iots> · Java · Apache-2.0 · Historical (2023-12) · 199 stars
- Notes: `wso2/product-emm` no longer exists, and the open plugin repository contains only Android
  and Windows plugins. The iOS MDM plugin was never published, so this is not a usable Apple MDM
  reference.

### MicroMDM precursors
- <https://github.com/micromdm/mdm> (archived 2018, 22 stars): hand-written command structs, superseded by mdmcommands.
- <https://github.com/micromdm/dep> (archived 2018, 9 stars): original DEP client, superseded by NanoDEP.
- <https://github.com/micromdm/tools> (archived 2017, 22 stars): `poke` push tool, `appmanifest`, `certhelper`.
- `micromdm/checkin`, `micromdm/command`: 2016 to 2018 archived precursors.

### Others
- <https://github.com/emersion/go-apple-mobileconfig> · Go · MIT · Archived (2016-07). Minimal mail account profile generator.
- <https://github.com/nolanbrown/ios-cert-enrollment> · Ruby · MIT · Archived (2013-11). Early SCEP enrollment example.
- <https://github.com/korylprince/macos-device-attestation> · Go · MIT · Historical (2022-07). Token-based "prove you are root on device X" service via MicroMDM. Not Apple Managed Device Attestation despite the name.

---

## 8. Summary comparison matrix

Full servers and DDM implementations only. Y means implemented in the project itself, P means
proxied to a separate service, blank means absent.

| Project | Lang | License | Status | Check-in | Commands | APNs | Profile enroll | DEP/ADE | User / account-driven | DDM server | DDM client | SCEP | ACME | Library API |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| NanoMDM | Go | MIT | Active | Y | Y | Y |  |  | Y | P |  |  |  | Y |
| NanoHUB | Go | MIT | Active | Y | Y | Y |  |  | Y | Y |  |  |  | Y |
| KMFDDM | Go | MIT | Active |  |  |  |  |  |  | Y |  |  |  | Y |
| Fleet | Go | MIT + EE | Active | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |  |
| MicroMDM | Go | MIT | Maintenance | Y | Y | Y | Y | Y |  | P |  | Y |  | Partial |
| MDMDirector | Go | Apache-2.0 | Active |  | Y (via MicroMDM) |  |  |  |  |  |  |  |  |  |
| NanoCMD | Go | MIT | Active |  | Y (workflows) |  |  |  |  |  |  |  |  | Y |
| Cairn-MDM | Go | MIT | Experimental | Y | Y | Y | Y |  |  |  |  | Y |  |  |
| Local MDM | Go | MIT | Experimental | Y | Y | Y | Y | Y |  |  |  | Y |  |  |
| VEx | Go | none | Experimental | Y |  | Y |  |  |  | Y |  |  |  |  |
| Zentral | Python | Apache-2.0 + EE | Active | Y | Y | Y | Y | Y | Y | Y |  | Y | Y |  |
| Commandment | Python | MIT | Historical | Y | Y | Y | Y | Y |  |  |  | Y |  |  |
| Micromanage | Python | none | Experimental |  |  |  | Y | Y |  | Y |  | Y |  |  |
| oreore-ios-mdm | Ruby | MIT | Experimental | Y | Y | Y | Y |  |  | Y |  |  |  |  |
| apple-mdm-poc | Rust | none | Experimental | Y | Y | stub | Y |  |  |  |  | stub |  | Y |
| TDS MDM | PHP/JS | none | Experimental |  |  |  |  |  |  | Y (via MicroMDM) |  |  |  |  |
| go-adm | Go | MIT | Active | codegen | codegen |  | codegen |  |  | codegen |  |  |  | Y |
| mdmcommands | Go | Unlicense | Active |  | codegen |  |  |  |  |  |  |  |  | Y |
| Contour | Rust | Apache-2.0 | Active |  |  |  | validate |  |  | validate |  |  |  | Y |
| mdmb | Go | MIT | Active | client | client |  | client |  |  |  |  | client |  |  |
| Fleet osquery-perf and mdmtest | Go | MIT | Active | client | client | mock | client | client | client |  | Y | client | client |  |

---

## 9. Observations for go-apple-mdm

Brief pointers, not a design. Each item names the best code reference for a protocol area.

- **Check-in and command handling.** NanoMDM's `service` and `mdm` packages are the cleanest
  reading of the protocol in Go, with the storage interface pattern that every derivative reuses.
  Fleet's vendored copy shows what a large deployment needed to change.
- **Typed commands and declarations.** Two generators exist over `apple/device-management`:
  `mdmcommands` (commands only, zero dependencies, consumed by NanoCMD) and `go-adm` (commands,
  profiles, declarations, and status). A library that wants DDM types has go-adm and the sibling
  `go-sdk-appleservices` as references; nobody else publishes a DDM type library in Go.
- **DDM server model.** KMFDDM is the reference for the `ServerToken` and declaration-items token
  model and for status report storage. Fleet's DDM architecture document and its migration
  history show how the model evolves in production (variables, assets, custom activations).
  Zentral shows `LegacyProfile` wrapping and status-to-inventory mapping.
- **MDM to DDM handoff.** NanoMDM's `-dm` HTTP proxy with HMAC is the established contract
  between an MDM core and a DDM service. NanoHUB's `ddmadapter` shows the in-process version.
- **Ecosystem dependencies that are already settled.** `micromdm/plist` for plist,
  `smallstep/pkcs7` for signature verification, `smallstep/scep` for SCEP, `nanolib` for daemon
  plumbing. APNs is the one unsettled area: the shared dependency `buford` is archived, NanoMDM
  ships its own `nanopush`, and `sideshow/apns2` is the maintained general client.
- **Push certificate workflow.** `mdmutil` (Go) and `mdmvendorsign` (Python) cover vendor CSR
  signing; `fleetapns` shows the request shape for Fleet's free signing service.
- **Enrollment.** NanoDEP for DEP, NanoAXM for the newer ABM API, `dep-webview-oidc` for ADE
  web-view auth, and the `Apple-JSON-discovery-server` for account-driven service discovery.
  Fleet's `mdmtest` client exercises every enrollment flow end to end.
- **ACME and attestation.** `nanoca` is the embeddable ACME handler set designed for NanoMDM;
  step-ca is the full CA. `ios-acme-simulator` and Fleet's test client cover the client side.
- **Testing.** `mdmb` for MDM v1 device simulation; Fleet's `osquery-perf` for DDM client
  simulation and its APNs mock for push. No standalone DDM client simulator exists.
- **Gaps in the ecosystem.** No Go library exposes a complete, importable DDM server API with
  pluggable storage other than KMFDDM. No Swift, Python, or TypeScript type library is generated
  from the DDM YAML. Account-driven enrollment server flows exist only inside full products
  (NanoMDM, Fleet, Zentral). The legacy software update commands are removed in OS 27, so any new
  library should treat DDM software update declarations as the primary path.

---

## 10. Not covered

Commercial and closed source MDMs (Jamf Pro, Kandji, Mosyle, Addigy, Intune, Workspace ONE,
Miradore, Relution, Hexnode, Meraki) are excluded because their protocol code is not readable.
`Jamf-Concepts/ddm-explorer` and iMazing Profile Editor are mentioned above only as tools.

Verified and rejected during research: `myMDM` (advertised AGPL repo returns 404),
`OliverForral/mdm-simulator` (a .NET template with no MDM code), Flyve MDM (archived, Android
agent only, no Apple protocol code), `multunus/onemdm-server` (Android only), and several
master-data-management repositories that match the "MDM" keyword.

Corrections to commonly cited names: `mdmb` is under `jessepeterson`, not `micromdm`; Zentral is
under `zentralopensource`; Commandment is under `cmdmnt`; KMFDDM is under `jessepeterson`;
`project-imas/mdm-server` is a fork of `intrepidusgroup/imdmtools`.
