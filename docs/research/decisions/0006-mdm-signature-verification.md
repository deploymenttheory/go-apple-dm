# 0006: Mdm-Signature verification and identity pinning

## Context

An MDM request can carry a detached CMS signature in `Mdm-Signature`. A valid signature must also be associated with an authorized enrollment identity.

## Decision

`cms.Verify` requires one signer, verifies content and signature, and accepts explicit trust roots and a clock. Configured clock-skew tolerance applies to signing-time checks at certificate validity boundaries without skipping signature or chain verification.

HTTP middleware extracts the certificate from CMS, TLS, or a configured trusted proxy header. The service applies pinning and certificate reuse policy. Optional certificate status enforcement runs independently of pin mode (record 0047).

During an authorized [profile replacement](0009-enrollment-profiles.md), issuance
binds a candidate fingerprint to one device and pending attempt. SCEP uses a
single-use challenge bound to the CSR key; ACME uses its authenticated identifier
and attestation policy. The candidate can complete the replacement handshake
but cannot fetch its credential-bearing profile or unrelated commands. A
replacement certificate cannot authenticate another device ID. Failure, expiry
or cancellation removes candidate authority while retaining the existing pin.

## Rationale

Separating extraction, cryptographic verification and enrollment authorization gives each transport the same service policy. Tolerating a configured signing-time skew supports freshly issued identities on devices with clock differences.

## Constraints

A signature proves possession of a key; it does not prove ownership of the enrollment identifier. Configure trust roots and secure the proxy boundary. Traditional MDM error handling avoids 401; recognized account-driven sessions can return Apple's reauthentication challenge. Unknown-enrollment responses are configurable.

## Verification

CMS tests cover skew, chains, multiple signers, tampered content and malformed input. HTTP and service tests cover identity transports, pinning, rotation, reauthentication and revocation.

## References

- [mdmprotocol/cms](../../../mdmprotocol/cms)
- [server/httpapi](../../../server/httpapi)
- [server/service](../../../server/service)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@main`, `cryptoutil/cryptoutil.go`, `VerifyMdmSignature`, `http/mdm/mdm_cert.go`, `service/certauth/certauth.go`
- `smallstep/pkcs7@main`, `verify.go`, `pkcs7.go`, `verifySignatureAtTime`, `SigningTimeNotValidError`
