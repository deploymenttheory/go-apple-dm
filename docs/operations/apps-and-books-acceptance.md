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

Keep the workspace outside this repository. On Unix, private directories use
mode 0700 and files use mode 0600. On Windows, native protected access-control
lists grant access only to the current user, LocalSystem and administrators;
Go's synthesized mode bits do not establish privacy. The lab applies native
protection before writing identity, evidence and pending-fence files. Each new
Apps and Books scope or phase directory is created with private access, without
changing existing parent directories. Keep operator-supplied workspace and input
paths private using the platform's native permissions.

Unix workspace preparation:

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
The serial is trimmed and uppercased before any comparison or Apple request,
matching the spelling in Apple and native device reports.
Apple's `clientUserId` is a user licensing identity. It does not require a serial,
ABM membership or hardware enrichment, and does not select a native management
channel. Serial is the sole cross-source physical correlation key; opaque
enrollment identifiers retain their native addressing purpose.

LIVE-013 needs `readAppsBooks`. LIVE-014 additionally needs `manageAppsBooks` and
`readAppsBooksEvidence`, including job creation/submission/polling and privileged
callback/job reads. The LIVE-014 preflight reads only the designated user, asset
and target assignment, so its duration does not grow with the location; LIVE-013
collects complete pages. It then creates a queued local reference intent and
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
absolute private evidence directory. The direct SDK path has no authenticated callback confirmation channel, so its
initial status query and cleanup query always observe Apple’s five-minute
notification fallback; PENDING responses wait at least 30 seconds. This is a
conservative acceptance policy, not a claim that Apple mandates waiting after
a confirmed callback. SDK completion, reference worker completion,
authenticated per-target callback and independent assignment readback are
separate checks. Polling respects the SDK's five-minute fallback and minimum
30-second interval; allow the run sufficient time for actual callbacks/status.
Apple's status can complete the reference job at its fallback poll before the
callback arrives. The receiver still records a late callback against the
succeeded job, so the module waits up to one minute for it before reporting the
callback path unexercised.
LIVE-014 has a protocol-specific default of 33 minutes and 30 seconds. Its direct
SDK assignment phase reserves five minutes before initial status polling, one
30-second PENDING interval and four minutes for bounded API/readback work, an
actual nine-minute-thirty-second deadline. The reference phase reserves the same
plus the one-minute callback wait, a ten-minute-thirty-second deadline. Direct
cleanup independently reserves five minutes for notification fallback,
one 30-second PENDING interval and three minutes for requests/readback. Before
the first Apple mutation the remaining deadline must cover these 28 minutes
and 30 seconds; a shorter remaining deadline is blocked and the error reports
the time remaining. The default leaves five minutes for preflight.
`lab run -module-timeout 45m` overrides the outer module deadline; `0` uses the module
default. This flag is independent of the global `-timeout` for individual HTTP
requests. The primary phase and cleanup caps remain in force. Other modules
retain the two-minute default.
Cleanup has its own context and can continue for at most eight minutes and
thirty seconds after cancellation; the outer module deadline alone is not a
combined process-and-cleanup deadline. The API/request allowances are project
budgets, while five-minute fallback and 30-second PENDING spacing follow the
[notification contract](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications).
Slow or persistently PENDING requests can still
exceed these bounded allowances and retain the recovery fence.

Cleanup uses a separate eight-minute-thirty-second bounded context. If the original assignment was absent,
the module disassociates only this app from this target and independently confirms
absence. It never uses broad target revocation. A preexisting assignment is never
disassociated. Cleanup must succeed before the invocation's own fence is removed.
Each direct mutation re-reads native app/revocation capabilities; the original
snapshot cannot authorize cleanup after those restrictions change. A final
reference read rejects contradictory callbacks received while cleanup settles.

Any uncertain submission, partial status, conflicting revision, callback failure,
failed readback or unsuccessful cleanup retains the fence and original evidence.
Once the fence exists, every unsuccessful outcome is a failure rather than
blocked, including an sToken or grant that becomes unavailable and an
unexercised callback: Apple licensing may already have changed, and the fence
blocks later runs until reconciliation. Prerequisites missing before the fence
remain blocked. The `cleanup` assertion passes only after the fence is released.
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
the exact expected bundle and version. `max_age_seconds` bounds source age. For
MDM it applies to the operation's native observation, which an installed-app
query refreshes. A DDM app observation keeps the time of its original app
report, and DDM status reports carry only changes, so DDM freshness is the latest
authenticated status report for the enrollment. A device that has sent no status
report within the bound remains blocked. Generated feature support governs each
platform, OS and channel. ACK and declaration publication alone are insufficient.
DDM uses current managed-app status joined to its declaration identity. For a
new declaration identifier, once the current owned generation and managed-app
installation are proven, their report arrival order does not affect terminal
processing; the app observation timestamp and native report bytes remain
unchanged. A successor reuses its predecessor's identifier, so it settles only
through an owned app report received after the new generation is confirmed. Installed
queries do not set the MDM-only managed-app filter. Current native enrollment
metadata selects generated-supported identity query keys; the ensuing signed
protocol replies still supply the required fresh OS/build/platform facts. On
macOS User Enrollment, `InstalledApplicationList` is forbidden. That query is
reported as `unexercised_unsupported_for_native_target`; DDM acceptance instead
requires current authenticated, exactly owned `app.managed.list` installation
and the exact actual app version both before and after the identity queries.
The report names `current_owned_app_managed_list` as its verification method.
Missing version, stale status, a foreign declaration or removal cannot satisfy
this path. MDM without a supported native version observation remains
unexercised. User licensing and this privacy path require no hardware serial. tvOS app delivery uses MDM;
this does not exclude its other supported DDM status features.

The privileged `native-observation` GET reads current authenticated native facts
before and after explicit query commands. It does not call SDK `Step`, update
leases, submit licensing or queue work. Stored operation evidence remains an
as-of snapshot; the inspection retains the operation as first read
(`operation-before.json`) and as read for the final provenance check
(`operation-final.json`). The server owns DDM status folding; the lab consumes its typed
observation and exact declaration/token/bundle provenance without recreating that
state engine. A late foreign declaration or removal cannot pass because the app
still appears in an installed-app list.

The inspection reports cleanup as not applicable because it changes no apps or
licenses. It does not claim the environment was restored after external setup.
Real-location and physical device acceptance remain pending until these procedures
run successfully against the explicitly designated resources; automated fixture
results do not close that requirement.

Automated orchestration acceptance uses the genuine TLS reference runtime,
sealed SQLite, the SDK HTTP fixture, authenticated callback composition and signed
native simulator exchanges. Its private due-state fixture first proves the real
SQL early-poll barrier, then changes only the selected job's `NextPollAt` while
preserving all other job/receipt/fence state. This constructs simulated scheduler
timing; it does not prove five elapsed wall minutes or PostgreSQL/MySQL timing.
Shared SDK storage contracts and reference transaction tests separately exercise
all three SQL backends. These automated scopes do not establish live Apple or
physical installation acceptance.
