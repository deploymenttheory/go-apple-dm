# Real Mac lifecycle implementation

Implementation branch: `feat/real-mac-lifecycle-and-recovery`.
Baseline: PR #53, merge `6fd5a46`; release tree `885e084`.

This report tracks the combined implementation. An unchecked milestone is not a
claim of compatibility or a completed feature. Required live acceptance uses the
operator's Apple silicon Mac on macOS 26.6.2.

## Milestones

- [ ] Correct installing-user enrollment and verify ACME and SCEP on the Mac.
- [ ] Verify identity replacement, enrollment/HTTPS trust rollover and recovery.
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

## Acceptance and operator boundaries

All required live results must be tied to the reviewed commit. Profile removal,
installation and trust changes require an explicit operator handoff with a
prepared artifact and recovery procedure. No device erase is part of this work.
Private evidence, identities and backups stay under `test-lab/local/certs`.

The existing 95% coverage gate, race tests, SQL integration, security scans,
generation checks and published-module checks remain required. The combined PR
stays draft until the automated and agreed live scenarios pass.
