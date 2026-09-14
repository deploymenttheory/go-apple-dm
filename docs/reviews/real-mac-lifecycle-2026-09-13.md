# Real Mac lifecycle implementation

Implementation branch: `feat/real-mac-lifecycle-and-recovery`.
Baseline: PR #53, merge `6fd5a46`; release tree `885e084`.

This report tracks the combined implementation. An unchecked milestone is not a
claim of compatibility or a completed feature. Required live acceptance uses the
operator's Apple silicon Mac on macOS 26.6.2.

## Milestones

- [x] Correct installing-user enrollment and verify ACME and SCEP on the Mac (isolated candidates; repeat on the reviewed commit).
- [x] Verify identity replacement, enrollment/HTTPS trust rollover and SQLite recovery (isolated candidates; repeat on the reviewed commit).
- [ ] Record all existing events transactionally and deliver through persistent state.
- [ ] Provide backup, verification and restore commands for all SQL backends.
- [ ] Exercise public packages from an external server and validate patch releases.

## Initial evidence

Existing private live results establish device enrollment and APNs-triggered
inventory. Full ACME acceptance is blocked: macOS records an enrollment installed
for `<Computer>`, no installing-user identity, and ignores that profile for the
interactive user's agent. The profile builder unconditionally writes System
scope. The reference server already advertises per-user connections.

The existing live acceptance check accepts any enabled child user. It must instead
identify the installing user and verify an acknowledged command on that channel.

## Enrollment checkpoint

