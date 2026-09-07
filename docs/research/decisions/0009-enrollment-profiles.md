# 0009: Configuration profiles and the enrollment profile builder

## Context

Enrollment profiles combine an MDM payload with an identity payload and optional trust anchors. Payload identity must remain stable when a profile is updated.

## Decision

`profile.Profile` composes generated payloads with explicit identifiers and UUIDs. `profile.Sign` creates attached CMS signatures; `profile.Parse` reads signed or unsigned profiles into registered payload types.

`enroll.Profile` builds the MDM configuration with SCEP, ACME or a pre-issued PKCS #12 identity, optional root certificates, access-right bits and server capabilities. Signing requests is enabled by default. The builder validates before returning the profile.

## Rationale

Explicit UUIDs allow callers to retain payload identity across content changes. Generated payloads keep structure and validation tied to the schema. A common builder applies enrollment options consistently.

## Constraints

Callers must preserve identifiers and UUIDs when updating existing profiles. Attached profile signatures and detached `Mdm-Signature` request signatures are different CMS forms. Device installation remains subject to platform and enrollment restrictions.

## Verification

Profile tests cover composition, parsing, stable UUIDs and attached signing. Enrollment tests cover identity selection, rights, capabilities and invalid payload combinations.

## References

- [mdmprotocol/profile](../../../mdmprotocol/profile)
- [mdmprotocol/enroll](../../../mdmprotocol/enroll)
- <https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys>
- <https://developer.apple.com/documentation/devicemanagement/mdm>
- <https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/cfgprofiles`
- `micromdm/micromdm@main`, `platform/profile`, `mdm/enroll`
- `deploymenttheory/go-sdk-appleservices`, `device_management/mdm/profile.go`
- `hslatman/mobileconfig-signer`
