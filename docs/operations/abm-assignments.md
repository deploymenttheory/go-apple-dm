# Explicit Apple Business assignments

The reference server persists explicit assignments and unassignments through
`server/axmassignment`. Synchronization collects organization, assignment and
coverage evidence; it never submits assignment mutations. There is no automatic
assignment policy. A workflow names one Apple account, that account's current
configuration and credentials, and its configured service for this MDM.

Configure the account's current revision and the actual CSV column names observed
in that organization's activity log. Apple documents a CSV download URL without a
stable column schema in [activity information](https://developer.apple.com/documentation/applebusinessapi/get-orgdeviceactivity-information).
The configured success value is an exact match; other values remain per-device
outcomes and never imply success.

```sh
curl -X PUT "$DM_URL/admin/v1/axm/accounts/$ACCOUNT/assignment-service" \
  -H "Authorization: Bearer $DM_TOKEN" -H 'Content-Type: application/json' \
  --data '{"account_revision":1,"service_id":"THIS_MDM_SERVICE_ID","log_columns":{"Serial":"serialNumber","Outcome":"status","Success":"SUCCESS"}}'

curl -X POST "$DM_URL/admin/v1/axm/accounts/$ACCOUNT/assignments" \
  -H "Authorization: Bearer $DM_TOKEN" -H 'Content-Type: application/json' \
  --data '{"request_id":"change-123","serials":["SERIAL1","SERIAL2"]}'

curl "$DM_URL/admin/v1/axm/accounts/$ACCOUNT/assignments/change-123" \
  -H "Authorization: Bearer $DM_TOKEN"
```

These column names are the test fixture's schema, not a guaranteed Apple CSV schema.
Use `POST /axm/accounts/{id}/unassignments` for explicit removal from this MDM;
read its workflow at the same `/assignments/{request}` path. The former global
`POST /axm/assign` and `/axm/unassign` routes are absent. The HTTP 202 response
acknowledges durable local intent, not Apple acceptance. Reusing a request ID with
the same account, operation, binding and serial order returns the existing intent;
a different request conflicts. IDs are globally unique within the server.
Outstanding operations with overlapping account/serial targets conflict, including
uncertain submissions. A submitted operation keeps that protection after fencing,
credential rotation, service replacement or account deletion/recreation; loss of
local authority does not resolve an external mutation. A fenced request which
never reached the durable submitting marker permits a new explicit request. Each request is bounded to 1,000 unique, nonempty serials.

The worker verifies the service, resolves the complete organization serial set and
persists Apple's opaque organization-device IDs before mutation. These IDs address
Apple resources; serials alone correlate physical hardware. A checked unassignment
requires every resource to be currently assigned to this account's configured
service. Requests cannot supply arbitrary resource IDs or service targets.
Apple's [create activity API](https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity)
uses an unconditional POST. The service readback is a local precondition; it cannot
prevent an independent administrator changing remote assignment between the read
and the POST.

The workflow exposes separate evidence:

| Field/state | Meaning |
|---|---|
| `queued` | Explicit local request exists; no submission has started. |
| `submitting` | A durable marker precedes a possibly started Apple POST. |
| `accepted_at`, `activity_id` | Apple returned an activity ID and it was persisted. |
| `terminal_status`, `terminal_sub_status` | Apple's activity reached a terminal status. This alone does not prove per-device success. |
| `log_complete`, `log_succeeded`, target `outcome` | Checked CSV evidence for the exact requested serial set. Missing, repeated or unexpected rows remain incomplete; partial failures and unknown outcomes remain visible. |
| target `observed_service`, `observed_at`, `converged` | Separate successful resource/linkage readback; delayed visibility never changes activity or CSV outcomes. |
| `complete` | Terminal and complete log evidence exists, and all targets with the configured successful outcome converged. Failed/unknown outcomes remain retained; `complete` does not mean every device succeeded. |
| `uncertain` | Submission may have been accepted, but no usable activity ID was persisted. Only read-only reconciliation runs. |
| `fenced` | Account incarnation, configuration, credentials or configured service changed. Historical evidence remains; the old worker cannot publish new success. |

Apple documents retrieval by known activity ID for the preceding 30 days. Its
published create/retrieve contracts expose no client idempotency key or activity
discovery endpoint. The controller therefore treats rediscovery of a lost activity
ID as unavailable; this is an inference from those documented contracts, not a
claim about undocumented Apple behavior. A lost response, process crash after
acceptance, or failed acceptance commit never triggers automatic resubmission.
Observed convergence after such a loss remains observed evidence and does not
invent acceptance or terminal success. Non-convergence does not prove that the
submission failed. Uncertain work remains blocked for operator investigation;
there is no retry/clear endpoint that could silently duplicate it.

Account edits and key rotation require explicitly configuring the binding again
against the current account revision. Every configuration creates a fresh binding
generation, including an identical replacement. Restoring earlier service values
cannot revive old leases or workflow authority. Deletion and recreation cannot inherit an
old worker's authority, even when the numeric revision is reused. Per-workflow
leases fence replicas and expired workers. Each remote attempt has a 90-second context
inside a two-minute lease; pending evidence is polled on a five-second schedule.
Attempt expiry leaves the worker and application lifetime intact. Local outcome
bookkeeping uses the worker context, still subject to the current lease and binding;
an expired lease rejects that write and the next owner resumes conservatively.
Application shutdown still cancels both remote calls and local bookkeeping.
Remote requests may finish after fencing or cancellation, but cannot commit stale
local outcomes. Apple calls never run inside local SQL transactions.

The inventory backend stores intent, leases and evidence in the existing shared
transaction/pool. Configuring a binding and enqueueing intent participate in the
administrative audit transaction. Successful organization-device reads retain
complete original JSON and account provenance through the existing source history.
Activity summaries omit signed download URLs and remote error bodies. Raw CSV is
retained byte-for-byte in bounded document chunks, alongside per-device outcomes;
`GET /axm/accounts/{id}/assignments/{request}/log` requires `readRawInventory`.
Ordinary workflow reads require `readBusinessManager`, assignment requires
`assignBusinessManagerDevices`, unassignment requires `unassignBusinessManagerDevices`, and
service configuration requires `manageAxMAccounts`. Use the route catalogue for
exact action names in a deployed server.

Memory state is process-local. SQL persistence survives restart and uses the
existing encryption/recovery policy. Back up the inventory documents with the
server database. Automated fixture and SQL tests establish workflow behavior;
they do not establish populated live Apple acceptance or physical enrollment.

## Named connection inspection

All live Apple reads select `/axm/accounts/{id}` explicitly. Each request opens
that account's current sealed credential, builds an isolated connector and checks
its account revision, incarnation and credential again before returning evidence.
A concurrent replacement returns HTTP 409. A missing or disabled connection never
uses the startup client. No credential-read route exists.

| Route below `/axm/accounts/{id}` | Result | Required grants |
|---|---|---|
| `GET /devices` | Bounded live organization-device page | `readBusinessManager`, `readRawInventory` |
| `GET /devices/{device}` | Live device addressed by Apple's opaque resource ID | `readBusinessManager`, `readRawInventory` |
| `GET /devices/{device}/coverage` | Bounded live AppleCare coverage page | `readBusinessManager`, `readRawInventory` |
| `GET /devices/{device}/assigned-service` | Observed Apple service linkage | `readBusinessManager` |
| `GET /services` and `/services/{service}` | Available Apple device management services | `readBusinessManager` |
| `GET /diagnostics` | Redacted connectivity, inventory presence and sync completeness | `readBusinessManager` |
| `GET /jobs` and `/jobs/{job}` | This account's durable synchronization jobs | `readInventory` |

Live reads use Apple's actual resources and do not write inventory or correlate
hardware. Serial remains the physical correlation key; the `device` path value is
an opaque Apple identifier. Assignment uses explicit serial intent and resolves
that intent to opaque resources through the existing worker. Listing services
does not select this MDM's assignment service; configure the binding explicitly.
The former global device, server and activity reads have no routes. Operation
inspection uses the account's retained assignment workflow instead of a global
activity client. Cursor and limit select bounded live device, coverage and service
pages. Account job cursors advance only through that account's matching jobs.

Named connection reads require successful audit access capture before opening the
connector or contacting Apple when persistent capture is configured. A capture
failure returns HTTP 503; durable local assignment intent and its audit outcome
commit together, so failed capture leaves no queued request. Apple calls run
outside the local transaction.

Diagnostics returns independent fields: `connection` is `connected`,
`authentication_failed`, `permission_denied`, `rate_limited`, or
`connection_failed`; `inventory` is `present`, `empty`, or `unknown`.
`synchronization` is `complete` only after the latest job for the current account
revision succeeds; it is otherwise `incomplete`. Connectivity and a successful
empty live page do not establish complete retained synchronization. OAuth error
descriptions, upstream bodies, bearer tokens and request URLs are never included.
Disabled accounts return HTTP 409; missing accounts return HTTP 404. Other live
read failures return a reviewed category with an upstream-failure HTTP status.

The CLI preserves the same account and identifier boundaries:

```sh
dmctl axm accounts diagnostics ACCOUNT
dmctl axm accounts services ACCOUNT
dmctl axm accounts device ACCOUNT APPLE_RESOURCE_ID
dmctl axm accounts coverage ACCOUNT APPLE_RESOURCE_ID
dmctl axm accounts assigned-service ACCOUNT APPLE_RESOURCE_ID
dmctl axm accounts configure-service ACCOUNT --file binding.json
dmctl axm accounts assignment-service ACCOUNT
dmctl axm accounts assign ACCOUNT --file intent.json
dmctl axm accounts operation ACCOUNT change-123
dmctl axm accounts operation-log ACCOUNT change-123
dmctl axm accounts unassign ACCOUNT --file unassignment.json
dmctl axm accounts jobs ACCOUNT
dmctl axm accounts job ACCOUNT JOB_ID
```

`binding.json` contains `account_revision`, `service_id` and `log_columns` from the
configuration example above. `intent.json` and `unassignment.json` contain an
explicit `request_id` and `serials`. CLI assignment returns queued intent; inspect
the durable operation for acceptance, terminal per-device outcomes and independent
linkage convergence. `axm accounts sync ACCOUNT --wait` polls the named account's
job route. None of these commands imply assignment policy or general ABM administration.
