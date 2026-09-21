# Build your own Apple device management server

[Getting started](getting-started.md) · [Run the reference server](reference-server.md)

Use this path when your application needs to own enrollment policy, device
inventory, authorization and operations. The packages supply protocol building
blocks and reusable server components. They do not create a fleet management
product merely by being imported.

Work through the steps in order. First produce a valid command offline. Then
compose storage, device identity verification and MDM handling. Add enrollment,
push, administration and DDM only when you can explain who owns each part.
The short examples are complete Go programs; they deliberately stop before
exposing a network service.

## 1. Choose the modules you need

| Module | What it contains | Add it when |
|---|---|---|
| `github.com/deploymenttheory/go-apple-dm` | `devicemanagement/` protocol/schema types, enrollment/PKI, Apple clients, storage interfaces and memory stores | You need typed Apple messages or reusable protocol components |
| `github.com/deploymenttheory/go-apple-dm/server` | Public `service`, `httpapi`, SQL stores, admin authorization and DDM/push adapters | You want the reference implementation's reusable server behavior |

`server/internal/app`, `server/internal/runtime` and `server/internal/dmctl`
are application wiring. External applications cannot import them. Read them as
integration examples; compose public packages in your own application.

Install Go matching this checkout's `go.mod` (currently 1.27). Outside this
repository, create your application directory and module:

```sh
mkdir my-apple-dm
cd my-apple-dm
go mod init example.com/my-apple-dm
```

Choose a reviewed repository commit containing the APIs used here, then replace
the placeholder before running:

```sh
DM_REV=REPLACE_WITH_REVIEWED_COMMIT
go get "github.com/deploymenttheory/go-apple-dm@$DM_REV"
# Required for step 3:
go get "github.com/deploymenttheory/go-apple-dm/server@$DM_REV"
```

Both modules can resolve the same repository commit. Their release tags are
independent: root `vX.Y.Z` and server repository tag `server/vX.Y.Z` (the Go
module version is still `vX.Y.Z`). The server declares its required library
version. Inspect both selections in `go.mod`; APIs are pre-1.0.

When developing against an unpublished checkout, use a **separate workspace**
containing your application, this repository root and its `server/` directory.
The repository's `go.work` does not automatically apply to an application
elsewhere. Do not carry local replacement directives into a published module.

## 2. Generate your first command offline

Save this as `main.go`:

```go
package main

import (
    "log"
    "os"

    "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func main() {
    query := &commands.DeviceInformation{
        Queries: []string{"OSVersion", "BuildVersion"},
    }
    target := support.Target{
        OS: support.MacOS, Version: osversion.New(osversion.MacOS26, 0, 0),
        Channel: support.ChannelDevice,
    }
    if err := query.Validate(target); err != nil {
        log.Fatal(err)
    }
    command, err := mdm.NewCommand(query)
    if err != nil {
        log.Fatal(err)
    }
    if _, err := os.Stdout.Write(command.Raw); err != nil {
        log.Fatal(err)
    }
}
```

Run it:

```sh
go run . > inventory.plist
cat inventory.plist
```

The plist contains `CommandUUID`, `RequestType=DeviceInformation` and your
queries. Each run generates a different UUID. It makes no network request and
does not enqueue anything. Use actual device metadata when selecting the
validation target; generated schema validation cannot enforce every rule Apple
describes in prose.

If you also built `dmctl`, inspect a field without a running server:

```sh
dmctl explain DeviceInformation.Queries -family commands -target macos:26
```

Captured CLI output (trailing spaces removed):

```text
DeviceInformation.Queries  (commands)
Title:                     Device Information Command
Schema:                    third_party/apple-device-management/current/mdm/commands/information.device.yaml

Target  macOS 26.0

  OK  DeviceInformation.Queries
```

Your first checkpoint is a valid plist. Device delivery is a later checkpoint.

## 3. Assemble storage and the MDM core

Start with the memory store in a local test. Replace `main.go` with this program
to construct the service and confirm it rejects an unauthenticated check-in:

