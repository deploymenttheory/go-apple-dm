# Apps and Books acceptance

`dmctl lab` separates location API evidence, explicitly selected reversible
licensing, and current installation on one authenticated native target. These
checks consume the Apps and Books SDK and the reference server's ordinary
authorized API. They do not acquire content, administer an ABM organization,
enable subscriptions, change notification configuration, or assign books.

| Module | Required evidence | Changes |
|---|---|---|
| LIVE-013 | Dedicated sToken, matching reference location, service/client configuration and complete assets/users/assignments | Reads only; no registration, ownership claim, refresh or callback configuration |
| LIVE-014 | Explicit free revocable app, one designated target, SDK and reference job completion, authenticated callback and target-specific assignment readback | Assign selected app; remove only a license absent in the original snapshot |
| LIVE-015 | Operator-selected prepared operation, privileged native dispatch/status, fresh authenticated identity, OS/build, current app and expected version | Native read queries only; no installation, removal or licensing changes |

A fixture pass is simulated service or delivery evidence. A live API pass cannot
establish physical installation. Every installation report retains separate
`macOS`, `iOS`, `iPadOS`, `visionOS` and `tvOS` rows; other platforms remain
`unexercised`. Virtual targets remain `simulated`. Missing tokens, content,
permissions, configured callbacks and devices are unmet prerequisites. Partial,
unknown or contradictory licensing results never pass.

## Private location scope

Use a dedicated Apps and Books location and its downloaded **base64 sToken**.
ABM OAuth and ADE credentials cannot substitute. Register the location through
the [reference workflow](apps-and-books.md) before acceptance. Ownership and an
authenticated callback must already be explicitly configured before LIVE-014.
LIVE-013 does not perform that setup.

Keep the workspace outside this repository, with private directories mode 0700
and files mode 0600:

```sh
PRIVATE_LAB=/absolute/path/to/private/live-workspace
install -d -m 700 "$PRIVATE_LAB/appsbooks"
install -m 600 /absolute/path/to/location-stoken "$PRIVATE_LAB/appsbooks/stoken"
```

Create `appsbooks/acceptance.json`, replacing example values with the designated
location. `uid` is Apple's library `uId`; `location` is the reference identifier.
Their opaque values bind the location and do not identify physical hardware.

```json
{
  "location": "content-lab",
  "uid": "APPLE-LIBRARY-UID",
  "mdm_id": "STABLE-MDM-ID",
  "max_pages": 100,
  "asset": {"adamId": "DESIGNATED-FREE-APP-ID", "pricingParam": "STDQ"},
  "client_user_id": "DESIGNATED-ASSOCIATED-USER",
  "designated_free_app": false,
  "allow_licensing": false,
  "exclusive_location_quiescent": false
}
```

For device licensing replace `client_user_id` with `serial`, never provide both.
Apple's `clientUserId` is a user licensing identity. It does not require a serial,
ABM membership or hardware enrichment, and does not select a native management
channel. Serial is the sole cross-source physical correlation key; opaque
enrollment identifiers retain their native addressing purpose.

LIVE-013 needs `readAppsBooks`. LIVE-014 additionally needs `manageAppsBooks` and
`readAppsBooksEvidence`, including job creation/submission/polling and privileged
callback/job reads. The preflight creates a queued local reference intent and
checks its evidence authority before any Apple licensing POST. Queuing cannot
submit a request automatically. No grant is installed by these modules.

```sh
make lab-build
DMCTL="$PWD/test-lab/local/bin/dmctl"
"$DMCTL" lab run -workspace "$PRIVATE_LAB" -modules LIVE-013
```

Preserve the source revision and executable hash with the private run directory.
Raw source pages and callback/configuration evidence can contain identifiers and
receiver credentials. Never attach them to public issues, commits or reports.
Ordinary errors omit credential contents, user identities, serials and private
credential paths. Each invocation allocates a new private evidence directory;
reruns do not truncate prior evidence.

## Reversible licensing

