# macOS 27 VM incident review

Reviewed **17 September 2026** against open and closed UTM/Tart issues, their
comments and linked patches. Searches covered macOS 27, Golden Gate, APNs, MDM,
guest provisioning, keychain errors and 27.2. Older closed incidents below are
included where their symptoms overlap; they are not macOS 27 fixes. Issue status
is the status at review time, not proof that a workaround is effective.
The two direct APNs reports, UTM #7819 and Tart #1320, were rechecked on
**18 September**; both remained open with no new comments or reported fix.

Our [live results](macos27-live-validation.md#guest-continuation--17-september-2026)
show native provisioning and same-identity Tart boots still failing APNs on 27.0.
The same host can deliver real pushes to a 26.6.2 guest. Upgrading that working
guest to 27.0 preserves enrollment but breaks new push delivery, including after
a cold boot with working HTTPS. No reviewed upstream report establishes a fix
for that complete failure sequence. The subsequent
[27.2 beta upgrade](macos27-live-validation.md#macos-272-beta-result) also failed
reliable independent push delivery on the same 27.0 host.

## APNs and guest identity

| Incident | State | What the discussion or patch establishes | Application to this lab |
|---|---|---|---|
| [UTM #7819](https://github.com/utmapp/UTM/issues/7819) | Open | Reporter relays Apple's feedback that Virtualization guest provisioning is needed for VM identity and APNs. Subsequent comments attach logs/configuration; no resolution. | Explains the first implementation gap, but does not show that provisioning alone resolves APNs. |
| [UTM #7757](https://github.com/utmapp/UTM/issues/7757) | Open | Requests the new provisioning API and links Tart's implementation. | Relevant API requirement; not a tested APNs repair. |
| [Tart PR #1263](https://github.com/openai/tart/pull/1263) and [PR #1294](https://github.com/openai/tart/pull/1294) | Merged | Adds first-boot provisioning through `VZMacOSVirtualMachineStartOptions.setGuestProvisioning`; later builds with Xcode 27 to expose that API. | Compared the patch with Guestweave v1.1.0: both set the five provisioning fields and pass the options to VM startup. No additional identity switch was found. The lab already verifies account creation, automatic login and SSH. |
| [Tart #1320](https://github.com/openai/tart/issues/1320) | Open | MDM push fails despite `--provisioning-opts`, initially across 27 betas; a 10 September comment still reports failure on 27 RC. | Direct corroboration of the remaining problem after provisioning. No reported 27.2 result or fix. |
| [Apple developer thread](https://developer.apple.com/forums/thread/840500) | Investigation | Reports guest BAA/key failures; DTS requests guest diagnostics. Apple Account sign-in is not demonstrated to fix it. | Corroborates native identity failure. The suggestion to enroll on 26 before upgrading is now tested here and did not restore push on 27.0. |

The references to Apple feedback in UTM are reporter-supplied quotations, not a
published Apple diagnosis of this lab. Neither native profile installation nor
an enabled enrollment with an old TokenUpdate establishes current APNs delivery.
Searches for **27.2** in both repositories returned no matching incidents at
review time. Apple's [27.2 release notes](https://developer.apple.com/documentation/macos-release-notes/macos-27_2-release-notes)
do not list an APNs/virtualization repair. Our subsequent 26B5086k test failed
reliable APNs acceptance; this is a local result, not an upstream resolution.

A separate [Apple forum report](https://developer.apple.com/forums/thread/839343)
reproduces missing `CSIdentityQueryExecute` results for provisioned users in
Tart and VirtualBuddy. DTS confirms a tracked fix was absent from beta seeds
available when that comment was written and asks for retesting. This is another
provisioning regression, not a reported APNs fix. Our failing upgrade control's
account was created through normal macOS 26 Setup Assistant, so that specific
provisioned-account symptom cannot by itself explain the control's failure.

## Reboots, automatic check-ins and apparent recovery

The [Der Flounder report and comments](https://derflounder.wordpress.com/2026/09/15/enrolling-macos-golden-gate-27-0-0-virtual-machines-with-mdm-servers-does-not-work-correctly/)
describe a 27.0 VM receiving its remaining profiles the following morning.
Mike Boylan suggests an automatic MDM check-in, approximately daily or after
reboot, could explain progress despite an APNs failure. David Young's follow-up
explicitly says repeated reboots did not help on the first day. The comments
therefore do not demonstrate that a reboot repaired APNs or its BAA identity.
The approximate schedule is a commenter's report, not a timing guarantee for
this lab.

Apple's [MDM command protocol](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device)
separates the push notification that prompts contact from the HTTPS exchange
that retrieves commands and returns results. Once a device contacts the server,
the server can supply subsequent commands in that exchange. A device-initiated
contact can therefore explain queued work completing without proving that the
push notification caused it. The article's proposed Secure Enclave path error
remains a hypothesis; its observed BAA failures are the stronger evidence.

Our 27.2 cold-boot/login run returned an older queued result and a new result
together at 19:28:25 UTC. The new request met the 45-second deadline in 20.32
seconds, but the next two independently pushed requests timed out at 45.72 and
45.77 seconds. Current-boot BAA acquisition still failed. This is consistent
with automatic contact releasing queued commands; the initiating event was
not established. No guest-side forced polling was used. Reboot-associated or
overnight progress can be a limited way to advance queued work, but it does not
establish reliable remote management or complete an enrollment that still lacks
its required TokenUpdate.

To distinguish recovery from queued work being released, let startup/login
activity finish, queue a new uniquely tracked command, send its push, and
correlate native APNs/MDM events with the server acknowledgement. Repeat with
new commands without another reboot or forced poll. Our post-login attempts
already failed this delivery criterion; a timed acknowledgement by itself does
not prove what initiated contact.

## Closed reports with overlapping keychain errors

| Incident | State | Reported outcome | Application to this lab |
|---|---|---|---|
| [Tart #1025](https://github.com/openai/tart/issues/1025) | Closed | Host Virtualization process cannot generate a key (`-25308`) when starting a VM from SSH. Reporter confirms a graphical host login as the invoking user fixes startup. | Same error number, different failing process and phase: our VM boots and guest `mobileactivationd` fails APNs key access. Check launch context before assuming equivalence. |
| [Tart #1075](https://github.com/openai/tart/issues/1075) | Closed | Host cryptography needs an active GUI session or unlocked keychain. Reporter confirms unlocking the host keychain works. | Host is already graphically logged in as the non-root VM owner. The Terminal-launched comparison still failed fresh push delivery. No host keychain changes were made. |
| [Tart #1137](https://github.com/openai/tart/issues/1137) | Closed | Host-key creation failure: one commenter reports a non-root launcher fix; the original reporter ultimately wipes the host. Root cause remains unclear. | Closure is not evidence of a product fix. Our VM runs as the logged-in user; wiping the host is not a justified experiment. |
| [Tart #1146](https://github.com/openai/tart/issues/1146) | Open | Headless Tahoe reports remain inconsistent. A maintainer reports that an initial GUI login followed by keychain unlock can permit subsequent starts. | Another host-session lead, not proof that guest APNs is repaired. |

The successful 26 guest on this same host weakens the hypothesis that the host
keychain is generally inaccessible. The subsequent
[Terminal comparison](macos27-live-validation.md#utmtart-incident-review-and-terminal-launch-comparison)
still missed the fresh push deadline after guest login; inspection confirmed
that the guest has Recovery. The comparisons required no changes to host
auto-login, accounts, FileVault or keychain policy.

## Other macOS 27 regressions and fixes

| Incident | State | What it says | Relevance |
|---|---|---|---|
| [UTM #7746](https://github.com/utmapp/UTM/issues/7746), [#7763](https://github.com/utmapp/UTM/issues/7763), [Tart #1261](https://github.com/openai/tart/issues/1261) | Open | Early 27 IPSW restores fail around 77–78% on older hosts. Later comments report working restores with newer 26.6 hosts/Xcode 27 beta support; installing Apple's full package inside an existing guest also works without beta-account enrollment. | Supports the full-installer route already used here. Successful restore or upgrade does not prove APNs. Old host MobileDevice rollback suggestions do not fit our 27 host. |
| [Tart PR #1260](https://github.com/openai/tart/pull/1260) | Merged | Fixes Swift 6.4 main-thread/run-loop ownership that could show a VM window without booting the guest. | Our guests boot and run services; this is a distinct startup failure. |
| [UTM #7758](https://github.com/utmapp/UTM/issues/7758), [PR #7868](https://github.com/utmapp/UTM/pull/7868) | Closed / merged | Xcode 27 build fixes for ANGLE, deployment targets and Apple graphics symbols. Maintainer waived runtime validation. | Build repair, not an APNs fix or evidence that all 27 runtime behavior works. |
| [Tart #1302](https://github.com/openai/tart/issues/1302) | Open | Missing Swift compatibility library on older hosts after the Xcode 27 build change. | Our signed Tart 2.37.0 already launches on host 27. No vendor-library replacement is indicated. |
| [Tart #1300](https://github.com/openai/tart/issues/1300) | Open | Large IPSW download fails without resume; discussion requests range/ETag validation. | The lab downloader already uses verified ranges and records package integrity. Separate from guest APNs. |
| [UTM #7824](https://github.com/utmapp/UTM/issues/7824), [#7831](https://github.com/utmapp/UTM/issues/7831), [vd_agent PR #4](https://github.com/utmapp/vd_agent/pull/4) | Open; patch unmerged | Quarantined SPICE launch plists prevent guest tools/clipboard startup. Removing quarantine from the two affected guest plists is reported to work; the packaging patch broadens quarantine removal in its temporary package tree. | Relevant to UTM clipboard, not Apple's running `apsd`. This lab uses Guestweave with clipboard disabled and has no SPICE dependency. |
| [UTM #7814](https://github.com/utmapp/UTM/issues/7814) | Open | Built-in serial terminal ignores input on host 27. A 17 September comment reports using a pseudo-TTY with `screen`. | A serial-console workaround, not Mac guest keyboard or APNs behavior. |
| [UTM #7750](https://github.com/utmapp/UTM/issues/7750) | Open | Sidecar touch scrolling behaves as dragging on host 27. | No reported fix; unrelated to this transport failure. |
| [UTM #7875](https://github.com/utmapp/UTM/issues/7875) | Open | UTM 5.0.5 changes Bluetooth headphone audio behavior with host/guest 27; reporter says 4.7.5 does not. | New audio regression, no resolution in the reviewed thread. |

## Older image and networking leads

[Tart #1232](https://github.com/openai/tart/issues/1232) (closed) distinguishes
images with a removed Recovery partition from full Apple restores when an OS
update fails. [Tart #637](https://github.com/openai/tart/issues/637) (closed) also
attributes an older enrollment problem to stripped Recovery, but its comments
do not establish a controlled macOS 27 APNs fix. Our fallback comes from Apple's
full IPSW, completed a native upgrade and has a verified Recovery volume.

[Tart #1222](https://github.com/openai/tart/issues/1222) (closed) concerns Setup
Assistant after identity changes; a skip-setup profile is the reported workaround.
[Tart #311](https://github.com/openai/tart/issues/311) (closed, not planned) warns
about duplicate machine identities in concurrent clones. Our comparison retains
identity and runs the source and clone at different times. Neither thread
justifies regenerating the enrolled VM's identity.

[UTM #7658](https://github.com/utmapp/UTM/issues/7658) (open) has reports of newer
host releases fixing N1 Wi-Fi bridging, while
[Tart #921](https://github.com/openai/tart/issues/921) (closed) mixes launcher
context and Go/local-network permission failures. Those symptoms differ from
verified guest HTTPS plus successful Apple TCP reachability. Network reachability
still cannot substitute for a native BAA certificate and acknowledged push.

The Terminal comparison and authorized 27.2 upgrade are complete; neither
restored reliable APNs. The beta's one timely acknowledgement around login was
followed by two independent failures. Acceptance still requires three fresh
timed command deliveries, including after a cold boot, and native LIVE-003 /
LIVE-004 application/removal results. No reviewed closed incident supplies that
proof for this configuration. The working 26 checkpoint and stopped beta guest
are preserved.

The working diagnosis is an Apple platform regression in the guest's native
APNs identity handling. A repair to that Apple implementation must come from
Apple; no reviewed evidence supports changing this MDM server to repair BAA key
creation or access. Apple has not confirmed the precise root cause. A supported
VM configuration workaround remains possible, and neither a beta host nor a
fresh beta restore has been tested here.
