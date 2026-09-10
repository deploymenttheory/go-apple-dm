# 0009: Configuration profiles and the enrollment profile builder

## Context

Enrollment profiles combine an MDM payload with an identity payload and optional trust anchors. Payload identity must remain stable when a profile is updated.

## Decision

`profile.Profile` composes generated payloads with explicit identifiers and UUIDs. `profile.Sign` creates attached CMS signatures; `profile.Parse` reads signed or unsigned profiles into registered payload types.

`enroll.Profile` builds the MDM configuration with SCEP, ACME or a pre-issued PKCS #12 identity, optional root certificates, access-right bits and server capabilities. Signing requests is enabled by default. The builder validates before returning the profile.

Initial profiles contain only enrollment prerequisites. In the reference server,
`DM_ENROLL_TLS_ANCHOR_FILE` selects the certificates needed to trust the HTTPS
endpoint independently of the client-identity issuer and incoming ADE/OTA
signature anchors. Publicly trusted HTTPS needs no additional trust payloads.
The common profile API supports ACME and SCEP, advertises the supported macOS
per-user capability and retains profile, MDM and trust-payload UUIDs in protocol
state. Recorded templates omit issuance secrets.

An enrollment-scoped administrative action prepares a profile replacement with
a fresh client identity. It retains the original profile's identifier, rights,
capabilities, topic and endpoints. The attempt owns its `InstallProfile` command
and expires after 30 minutes. Candidate Authenticate and TokenUpdate messages
are staged; the active identity changes only after the candidate's device
TokenUpdate and acknowledgment of the delivered command, in either order.
Cancellation, expiry and device-reported failure preserve the working enrollment.
Ordinary re-enrollment remains governed by its existing policy.

## Rationale

Explicit UUIDs allow callers to retain payload identity across content changes. Generated payloads keep structure and validation tied to the schema. A common builder applies enrollment options consistently.

## Constraints

Callers must preserve identifiers and UUIDs when updating existing profiles. Attached profile signatures and detached `Mdm-Signature` request signatures are different CMS forms. Device installation remains subject to platform and enrollment restrictions.

Replacement requires a recorded original profile with profile-installation rights.
Candidate authorization follows [identity pinning](0006-mdm-signature-verification.md);
[storage contracts](0005-storage-interfaces.md) preserve queued work, escrow,
certificate history and user channels during the transition. Simulator recovery
does not establish macOS replacement eligibility or device-side rollback. OTA
updates after profile-signing certificate expiry require separate live evidence.

## Verification

Profile tests cover composition, parsing, stable UUIDs and attached signing. Enrollment tests cover identity selection, rights, capabilities and invalid payload combinations.

Shared bench scenarios exercise successful and failed replacement with SCEP and
attested ACME. Follow the [Mac enrollment runbook](../../operations/mac-enrollment-testing.md)
for actual installation, replacement and rollback verification.

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
