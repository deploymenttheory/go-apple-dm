# Companion diagram specifications

Reviewed revision: `ff85958196cb013e8e14c1055f067319b07e3299`. These four specifications provide complete authored node, relationship, card, and branch text. They are semantic proposals, not rendered Archify candidates. Preserve existing diagram basenames and add these companions; cross-link them from related diagrams and the catalogue.

Use fresh stable IDs below, English viewer UI, classic preset, static motion, and showcase quality. Omit a subtitle and guided views for the initial version: the main composition must explain the flow. Sequence relationship order is the illustrated reading order, with concurrency and alternatives defined in the topology notes. Workflow lanes express the two distinct credential paths; they do not add a new chronological dependency.

## Controlled Enrollment-profile Replacement

Proposed basename: `flow-enrollment-profile-replacement`; type: `sequence`. Explain how an enabled device changes its enrollment identity without resetting its working management state.

Evidence: [server/internal/app/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/replacement.go#L284), [server/internal/app/adminbench.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/adminbench.go#L1), [server/service/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/service/replacement.go#L40), [storage/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/replacement.go#L72), [storage/inmem/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/inmem/replacement.go#L1), [server/sqlstore/sqlcommon/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/sqlstore/sqlcommon/replacement.go#L1), [storage/storagetest/replacement.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/storage/storagetest/replacement.go#L17), [server/internal/dmctl/benchverbs.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/dmctl/benchverbs.go#L1).

### Participants or nodes

| ID | Label | Description |
|---|---|---|
| operator | Authorized operator | Requests replacement and a device wake |
| admin | Enrollment administration | Prepare an authorized replacement attempt |
| store | Enrollment and replacement store | Keep active and candidate state separate |
| device | Enrolled Apple device | Uses the working identity to fetch the profile |
| issuer | SCEP or ACME issuer | Issue the authorized candidate identity |
| core | MDM service | Intercept replacement check-in and command traffic |

### Relationships in reading order

| ID | From → to | Exact label |
|---|---|---|
| prepare | operator → admin | POST the enrollment-scoped replacement action |
| read | admin → store | Read enabled enrollment and original profile |
| begin | admin → store | Begin a 30-minute replacement attempt |
| prepared | admin → operator | Return redacted attempt metadata |
| wake | operator → device | Request APNs wake through the MDM notifier |
| poll | device → core | Poll with the current enrollment identity |
| deliver | core → store | Mark replacement InstallProfile as delivered |
| profile | core → device | Return InstallProfile with fresh identity authorization |
| issue | device → issuer | Complete authorized SCEP or ACME issuance |
| register | issuer → store | Record the candidate certificate fingerprint |
| identity | issuer → device | Return candidate identity certificate |
| authenticate | device → core | Authenticate with the candidate identity |
| stageauth | core → store | Record successful candidate authentication |
| token | device → core | TokenUpdate with candidate identity and push details |
| stagetoken | core → store | Stage the candidate device token |
| ack | device → core | Acknowledge the delivered InstallProfile command |
| result | core → store | Record acknowledgement and evaluate completion |
| commit | store → store | Atomically promote candidate identity and tokens |
| complete | core → device | Return an empty successful response |

### Branches and interpretation

- Order participants by communication role, and group messages into Prepare, Deliver, Candidate check-in, and Commit. The drawn order is one valid success ordering, not a protocol requirement that acknowledgement comes last.
- The wake edge is a collapsed external path: operator/bench → push notifier → APNs → device. The replacement HTTP action alone does not send that wake.
- Alternative branch after preparation: operator → admin “Cancel pending attempt”; admin → store “Mark cancelled”. Alternative branch on command result: core → store “Record failed attempt”. Expiry is evaluated on store transitions, including reads; do not invent an expiry worker.
- Only the old identity may receive the credential-bearing InstallProfile. Candidate traffic is constrained; do not imply candidates can fetch ordinary queued commands.

### Explanatory cards

**Before starting**

- The device must be enabled and have a recorded original profile with profile-installation rights. The replacement must keep the management endpoint and MDM topic.
- The admin action requires replaceEnrollmentProfile authority. Preparation stores the replacement command outside the ordinary command queue.

**Commit conditions**

- Promotion requires candidate Authenticate, a valid device-channel TokenUpdate, and acknowledgement of the delivered InstallProfile command. Acknowledgement may arrive before or after candidate check-in.
- The original top-level profile and MDM payload metadata are retained; the identity payload receives a fresh UUID and issuance authorization. A replacement can select SCEP or ACME.

**Unsuccessful attempts**

- Error or CommandFormatError fails the attempt; cancellation and expiry also end it without promoting the candidate. NotNow leaves the attempt pending.
- The server keeps its working enrollment, queue, escrow, and user channels. This is server-side preservation; real-device rollback remains unverified in PR 14.

**Public documentation**

- Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
- MDM payload — https://developer.apple.com/documentation/devicemanagement/mdm
- ACMECertificate — https://developer.apple.com/documentation/devicemanagement/acmecertificate
- SCEP — https://developer.apple.com/documentation/devicemanagement/scep

Public links: [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm), [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate), [SCEP](https://developer.apple.com/documentation/devicemanagement/scep).

## APNs Credentials: MDM and App Certificate Workflows

Proposed basename: `apns-certificate-workflows`; type: `workflow`. Explain why app push certificates cannot replace MDM push credentials and where vendor signing belongs.

Evidence: [pki/pushcert/csr.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/pushcert/csr.go#L1), [pki/pushcert/inspect.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/pushcert/inspect.go#L1), [pki/pushcert/pushcert.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/pki/pushcert/pushcert.go#L1), [server/internal/dmctl/apnsverbs.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/dmctl/apnsverbs.go#L1), [server/internal/app/push.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/push.go#L1), [server/internal/app/apppush.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/apppush.go#L141), [server/apppush/store.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/apppush/store.go#L68).

### Participants or nodes

| ID | Label | Description |
|---|---|---|
| mdm-key | Create customer MDM key and CSR | Keep the matching private key |
| vendor | Sign the MDM CSR request | Use the MDM vendor signing identity |
| portal | Apple Push Certificates Portal | Customer obtains the MDM push certificate |
| mdm-import | Validate and import MDM credential | Certificate, key, validity, and MDM topic |
| mdm-store | MDM push certificate store | Versioned credential selected by MDM topic |
| app-key | App provider certificate and key | Obtain through Apple Developer provisioning |
| app-import | Validate app provider credential | Confirm certificate type, key, topic, and validity |
| app-store | Encrypted app credential store | Separate apppush/v1/ state namespace |
| clients | Retire old APNs connections | Use the committed replacement credential |

### Relationships in reading order

| ID | From → to | Exact label |
|---|---|---|
| m1 | mdm-key → vendor | Submit customer CSR for vendor signing |
| m2 | vendor → portal | Upload the signed CSR request |
| m3 | portal → mdm-import | Import issued MDM certificate with matching key |
| m4 | mdm-import → mdm-store | Commit validated credential and version |
| m5 | mdm-store → clients | On credential reload, retire the old MDM connection |
| a1 | app-key → app-import | Import certificate and matching private key |
| a2 | app-import → app-store | Encrypt and commit the app credential |
| a3 | app-store → clients | Retire app connections for this topic |

### Branches and interpretation

- Use two independent main lanes, MDM and App; each is a complete path and they never converge on one credential store. Connection lifecycle is a shared explanatory endpoint.
- Portal issuance and Developer provisioning are external operator actions. The repository supplies CSR/sign/import tooling, not automated access to Apple account portals.
- Use the existing MDM push diagram and the app delivery companion for subsequent sends. Do not add a device identity CA to either APNs credential path.

### Explanatory cards

**Different credentials**

- MDM customer certificates authorize MDM wake notifications. App provider certificates authorize app notifications for their permitted topics.
- The MDM vendor signing identity signs customer CSR requests; it is not the credential used to send customer MDM pushes.

**Validation and replacement**

- Inspecting a certificate does not prove possession of its private key. Import and send validation require a matching certificate/key pair.
- Invalid imports leave the existing credential in place. Successful replacement retires cached connections, and shutdown closes clients.

**Persistent storage**

- App push credentials require encryption in persistent state. They are separate from the MDM certificate table and its key-rewrap path.

**Public documentation**

- Setting up push notifications for your device management customers — https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
- Establishing a certificate-based connection to APNs — https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns

Public links: [Setting up push notifications for your device management customers](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers), [Establishing a certificate-based connection to APNs](https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns).

## App Notifications: APNs Acceptance and Receipt Evidence

Proposed basename: `flow-app-notification-delivery`; type: `sequence`. Show the server-managed app notification API and the separate live-bench receipt check.

Evidence: [server/internal/app/apppush.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/app/apppush.go#L141), [server/apppush/store.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/apppush/store.go#L68), [appleplatformservices/push/apns/app.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/push/apns/app.go#L1), [appleplatformservices/push/apns/lifecycle.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/appleplatformservices/push/apns/lifecycle.go#L1), [server/internal/bench/live.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/live.go#L34).

### Participants or nodes

| ID | Label | Description |
|---|---|---|
| caller | Authorized caller or live bench | Supplies the app registration and payload |
| api | App push administration | Authorize and validate the send request |
| store | App credential store | Load the certificate for the app topic |
| client | App APNs client | Select explicit development or production host |
| apns | Apple Push Notification service | Accept or reject the provider request |
| app | Registered app on the device | Receives the app notification |
| evidence | Live bench evidence | Match receipt to the attempted notification |

### Relationships in reading order

| ID | From → to | Exact label |
|---|---|---|
| send | caller → api | POST /admin/v1/apppush/send |
| select | api → client | Select explicit environment and validate payload |
| credential | client → store | Load app credential by topic |
| cert | store → client | Return validated certificate and private key |
| post | client → apns | POST payload to the registered device token |
| accepted | apns → client | Return status, reason, and APNs identifier |
| result | client → api | Report APNs send outcome |
| response | api → caller | Return Accepted, Outcome, Status, Reason, APNSID |
| delivery | apns → app | Asynchronously deliver if permitted by the device |
| receipt | app → evidence | Record labCorrelationID, topic, and environment |
| verify | caller → evidence | Wait for the matching receipt in a live scenario |

### Branches and interpretation

- Make the APNs→app arrow asynchronous. Its position is illustrative; delivery and receipt collection are not causally ordered after the admin response.
- Draw the receipt path as a separate bench-only group. Use no MDM push certificate, PushMagic, or command queue in this flow.
- Use an alternative failure note at the provider response: “Rejected request: report APNs reason; no acceptance claim”. Avoid asserting that every failure is retryable.

### Explanatory cards

**Required send inputs**

- Supply the registered app token, app topic, explicit environment, push type, and aps payload. This implementation supports alert and background pushes.
- The app client validates payload shape and APNs request constraints before sending. These credentials never pass through the MDM wake notifier.

**Two different success signals**

- Accepted reports provider acceptance by APNs. It does not establish device delivery or app processing.
- The live bench separately requires a receipt matching labCorrelationID, topic, and environment; its current receipt wait is bounded to 30 seconds.

**Failure outcomes**

- Invalid input is rejected locally. APNs may reject the request or a transport error may prevent submission.
- Missing receipt evidence leaves live delivery unverified even after acceptance. The receipt mechanism is test-lab behaviour, not a general server delivery guarantee.

**Public documentation**

- Sending notification requests to APNs — https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
- Establishing a certificate-based connection to APNs — https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns

Public links: [Sending notification requests to APNs](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns), [Establishing a certificate-based connection to APNs](https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns).

## Shared Reference-server Bench and Validation Evidence

Proposed basename: `reference-server-bench`; type: `architecture`. Explain which server implementation is exercised, who controls fixtures, and what simulated and live success establish.

Evidence: [server/internal/bench/environment.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/environment.go#L1), [server/internal/bench/catalog.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/catalog.go#L1), [server/internal/bench/workspace.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/workspace.go#L1), [server/internal/bench/live.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/bench/live.go#L34), [server/internal/runtime/runtime.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/runtime/runtime.go#L35), [server/acceptance/scenarios_test.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/acceptance/scenarios_test.go#L1), [server/e2e/harness_test.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/e2e/harness_test.go#L1), [server/internal/dmctl/benchverbs.go](https://github.com/deploymenttheory/go-apple-dm/blob/ff85958196cb013e8e14c1055f067319b07e3299/server/internal/dmctl/benchverbs.go#L1).

### Participants or nodes

| ID | Label | Description |
|---|---|---|
| cli | dmctl bench | Initialize, supervise, run, and inspect |
| workspace | Bench workspace | Configuration, identities, and evidence files |
| supervisor | Bench supervisor | Start servers, await readiness, and stop them |
| runtime | Shared server runtime | Serve HTTP/TLS and drain workers |
| server | Reference-server application | Configured roles and normal admin APIs |
| catalogue | Shared scenario catalogue | Defines scenario IDs and supported modes |
| simulator | Simulated device and fixtures | Local protocol exchanges and fake services |
| process | Process acceptance runner | Exercises built dmserver and CLI processes |
| live | Real app or enrolled device | Uses real credentials and registration |
| evidence | Validation evidence | Protocol outcomes, command results, receipts |
| contracts | Backend contract suites | Verify persistence independently of scenarios |

### Relationships in reading order

| ID | From → to | Exact label |
|---|---|---|
| b1 | cli → workspace | Read or initialize the selected workspace |
| b2 | cli → supervisor | Start and stop the bench |
| b3 | supervisor → runtime | Launch configured embedded or process runtime |
| b4 | runtime → server | Build and serve the reference application |
| b5 | cli → catalogue | Select a supported scenario and mode |
| b6 | catalogue → simulator | Drive simulated protocol exchanges |
| b7 | simulator → server | Use device endpoints and normal admin APIs |
| b8 | process → supervisor | Run shared scenarios against built processes |
| b9 | catalogue → live | Drive supported live app or MDM checks |
| b10 | live → server | Use real enrollment or app push flows |
| b11 | server → evidence | Expose enrollment evidence and command results |
| b12 | simulator → evidence | Record simulated protocol outcomes |
| b13 | live → evidence | Supply correlated receipt or device evidence |
| b14 | contracts → evidence | Report backend contract results |

### Branches and interpretation

- Use three boundaries: operator/workspace, reference-server runtime, and test drivers/evidence. Fixtures belong outside the production administration boundary.
- Label embedded versus process execution on b3. The architecture should not imply the CLI runs in-process inside the server.
- Do not hard-code scenario totals in the diagram: the catalogue is the authoritative inventory. Do not imply live tests run in ordinary CI.

### Explanatory cards

**One maintained runtime**

- Bench scenarios and dmserver share the reference-server runtime. Process acceptance tests built executables, while retained detailed regressions also inspect lower-level composition.
- The supervisor waits for readiness and stops runtimes before closing local fixtures.

**Simulated and live modes**

- Simulated scenarios use synthetic identities, a device simulator, and local service fixtures. Fixture controllers stay outside production administration.
- Live scenarios need real credentials, app registration or an enrolled device, and correlated outcome evidence. Simulated success does not establish Apple hardware compatibility.

**Independent checks**

- Storage contract suites verify persistence across supported backends. The executable catalogue and bench-docs-check keep scenario documentation aligned.
- Coverage policy and live validation status are separate from the existence of a scenario. PR 14 records outstanding real-device verification.

**Public documentation**

- Go command: Test packages — https://pkg.go.dev/cmd/go#hdr-Test_packages
- Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
- Sending notification requests to APNs — https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns

Public links: [Go command: Test packages](https://pkg.go.dev/cmd/go#hdr-Test_packages), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Sending notification requests to APNs](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns).
