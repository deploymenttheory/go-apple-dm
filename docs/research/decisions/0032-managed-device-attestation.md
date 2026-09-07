# 0032: Managed Device Attestation: parsing, verification, and policy

## Context

Managed Device Attestation can establish properties of Apple hardware and bind a certificate request to an attested key.

## Decision

The verifier checks the certificate chain to configured trust anchors, requires the expected freshness extension, parses documented property encodings and compares the attested key with the key being certified. Identity/version properties use Apple's string encoding; integrity properties use DER integers with their documented interpretation. Malformed values fail verification; empty values are treated as absent.

The same verifier reads ACME attestation objects and MDM `DevicePropertiesAttestation` certificate chains. The default Apple root has a recorded source and fingerprint. The CBOR decoder accepts only the bounded subset needed by the object format.

## Rationale

Verifying chain, freshness and key binding prevents trusting unauthenticated properties or certifying a different key. Shared verification keeps both protocol entry points consistent.

## Constraints

User Enrollment may omit serial number and UDID; policy decides whether unidentified or unattested requests are allowed. MDM attestation may be cached by the device, unlike ACME's per-challenge freshness. A valid attestation is not organizational ownership authorization. Intermediate certificates can rotate and are supplied in the chain.

## Verification

Tests cover foreign/expired chains, missing intermediates, absent/wrong freshness, key mismatch, every modeled property encoding and malformed CBOR/extensions. ACME tests check binding to expected device identity; end-to-end tests cover both entry points.

## References

- [pki/acme/attest](../../../pki/acme/attest)
- [pki/acme/attest/attesttest](../../../pki/acme/attest/attesttest)
- [internal/cbor](../../../internal/cbor)
- <https://developer.apple.com/documentation/devicemanagement/acmecertificate>
- <https://developer.apple.com/documentation/devicemanagement/deviceinformationresponse>
- <https://support.apple.com/guide/deployment/managed-device-attestation-dep28afbde6a/web>
- <https://www.apple.com/certificateauthority/private/>

Reference source identifiers and paths (relative to the named project):

- `brandonweeks/nanoca@df2dba6c`, `verifiers/apple/apple.go`, `webauthn.go`, `handlers.go`
- `certutil/certutil.go`, `AttestationVerifier`
- `smallstep/certificates@bb481fbf`, `acme/challenge.go`, `acme/order.go`
- `authority/provisioner/acme.go`, `doAppleAttestationFormat`
- `fleetdm/fleet@e1bbd21c`, `server/mdm/acme/internal/service/challenge.go`
- `hslatman/ios-acme-simulator@8373a8f9`, `main.go`
- `zentralopensource/zentral@902e596c`, `zentral/contrib/mdm/cert_issuer_backends/__init__.py`
