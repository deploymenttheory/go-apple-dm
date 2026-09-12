# 0033: ACME identity in enrollment profiles, declarative credentials, and the reference server

## Context

Enrollment profiles and declarative credentials can both request ACME identities, with different device information available at composition time.

## Decision

The profile builder validates key size, hardware binding and attestation combinations before signing. Client identifiers are minted for the requesting context: ADE binds to `MachineInfo`, while account-driven enrollment initially identifies an account rather than attested hardware.

The declarative credential endpoint requires an enrolled certificate identity and binds its identifier to that device. Issued certificate records retain attested properties separately from the subject. SCEP remains the reference server's default identity choice; ACME is selected explicitly. Enrollment-enabled compositions mount ACME endpoints so declarative credentials can use them with SCEP enrollment.

## Rationale

A common identifier and verifier path ties issuance to its authorization context. Storing attested properties preserves their provenance without interpreting issuer-written subject names as attestation.

## Constraints

Mac credential flags follow current Apple documentation: macOS 14+ Apple
silicon supports hardware binding and attestation; T2 supports hardware binding
without attestation; other Intel hardware requires software keys. Known target
context controls selection. Unknown secondary Mac hardware uses software keys
until capabilities are established. A five-minute grant binds the existing
identity and the requested attestation; challenge and finalization recheck it.
`Binding.RequireAttestation` also binds the signed client identifier and prevents
an unattested policy from overriding that request. Initial enrollment retains
its attestation requirement unless the deployment explicitly authorizes software
identities. See [operations](../../operations/enrollment-security.md) and the
[current Apple source comparison](../../wip/apple-enterprise-hardening-2026-09-11.md).
Persistent CA, protocol state and identifier keys are deployment requirements.
Simulator tests do not establish physical-device compatibility.

## Verification

Enrollment tests cover valid/invalid ACME payload combinations and profile round trips. Application tests cover device binding, credential authentication and default identity selection. End-to-end scenarios cover attested enrollment and declarative credentials.

## References

- [mdmprotocol/enroll/enroll.go](../../../devicemanagement/mdmprotocol/enroll/enroll.go)
- [pki/acme](../../../devicemanagement/pki/acme)
- [server/internal/app/acme.go](../../../server/internal/app/acme.go)
- <https://developer.apple.com/documentation/devicemanagement/acmecertificate>
- <https://developer.apple.com/documentation/devicemanagement/assetcredentialacme>
- <https://developer.apple.com/documentation/devicemanagement/securityidentity>
- <https://developer.apple.com/documentation/devicemanagement/deviceinformationcommand>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@e1bbd21c`, `server/mdm/apple/apple_mdm.go`
- `articles/testing-apple-device-attestation-without-a-commercial-ca.md`
- `zentralopensource/zentral@902e596c`, `zentral/contrib/mdm/payloads.py`
- `cert_issuer_backends/__init__.py`, `test_acme_payload`
- `public_views/mdm.py`, `ACMECredentialView`