```go
package main

import (
    "fmt"
    "log"
    "net/http"
    "net/http/httptest"
    "strings"

    "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
    "github.com/deploymenttheory/go-apple-dm/server/httpapi"
    "github.com/deploymenttheory/go-apple-dm/server/service"
)

func main() {
    core, err := service.New(service.Config{Store: inmem.New()})
    if err != nil {
        log.Fatal(err)
    }
    handler := httpapi.Handler(httpapi.Config{Checkin: core, Connect: core})
    body := `<plist version="1.0"><dict>
        <key>MessageType</key><string>Authenticate</string>
        <key>UDID</key><string>example-device</string>
        <key>Topic</key><string>com.apple.mgmt.example</string>
        </dict></plist>`
    request := httptest.NewRequest(http.MethodPut, "/mdm", strings.NewReader(body))
    request.Header.Set("Content-Type", httpapi.ContentTypeCheckin)
    response := httptest.NewRecorder()
    handler.ServeHTTP(response, request)
    fmt.Println("Check-in without a device certificate:", response.Code)
    if response.Code != http.StatusForbidden {
        log.Fatal("expected the device identity gate to reject the request")
    }
}
```

```sh
go run .
```

The checkpoint is HTTP 403. This demonstrates the identity gate, not a complete
enrollment server. Do not disable pinning to turn that rejection into success.
Before serving a real device, add trusted identity extraction and enrollment
admission in step 4.

For persistence, select `server/sqlstore/sqlite`, `postgres` or `mysql`. Supply
a `crypt.Keyring` backed by your secret provider. Public SQL constructors can
accept a nil keyring and retain plaintext; the reference application's mandatory
keyring is a **composition policy**, not an automatic guarantee of every library
constructor. Select and test your policy explicitly. The keyring protects
designated values, not the entire database.

MDM storage is only one store. DDM, enrollment protocol state, certificate
lifecycle, administrators, events and optional Apple services have their own
interfaces. Plan migrations and transaction boundaries for all the stores you
enable. Start with the existing SQL implementations and their
[contract tests](../../devicemanagement/storage/storagetest).

## 4. Implement enrollment and identity verification

Build these parts before allowing the first real enrollment:

| Your responsibility | Building blocks | Completion evidence |
|---|---|---|
| Decide who may enroll | Your admission rules; `mdmprotocol/enroll` and enrollment-specific packages | An unapproved device/account cannot obtain a usable identity |
| Issue a separate identity per enrollment | `pki/ca`, `pki/scep`, `pki/acme`; persistent protocol state | Retries reuse issuance state; the certificate is bound to the admitted device/account |
| Construct the correct profile | `mdmprotocol/enroll`; generated profile types | URLs, push topic, rights, channel and identity match the chosen enrollment method |
| Verify incoming identity | `server/httpapi` certificate middleware; CMS/TLS verification | An untrusted signer, changed certificate or reused identity fails before side effects |
| Renew, revoke and recover | `pki/lifecycle`, `pki/revocation`, persistent keys and receipts | Renewal/replacement and revocation still work after a restart |

`httpapi.CertFromTLS` extracts a certificate; configure the TLS listener's trust
and verification policy. `CertFromMdmSignature` requires trusted verification
options. A forwarded certificate header needs a trusted proxy which verifies
the client and strips attacker-supplied headers. Merely attaching a certificate
to a request context is not proof of possession.

The MDM core pins identities, denies changed-certificate reenrollment and denies
reuse by default. Its optional `CertificateStatus` callback runs before hooks
and device side effects. You must wire revocation checking if your composition
requires it. Profile replacement is a separate authorized workflow; ordinary
reenrollment is not a substitute for renewal authorization.

Choose profile-based Device Enrollment, ADE, or account-driven enrollment
deliberately. ADE adds organizational assignment and an enrollment-service
token. Account-driven flows add discovery, upstream authentication, account
mapping and ongoing device authorization. An admin API token is not a device
enrollment credential. Follow the [enrollment security guide](../operations/enrollment-security.md)
and test the chosen route end to end.

## 5. Own command delivery and outcomes

Call `service.Core.Enqueue` with an `mdm.Command`, enrollment IDs and
`storage.EnqueueOptions`. Inspect both its error and `EnqueueResult.Skipped`:
the core can reject unsupported targets without failing the whole request.
Targets need recorded OS/channel/capability evidence; do not invent supervision
or ADE state to get a command accepted.

