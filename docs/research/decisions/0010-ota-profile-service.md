# 0010: Over-the-air profile service (two-phase enrollment)

Status: accepted
Date: 2026-09-01
Phase: 3

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles>
- Doc: <https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/> (Profile Service payload, device attributes, phase 1 and phase 2)
- YAML: `mdm/profiles/TopLevel.yaml` (the Profile Service payload is documented only in the archive)

## References read

- `micromdm/micromdm@main` `mdm/enroll/service.go`, `mdm/enroll/endpoint.go` (OTA phase 1 verified against the Apple iPhone Device CA bundled in `pkg/crypto`)
- `micromdm/scep` client used by MicroMDM's OTA flow
- `fleetdm/fleet` (no OTA support; account-driven and ADE only)

## Known pitfalls found

- MicroMDM treats every OTA POST the same way regardless of which certificate signed it, so a phase 2 request re-runs the phase 1 logic and the challenge check has to be disabled for it.
- The archive documents the device attributes plist as a signed PKCS #7 with the content attached; using detached verification rejects every request.
- A Profile Service profile with an empty `Challenge` still installs but leaves the endpoint open to any Apple device.

## What they do

- **MicroMDM**: one handler, verifies against the Apple device CA, returns the enrollment profile with a SCEP payload on every call.

## What we do better

1. `enroll.OTAService` classifies each request by the CA the signature chains to: `PhaseDevice` for the Apple iPhone Device CA, `PhaseIdentity` for the SCEP-issued identity, so the challenge applies to phase 1 and the identity applies to phase 2 without configuration flags.
2. `Authorize` and `Profile` are callbacks with the verified request (phase, attributes, signer), which keeps challenge policy and profile selection in the caller.
3. Body limits, method checks, and nosniff on the profile response; the simulator implements the client side so the flow is exercised end to end.

## Verified by

1. `enroll.TestOTAPhase1Verify` (phase classification, stranger CA rejected).
2. `enroll.TestOTAPhase1Verify` (bad challenge forbidden, profile error surfaced).
3. `e2e.TestE2E_OTAProfileService`.

## Rejected alternatives

- A single-phase flow that returns the MDM profile straight away: it would need the device to already trust the SCEP CA.
- Bundling Apple's device CA certificates in the library: they change; callers supply the pool.