Designate a free app acquired for this dedicated location. Confirm that native
asset capabilities report `App`, revocable licensing and device assignment when
using a serial. User licensing requires a currently Associated user. Missing
availability does not mean zero or permission to assign. Books are excluded from
mutation because they cannot routinely be reclaimed.

Stop external writers and background operators for this location. Apple has no
ownership compare-and-swap. Set all three attestations
`designated_free_app`, `allow_licensing` and `exclusive_location_quiescent` to
`true` only for the designated scope, then run LIVE-014 separately:

```sh
"$DMCTL" lab run -workspace "$PRIVATE_LAB" -modules LIVE-014
```

The module snapshots configuration, ownership, location revision, capabilities
and the complete original target assignment before its first Apple POST. A
persistent exclusive `appsbooks/pending-licensing.json` fence retains the exact
absolute private evidence directory. SDK completion, reference worker completion,
authenticated per-target callback and independent assignment readback are
separate checks. Polling respects the SDK's five-minute fallback and minimum
30-second interval; allow the run sufficient time for actual callbacks/status.
LIVE-014 has an eight-minute protocol-specific default to accommodate the
five-minute fallback and subsequent status requests; explicit library run options
can supply a different bound. Other modules retain the two-minute default.

Cleanup uses a separate bounded context. If the original assignment was absent,
the module disassociates only this app from this target and independently confirms
absence. It never uses broad target revocation. A preexisting assignment is never
disassociated. Cleanup must succeed before the invocation's own fence is removed.

Any uncertain submission, partial status, conflicting revision, callback failure,
failed readback or unsuccessful cleanup retains the fence and original evidence.
A rerun under another output directory cannot bypass it. Stop the original
process and any worker before operator reconciliation. Inspect the retained job,
native configuration and full assignment evidence through privileged routes;
establish the outcome of each recorded event before deciding on an explicit
restoration. Never guess an inverse, replay a submission, overwrite the marker,
or remove it because current state happens to resemble the original snapshot.
Retain the operator's reconciliation decision and cleanup evidence privately.

## Independent native installation inspection

Prepare an explicit installation through the reference managed-content workflow
and allow its selected native device to process it. LIVE-015 does not prepare or
remove that app. External preparation and subsequent app removal remain operator
responsibilities; existing apps and licensing are preserved by this inspection.
`Cancel` is not post-dispatch removal and is not restoration evidence.

Create private `appsbooks/installation.json`:

```json
{
  "operation": "EXPLICIT-PREPARED-OPERATION",
  "bundle_id": "com.example.designated-app",
  "version": "EXPECTED-NATIVE-VERSION",
  "platform": "macOS",
  "identity": "scep",
  "max_age_seconds": 900
}
```

Select the actual enrolled device using the lab's device target options. For a
native user channel also select its installing user. The operation's exact
authenticated native channel governs query addressing; licensing type does not
imply a user channel. Grant `readManagedContent`, `readManagedContentEvidence`,
`readEnrollment`, scoped `enqueueCommand.DeviceInformation`, applicable
`enqueueCommand.InstalledApplicationList`/`enqueueCommand.ManagedApplicationList`,
and `pushEnrollment`, in addition to location read authority.

```sh
"$DMCTL" lab run -workspace "$PRIVATE_LAB" -modules LIVE-015 -device-id "$NATIVE_DEVICE_ID"
```

The gate binds privileged wire and native processing to the selected operation,
rejects stale/foreign processing, observes authenticated identity continuity and
fresh OS/build, and requires positive current installed/managed app state with
the exact expected bundle and version. Generated feature support governs each
platform, OS and channel. ACK and declaration publication alone are insufficient.
DDM uses current managed-app status joined to its declaration identity; installed
queries do not set the MDM-only managed-app filter. tvOS app delivery uses MDM;
this does not exclude its other supported DDM status features.

The inspection reports cleanup as not applicable because it changes no apps or
licenses. It does not claim the environment was restored after external setup.
Real-location and physical device acceptance remain pending until these procedures
run successfully against the explicitly designated resources; automated fixture
results do not close that requirement.
