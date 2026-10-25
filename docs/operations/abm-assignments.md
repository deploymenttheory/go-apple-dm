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
uncertain submissions. Each request is bounded to 1,000 unique, nonempty serials.

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
against the current account revision. Deletion and recreation cannot inherit an
old worker's authority, even when the numeric revision is reused. Per-workflow
leases fence replicas and expired workers. Each attempt has a 90-second context
inside a two-minute lease; pending evidence is polled on a five-second schedule.
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