The queue and APNs have different jobs. Enqueue persists the command. A push
wakes the device; it does not carry the command or prove execution. Add
`server/pushnotify` and a suitable `appleplatformservices/push` provider, then
observe the device's command response. Preserve Apple's `NotNow`, error and
retry semantics. Retrying the same operation must not create a new command
identity accidentally.

`service.Hook.Before` can reject operations before storage changes.
`Hook.After` observes their outcome; it is not a substitute for a persisted
transactional audit record. Optional completion hooks must support retries
after partial progress. Keep remote calls outside storage transactions.

The event bus supports different delivery models. A plain asynchronous bus is
not persisted. If an operation must not commit without recording its event, wire
the transactional capture and destination delivery components as the reference
server does. Consumers must tolerate retries and record their own completion.
See [event delivery](../operations/event-delivery.md).

## 6. Add DDM and optional services deliberately

Construct `mdmprotocol/ddm.Engine` with a transactional DDM store. Connect it
to the core using `server/ddmadapter/inproc`; add `server/ddmsync` lifecycle
hooks and the notifier to deliver changes. The engine itself does not queue
MDM commands or send pushes. A declaration must belong to the right set and
enrollment/channel, with activation references, before a device can use it.

The reusable engine's zero-value `Subscriptions.Enabled` is false. The reference
server chooses to enable synthesized status subscriptions by default. Decide
whether that policy suits your application. Preserve canonical token behavior,
per-enrollment snapshots and transactional full-status replacement when adding
dynamic resolvers or expansion. Follow paginated status APIs until the cursor
is exhausted; a page limit is not a fleet or protocol capacity limit.

Other integrations bring their own ownership requirements:

| Capability | Policy and lifecycle you still own |
|---|---|
| Apps and Books | Location isolation, license ownership, asynchronous completion, retry and reconciliation; see [licensing](../operations/apps-and-books.md) |
| FileVault recovery | Persist encryption identity before enqueue, retain the private key for retry/decryption, configure escrow; see [protocol helpers](../operations/protocol-helpers.md) |
| DEP/ADE and AxM | Separate credentials, assigned-device synchronization and profile outcomes; service tokens and API credentials are not interchangeable |
| App push | Namespace-scoped provider credentials, application ownership and distinct development/production environments |
| Return to Service | Explicit eligibility and erasure policy; the unconfigured MDM core answers disabled |
| User channels | Enrollment/channel identity, parent association and the relevant UserAuthenticate behavior |

Implement a capability only after its persistent state and failure handling are
part of your design. Importing a helper does not schedule its reconciliation or
renewal jobs.

## 7. Build the control plane and operational lifecycle

Your product needs an administrative API/UI, authentication, scoped
authorization, inventory views and operator workflows. `server/adminauth` can
supply principal and Cedar-policy behavior. Root status permits principal and
policy administration; ordinary policy-controlled actions still need a permit.
Role names alone grant nothing.

Own process startup, TLS, public routes, ingress limits, workers, health and
shutdown. Stop admitting requests, drain in-flight operations, stop workers and
close stores within an explicit deadline. Keep storage-health, worker-readiness,
certificate-readiness and device-delivery signals separate.

Backups need consistent state **and** the original keyring and retained PKI
material. Test restoration into an isolated environment before sending pushes
or issuing identities from a restored instance. Preserve APNs topic continuity
and renewal account ownership. Decide retention for raw messages, status,
audit and recovery material. The [recovery guide](../operations/recovery.md)
describes the reference implementation's maintenance fence and verification.

## 8. Validate each boundary

1. Compile examples and test typed messages offline.
2. Run storage contracts, including conflicts, rollback and retry paths.
3. Test rejected and accepted identity/admission paths through your HTTP layer.
4. Test Authenticate → TokenUpdate → push → command → acknowledged response.
5. Add DDM synchronization, status and lifecycle tests for both channels.
6. Exercise restart, expired credentials, interrupted issuance and restore.
7. Run the relevant flows on authorized physical devices with your actual
   OS, hardware, enrollment mode, proxy and trust configuration.

Use the [reference bench](../testing/bench.md) as a working composition and the
[scenario catalogue](../testing/bench-catalogue.md) to select cases. Simulator
success verifies modeled exchanges. Generated schema availability and build
success do not establish universal physical-device compatibility.
