# 0039: Declarative management is an extension of MDM, not a peer

Status: accepted
Date: 2026-09-03
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices> — the statement this record rests on
- Doc: <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management> — enablement, and what unenrolling does
- Doc: <https://developer.apple.com/documentation/devicemanagement/declarative-management> — the check-in endpoint and its four operations
- Doc: <https://developer.apple.com/documentation/devicemanagement/declarativemanagementcommand> — the MDM command that turns it on
- YAML: `third_party/device-management/mdm/checkin/declarativemanagement.yaml` — filed under `mdm/`, not `declarative/`
- YAML: `third_party/device-management/mdm/commands/declarativemanagement.yaml`
- YAML: `third_party/device-management/declarative/declarations/assets/data.yaml` — `Authentication.Type: MDM`

Framing: this record exists because the admin API had eleven DDM routes and none for MDM, and the
question was whether that reflected the protocol or an inherited habit. The answer decides whether
the project re-architects. It does not.

Dependency note: none. Nothing here adds a dependency; the work it justifies removes an edge.

## References read

- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`
  `zentral/contrib/mdm/artifacts.py`, `commands/declarative_management.py`, `commands/scheduling.py`,
  `commands/base.py`, `declarations/`, `workers.py`, `models.py`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`
  `server/mdm/apple/commander.go`, `server/mdm/apple/reconcile.go`,
  `server/service/apple_mdm_declarations_batched.go`, `server/service/apple_mdm.go`,
  `server/mdm/nanomdm/service/service.go`, `server/mdm/nanomdm/service/nanomdm/dm.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`
  `notifier/notifier.go`, `notifier/foss/foss.go`, `notifier/foss/dm.go`, `notifier/cmd_dm.go`,
  `http/api/api.go`, `http/api/declarations.go`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`
  `ddmadapter/ddmadapter.go`, `ddmadapter/service.go`, `enqueue/enqueue.go`, `nanohub.go`,
  `cmd/nanohub/nanohub.go`

## Known pitfalls found

- `kmfddm/notifier/foss/foss.go:144-201`: every per-chunk enqueue error is logged and `continue`d,
  and `Enqueue` returns `nil` unconditionally. A failed enqueue is silently swallowed, and a
  single `bytes.Buffer` created outside the loop is consumed by the first request, so chunks after
  the first send an empty body.
- `kmfddm/http/api/declarations.go:55-63`: the HTTP response is written *before* the notify runs,
  on `r.Context()`, which is cancelled when the handler returns. The enqueue can be cancelled
  mid-flight and the caller cannot see it.
- KMFDDM has no coalescing anywhere. Fifty declaration edits produce fifty
  `DeclarativeManagement` commands and fifty pushes.
- `zentral/contrib/mdm/commands/scheduling.py:178`: the token comparison runs a full scope walk on
  every device check-in, and there is no way to propagate faster than the APNs worker's period,
  which defaults to four hours.
- `fleet/server/service/apple_mdm_declarations_batched.go:203`: their own TODO — a failed
  `DeclarativeManagement` send leaves the host in a state that never retries.
- Ours: `internal/app` gave the notifier the raw `storage.Store`, so DDM's own command skipped the
  hook chain, the `schema/support` target screening, and the `CommandQueued` event.
- Ours: `ddm.Config.Wake` let the engine call back into the notifier, a cycle no reference has.
- Ours: the admin API had no `mdm` route family at all, over a storage API that supported every
  operation.

## What they do

- **Apple**: "While declarative management is a new paradigm, it's not a new protocol: the protocol
  has been added to the existing MDM protocol to make adoption simpler." DDM rides `PUT /checkin`
  with `application/x-apple-aspen-mdm-checkin`, carries the same six identity keys as `Authenticate`
  and `TokenUpdate`, authenticates with the MDM device-identity certificate, has no enrollment of
  its own, is switched on by an MDM command, and is wiped when the device unenrolls. Its four
  endpoints are one HTTP endpoint with an `Endpoint` discriminator in the body.
- **Zentral**: `declarations/` is a pure payload library that never imports `commands` and never
  touches APNs. The DeclarationsToken is recomputed at each check-in and compared to the value on
  the device row; a mismatch creates a `DeclarativeManagement` through the same
  `Command.create_for_target` as every other command. The two halves meet at one read-only property
  on a mediating object. No change-notification fan-out exists at all.
- **Fleet**: `MDMAppleCommander.DeclarativeManagement` sits beside `InstallProfile` and ends in the
  same `EnqueueCommand`. Change detection is a 30s cron reconcile whose comment says it "uses the
  SAME shared dispatcher so profile and declaration label semantics cannot drift". Its DDM serving
  object holds no commander; the reconciler that does is separate.
- **KMFDDM / NanoHUB**: the notifier resolves *who* and delegates *how* through a two-method
  `Enqueuer`. NanoHUB avoids a cycle by building both halves from storage and push and never from
  each other. KMFDDM notifies from the admin write path, in eight handlers.

## What we do better

1. **The protocol relationship is modelled, not inherited.** `mdm/` has no reference to DDM; the
   service dispatches `DeclarativeManagement` as one of nine check-in messages through a seam;
   `ddm` depends on `mdm` and never the reverse. The role split is deployment topology, and records
   0023 and 0025 now say so rather than reading as a protocol boundary.
2. **DDM's command travels the MDM command path**, so it is screened against `schema/support`,
   runs the hook chain, and appears in the event bus and the audit trail — like every other command.
   Zentral is the only reference that also does this; NanoMDM has no path rich enough to bypass.
3. **No callback from the serving side into dispatch.** `Config.Wake` is gone; the persistent signal
   is the change rows written inside the transaction. The latency shortcut lives once in the admin
   route wrapper, where a route added later cannot forget it, rather than repeated per handler.
4. **Coalescing.** A burst of edits becomes one command per enrollment, with a window, dedupe and
   backoff. No reference has this.
5. **MDM and DDM are peers in the admin plane**, with the enrollment as the root object and DDM's
   sets and declarations as sub-resources of it.
6. **Every admin route is driveable from the CLI, and stays so**, because the test walks the route
   table the mux is built from.

## Verified by

1. Import direction is asserted by the build; `app.TestBuild/*` covers the roles (claim 1).
2. `app.TestDDMCommandTravelsTheCommandPath/PublishesCommandQueued` and `/LandsInTheAuditTrail`
   (claim 2; would fail on the previous wiring, and on NanoHUB's, which enqueues from storage).
3. `app.TestAdminWriteKicksTheNotifier`, `app.TestAdminReadDoesNotKick`, and the `ddm` engine tests
   that assert change rows rather than wake counts (claim 3).
4. `ddm.TestNotifier*` window, dedupe and backoff cases (claim 4).
5. `app.TestMDMAdminRoutes`, `app.TestMDMAdminRoutesAreGoverned`, `app.TestAdminAPIOnTheMDMRole`
   (claim 5).
6. `e2e.TestE2E_AdminCLI/DrivesEveryAdminRoute` (E2E-024) walks `GET /routes` and fails when a
   route cannot be expressed by the CLI (claim 6).

## Not modelled, deliberately, and recorded so it is not rediscovered

- **DDM ownership beats MDM.** Apple states it per command: `InstallApplication` "fails if
  Declarative Device Management is managing the app"; `RemoveApplication` likewise;
  `ManagedApplicationList` and `InstalledApplicationList` omit DDM-managed apps; `CertificateList`
  "doesn't return certificates that Declarative Device Management installs"; a `LegacyProfile`
  takeover makes MDM install, update and remove of that profile fail. We cannot express this: the
  `DMHandler` seam carries device traffic only, so `service` can never ask `ddm` what it owns. The
  pieces are in place for whenever it is scheduled — `Core.checkTargets` already screens outbound
  commands against their target, and `Engine.EnrollmentDeclarations`/`Manifest` already hold the
  answer; only the seam is missing. Note Fleet does not model it either, relying on identifier
  uniqueness between profiles and declarations; only Zentral does, by subtracting DDM-managed
  artifact types from the classic install paths.
- **Cleanup on re-enrolment is best-effort.** `ddm.ServiceHook.After` clears DDM state after the
  MDM operation has committed and swallows the error into a warning, so a failed clear leaves a
  re-enrolled device inheriting the previous enrollment's declarations — the bug KMFDDM #41
  describes. Fixing it needs one transaction across `storage.Store` and `ddm.Store`, which today
  have separate transaction boundaries even on the same `*sql.DB`.

## Rejected alternatives

- Re-architecting so DDM and MDM are peer subsystems: the protocol says DDM rides on MDM, and the
  code already matches. The asymmetry was in the admin plane, not the model.
- A lazy adapter to pass the core to the notifier: it would have been a second late-bound
  indirection holding one cycle together. Removing the cycle is smaller and matches every
  reference.
- Moving `mdm.NewCommand` out of `ddm/notifier.go`: KMFDDM and NanoHUB both build the command
  inside their DDM package, and passing a built `*mdm.Command` lets `checkTargets` inspect the real
  `RequestType`.
- A typed `mdmctl` verb per proxied family route: `dep`, `axm` and `acme` proxy Apple-shaped APIs
  whose surface is not ours to model. `mdmctl api` keeps them reachable without pretending
  otherwise.
- Retiring the `mdm`/`ddm` role split: the protocol finding constrains behaviour, not deployment.
  Running the declaration engine separately is a legitimate operational choice; the records were
  the thing that needed correcting.
