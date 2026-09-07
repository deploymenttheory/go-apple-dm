# 0010: Over-the-air profile service (two-phase enrollment)

## Context

Apple's over-the-air Profile Service protocol distinguishes requests signed by a device identity from requests signed by the issued enrollment identity.

## Decision

`OTAService` verifies attached CMS and classifies the signer as `PhaseDevice` or `PhaseIdentity` according to its trusted CA. `Authorize` and `Profile` callbacks receive the phase, attributes and signer. The handler bounds the body, checks methods and serves the returned profile with the appropriate content type.

## Rationale

Protocol-phase classification lets callers apply the device challenge during the first stage and the issued identity during the second. Keeping profile selection and admission in callbacks supports deployment-specific policy.

## Constraints

The phases are part of the OTA protocol. Callers supply the trusted device and identity CA pools and must implement the authorization callback required by their deployment.

## Verification

OTA tests cover signer classification, untrusted CAs, failed challenges and callback errors. The simulator and end-to-end OTA scenario exercise the profile exchange.

## References

- [mdmprotocol/enroll](../../../mdmprotocol/enroll)
- [simulator](../../../simulator)
- <https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles>
- <https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/>

Reference source identifiers and paths (relative to the named project):

- `micromdm/micromdm@main`, `mdm/enroll/service.go`, `mdm/enroll/endpoint.go`, `pkg/crypto`
- `micromdm/scep`
- `fleetdm/fleet`