The library now exposes installation scope and preserves it on profile parsing
and replacement. Manual macOS exports default to User scope; explicit System
scope remains available. This follows the installation model described in
[Apple's macOS management documentation](https://developer.apple.com/documentation/devicemanagement/managing-devices-and-users-in-macos).
Whether scope caused this Mac's missing installing-user association remains a
live hypothesis until reinstallation provides evidence.

LIVE-002/003 now require an operator-specified `-user-id`, a completed TokenUpdate
for that exact child channel, and an acknowledged ProfileList on that channel.
The device inventory response is checked separately. Another enabled user cannot
satisfy acceptance.

Targeted enrollment library, reference application, bench and CLI tests passed.
A private consistent SQLite snapshot passed integrity verification before profile
issuance. The reviewed attested ACME profile was handed to the operator for manual
installation; live acceptance is pending.

The first reinstall attempt reached ACME issuance but MDM Authenticate was
rejected by the configured re-enrollment policy. After temporarily enabling that
policy for the admitted lab Mac, a second attempt exposed a separate storage
defect: a disabled enrollment could not restart even after policy approved a new
identity. Retrying either consumed profile then produced ACME HTTP 400 because
its one-time identifier had already been claimed.

The storage transition now carries explicit approval to restart a disabled
enrollment with a different certificate. The old certificate cannot reactivate
it, a stale policy decision cannot replace its pin, and the new enrollment must
provide fresh push tokens. Memory and SQLite contract tests, a signed HTTP test
covering removal through renewed command delivery, and targeted race tests pass.
The lab is running this isolated fix with its existing schema. A third reviewed
profile installed successfully. The temporary re-enrollment setting was switched
off and the server restarted before live acceptance.

At 18:35 UTC on September 13, LIVE-002 passed against that isolated candidate:
the device acknowledged APNs-triggered DeviceInformation with OS/build values,
and the exact local GeneratedUID's user channel acknowledged an independently
queued ProfileList after its APNs wake. Both channels completed TokenUpdate.
macOS confirms user-approved enrollment. The candidate is based on `b1a0d19` plus
the new re-enrollment fix; private source and binary hashes accompany the report.
The ordinary bench CLI lacked its supervisor's running file after the direct
server restart, so a private runner invoked the unchanged LIVE-002 scenario
against the existing process. This run is an intermediate checkpoint; final
acceptance must be repeated against the reviewed implementation commit.

## Temporary profile and DDM checkpoint

The operator approved both live tests. The exact installing-user channel
acknowledged ProfileList, InstallProfile, ProfileList, RemoveProfile and
ProfileList. The list was checked for the unique marker's absence, presence and
absence respectively. The only installed payload wrote a temporary marker in
the lab's own managed preference domain.

At 19:02 UTC, the Mac reported the temporary DDM activation and its OS/build
subscription as active and valid. Fresh status values were macOS `26.6.2` and
build `25G83`. The test set was unassigned and its two declarations deleted.
The Mac acknowledged the resulting DeclarativeManagement command, then sent a
new `management.declarations` item at 19:03 UTC without either test declaration.
Both enrollment channels remained enabled, with re-enrollment still disabled.
Private reports and cleanup evidence are under
`test-lab/local/certs/candidates/live-profile-ddm`.

The first DDM attempt was rejected before assignment because the private runner
generated identifiers longer than the server's 64-byte limit. Its cleanup also
treated deletion of a nonexistent declaration as a failure. The identifiers and
cleanup handling were corrected; the successful profile test was not repeated.

Cleanup exposed a separate library defect: the stored raw status item correctly
showed removal, but parsed declaration rows retained earlier active entries when
`FullReport` was omitted. Memory and SQL storage now replace the declaration
collection whenever `management.declarations` is present, while other items
omitted from a partial report remain unchanged. The regression first failed on
the old behavior. DDM engine and memory race tests, plus SQLite, PostgreSQL and
MySQL storage race suites, pass with the fix. The reference application's race
suite also passed. This fix is now deployed in an isolated lab candidate with
private source and binary hashes under `candidates/ddm-lifecycle-fix`.

The Mac also reported the existing automatically generated subscription as
inactive because it lacked an activation. The explicitly activated temporary
subscription passed. The library now generates an unconditional companion
activation for its automatic subscription. Explicitly assigned configuration or
activation overrides remain authoritative. Tests cover restart, capability changes,
override removal and disabled subscriptions.

The deployed candidate passed another live check: the automatic configuration
and activation were active and valid, fresh OS/build values matched inventory,
stale temporary declarations disappeared, and both device and exact installing-user
commands were acknowledged. Re-enrollment remained disabled. These results are
intermediate candidate evidence; the final reviewed commit still requires acceptance.

Preparing SCEP exposed a returning-user transition defect in storage: a successful
parent re-enrollment left its old user row disabled. An approved parent restart now
resets children to pending, clears old tokens and queues, and requires a fresh
TokenUpdate signed by the new parent identity. Memory, SQLite, PostgreSQL and MySQL
contract tests and a signed HTTP regression passed; old identities remain rejected.

At 20:28 UTC, the maintained CLI passed LIVE-002 using direct HTTPS attachment to
the existing server. At 20:34 UTC, LIVE-004 passed activation, inventory comparison
and removal. Its first attempt had incorrectly demanded new timestamps for
unchanged OS/build values. The Mac reported the new declarations but correctly
omitted those unchanged values. The corrected scenario requires fresh declaration
status and compares retained DDM values with a newly acknowledged DeviceInformation
response. Regression cases reject mismatched inventory and stale declaration status,
and accept unchanged matching values. The failed report is preserved separately.

This follows Apple's incremental status model and its status-subscription set
union: see [WWDC21](https://developer.apple.com/videos/play/wwdc2021/10131/) and the
[subscription schema](https://github.com/apple/device-management/blob/release/declarative/declarations/configurations/management.status-subscriptions.yaml).
The earlier 19:02 and 19:31 checkpoints independently establish initial OS/build
status receipt. A repeated subscription does not prove another OS/build item arrived.

## Persistent event checkpoint

The native SQL audit destination now appends the occurrence and acknowledges its
delivery in the same transaction. Failure in append, acknowledgement or lease
validation rolls both back. A recovered attempt commits once; pruning that audit
row cannot make the acknowledged delivery replayable. Remote destinations run
outside SQL transactions. Focused race tests for these failure cases and the
application's audit/event/enrollment paths passed. Remote operation journaling,
the remaining event producers and the full backend/security/coverage gates remain
outstanding.

## SCEP handoff checkpoint

A fresh SCEP profile is prepared under `candidates/scep-enrollment` with User
scope and rights mask 19. Its trust anchors, service URL and APNs topic match the
installed ACME profile. A consistent SQLite backup passed integrity verification;
configuration and private evidence accompany it. The profile uses the managed
issuer route `/scep/issuers/1`. An initial private review incorrectly expected the
legacy `/scep` path; correcting that assertion did not require another issuance.

The same candidate server restarted with temporary re-enrollment enabled and the
admission policy restricted to the lab Mac. HTTPS health and the maintained
E2E-027 discovery/trust scenario passed at 20:39 UTC. Operator removal of the ACME
profile and installation of the fresh SCEP profile are pending. Its grant expires
at approximately 21:37 UTC (22:37 BST) on September 13. After fresh device and
returning-user TokenUpdate, disable temporary re-enrollment and restart before
LIVE-003. A failed/expired profile needs a fresh grant; the old ACME profile cannot
be reused for recovery.

The first SCEP installation failed at 20:42 UTC: GetCACert and GetCACaps returned
200, but PKIOperation returned 400 with `scep: invalid CMS message`, before grant
verification. The CMS algorithm-policy decoder assumed DER; Apple's native SCEP
encoder emits BER indefinite lengths. An offline reproduction using the Mac's
Security framework verified the original CMS signature, decrypted the envelope
and verified CSR proof, while reproducing the old policy-check rejection.

The library now creates a bounded definite-length view for its AES policy check,
preserving the original bytes for signature verification and decryption. A failing
regression now passes, including constructed ciphertext chunks, malformed framing,
truncation, trailing input, nesting limits and rejection of DES in BER. SCEP and
parser race suites, the isolated application's issuance/re-enrollment regressions,
and targeted lint/security checks passed. A 20-second fuzz run exercised 2,135,960
inputs without failures. The corrected public SCEP server produced a certificate
reply to the native offline request; that is not a live enrollment pass.

The isolated `candidates/scep-ber-fix` binary is deployed with private provenance
and fresh backups. HTTPS health and E2E-027 passed. A reviewed second SCEP profile
was generated at 20:57 UTC and expires at approximately 21:57 UTC (22:57 BST).
Both enrollment rows were disabled after the operator removed ACME. Temporary
re-enrollment remains enabled for the admitted Mac, pending the fresh SCEP install;
it must be disabled again before LIVE-003 acceptance.

The operator installed the second SCEP profile successfully. At 21:00 UTC the
device and exact returning user's channels completed fresh TokenUpdate under the
new SCEP identity. The server's issuance record and identity pin agree, and macOS
confirms user-approved enrollment. Temporary re-enrollment was disabled and the
same verified server restarted before acceptance.

LIVE-003 passed at 21:02 UTC: APNs-triggered DeviceInformation was acknowledged,
followed by an acknowledged ProfileList on the exact installing-user channel.
LIVE-004 passed at 21:03 UTC on the SCEP enrollment, including automatic and test
activation, OS/build comparison, and removal. The temporary declarations are
absent from both definitions and reported status. Private consolidated evidence
is `candidates/scep-ber-fix/accepted-scep-tests.json`. This completes the initial
ACME/SCEP Mac checkpoint; final acceptance must still run on the reviewed commit.

## Identity replacement checkpoint

The operator approved SCEP identity replacement and server restart. The first
attempt failed at 21:08 UTC before issuance. The Mac reported an InstallProfile
error and retained its original identity; device and exact-user commands passed
after the refusal. An isolated database/configuration copy accepted issuance,
and Apple's native SCEP API preserved a synthetic 96-byte subject. Neither probe
reproduced the macOS profile installer's behavior.

A fresh diagnostic attempt at 21:21 UTC established that the profile installer
truncated the 96-byte common name to 64 bytes, leaving only four characters of
the attempt UUID. The server correctly refused the mismatched attempt. Both
channels again remained usable with the original certificate. These failures
are preserved under `candidates/scep-identity-replacement` and
`candidates/replacement-diagnostic`; they are not controlled fault-injection proof.

New replacements use a 46-byte common name containing the attempt UUID. A
persistent protocol-state record binds that reference to the device. The
binding remains available across later attempts and restarts; the pending
attempt, challenge, exact CSR, candidate certificate and channel checks still
authorize issuance and admission. Previously issued legacy subjects remain
readable. Tests exercise the subject limit, separate state readers, unavailable
or corrupt state, challenge refusal and attempted rebinding to another device.

The corrected candidate committed a new SCEP identity at 21:26:28 UTC with
Authenticate, acknowledgement and fresh device/user TokenUpdate evidence.
LIVE-003 passed before restart and again at 21:27 UTC after restarting the same
binary. LIVE-004 also passed after restart, including removal of its temporary
declarations. macOS still reports user-approved MDM enrollment. General re-enrollment
remained disabled throughout. Evidence and consistent database backups are under
`candidates/replacement-subject-fix/accepted-replacement.json`. The full reference
application test package passed with the race detector; the isolated candidate's
application package also passed lint. Combined-branch lint still reports issues
in the ongoing persistent event changes and remains an outstanding gate.
This proves identity replacement under the
existing issuer; issuer/HTTPS trust overlap, controlled issuance refusal and the
full recovery drill remain separate gates. Repeat on the final reviewed commit.

## Issuer, HTTPS trust and recovery checkpoint

The operator authorized the remaining rollover and recovery tests. A private
candidate adds a replacement-only SCEP issuance refusal after resolving the
device/attempt binding and before certificate signing. Its source and binary
hashes are recorded separately; this fault switch is absent from repository
source. Ordinary check-in and initial enrollment paths are unchanged.

At 21:46 UTC the Mac's replacement request reached that deliberate refusal.
macOS reported failure, no candidate certificate was issued, and its prior
certificate remained pinned. LIVE-003 passed at 21:47 UTC. The issuer worker
recorded a blocked migration requiring explicit retry. After a consistent
checkpoint and graceful server restart, the failed attempt, blocked migration
and certificate pin were identical; LIVE-003 passed again at 21:49 UTC.
The refusal was then disabled and the migration explicitly retried.

The cohort also includes one synthetic offline enrollment signed by the old
issuer, with no APNs credentials. It tests the server's retirement guard; it is
not evidence from a second physical Mac. The initial stale check-in on the real
Mac triggered the normal one-hour deferral. After fresh inventory and user
commands, an explicit retry resumed that Mac before any trust command or
replacement attempt had been created.

The explicit retry committed a new identity under issuer revision 2. Its migration
was confirmed at 21:54 UTC and LIVE-003 passed at 21:55 UTC. Retiring revision 1
returned HTTP 409 while the offline fixture remained enabled.

The HTTPS CA successor was prepared next. Its public certificate was exported over
the verified administrative connection, matched against its workflow fingerprint,
and appended to the administrative trust bundle before HTTPS activation. At
21:58 UTC a fresh device ProfileList reported the issuer and HTTPS trust profiles
as managed, each containing the old and new CA payload identifiers. LIVE-004
passed during this overlap. At 22:02 UTC the Mac's HTTPS migration was confirmed,
the old HTTPS leaf was still active, and old-CA retirement returned HTTP 409.

The HTTPS fixture initially lacked OS inventory, so command eligibility blocked
its trust installation. Adding synthetic Mac inventory and explicitly retrying
only that fixture produced a pending trust command with zero deliveries. It still
had no APNs credentials or recent check-in. That unacknowledged command was
captured before disabling the fixture and requesting reconciliation. A private
evidence collector also initially looked for replacement commands in the ordinary
queue; those commands use the separate replacement store. Correcting the collector
preserved the original trust acknowledgements and terminal replacement records.

Both workflows completed at 22:04 UTC after the fixture was disabled. The new HTTPS
leaf verified for the original IP address using only the successor HTTPS CA. The
fault candidate was stopped and the normal candidate restarted, forcing fresh
connections. LIVE-003 passed at 22:05 UTC and LIVE-004 passed before retiring the
old enrollment issuer and HTTPS CA.

The normal server was stopped and drained before capturing a consistent SQLite
checkpoint with its external keys, original key IDs, configuration, secret files,
admission policy and administrative trust. The archive was encrypted using age
v1.3.2. Isolated verification authenticated the archive, checked every member hash,
checked SQLite integrity and foreign keys, and passed the candidate's local
`setup check` without listeners or workers. It rejected an occupied restore target,
wrong archive identity, altered or truncated ciphertext, empty/wrong storage keys,
and identical key material under a changed storage key ID.

The verified archive was restored into the previously absent
`managed/recovered-20260913` directory. The canonical `managed/setup.json` now points
at that restored database and secrets, retaining the original public URL. After
startup, the accepted device certificate, both channel token timestamps, HTTPS
leaf and completed rollover records matched the checkpoint. LIVE-003 passed at
22:06 UTC and LIVE-004 passed at 22:07 UTC, including removal of its temporary
declarations. macOS still reports user-approved enrollment. The retired revision's
SCEP route returns 404, the legacy route returns 410, and revision 2 returns 200.

Private evidence is under
`candidates/issuer-https-rollover/accepted-rollover-recovery.json`. The normal
candidate remains running against the restored state with general re-enrollment
disabled. No enrollment profile removal or manual reinstall was required. The
synthetic fixture remains disabled for audit evidence. Old CA root payloads remain
in installed profiles; retirement closes server trust and issuance, and does not
perform device profile cleanup. The private SQLite drill does not replace the
planned public backup/verify/restore commands or their three-backend acceptance.

## Public recovery validation — 14 September

The public workflow is documented in [server recovery](../operations/recovery.md).
`dmctl recovery` now provides key generation, pause/status/resume, stopped-process
cleanup, backup, verification and restore. Maintenance ownership and process drain
acknowledgements use persistent state; a timeout retains the fence and its ticket.

The native SQLite, PostgreSQL and MySQL recovery contracts exercise authenticated
archives, original storage keys and row bindings, enrollment pins and push tokens,
empty-target checks, paused restoration, and explicit resume. The PostgreSQL and
MySQL tests also exercise the complete public deployment API. Restoration retains
cursor high-water marks after pruning and rejects a cursor behind a restored row
before committing data. The CLI round trip additionally compares issuer material.

Failure tests cover malformed or incomplete authenticated archives, a damaged final
encryption tag, wrong keys, swapped row/column bindings, unsafe paths, competing
maintenance ownership, transaction loss and rollback, failed webhook delivery,
and cleanup of plaintext verification staging. Tests use synthetic temporary data.

The local validation checkpoint passed the 95% overall and per-package coverage
gate at 95.77%, full unit/race tests, all eight OS 27 schema contracts, generation
verification, lint, gosec SARIF, reachable-vulnerability checks, independent server
module builds and command installation, end-to-end suites on SQLite/PostgreSQL/
in-memory storage, and reference-server acceptance. Final CI and backend rerun
results are recorded on draft PR #55.

The live Mac continues to use its existing recovered deployment. Public recovery
tests do not establish a new physical-Mac or off-machine recovery result. The
remaining event/remote-operation work, public certificate orchestration, external
runtime example, patch release verification and final Mac acceptance remain on
the combined PR's checklist.

## Acceptance and operator boundaries

This is a preproduction schema. At the operator's request, the audit occurrence
ID and unique index belong to `0001_init.sql` for each backend. No separate
`0002_event_id` migration or compatibility layer is part of this change.

All required live results must be tied to the reviewed commit. Profile removal,
installation and trust changes require an explicit operator handoff with a
prepared artifact and recovery procedure. No device erase is part of this work.
Private evidence, identities and backups stay under `test-lab/local/certs`.

The existing 95% coverage gate, race tests, SQL integration, security scans,
generation checks and published-module checks remain required. The combined PR
stays draft until the automated and agreed live scenarios pass.
