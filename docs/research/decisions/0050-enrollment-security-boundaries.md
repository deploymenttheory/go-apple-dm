# 0050: Enrollment security boundaries

## Context

Transport authentication proves certificate possession. A management service
also needs organizational admission, stable device/account association and
atomic lifecycle transitions. Apple requires a certificate-to-device association;
CMS is optional and verified direct mTLS is supported.

## Decision

Every raw enrollment ID has one immutable channel and parent, including pending
user-authentication handshakes and DDM preassignments. Storage validates the full
identity on reads and writes. Administrative Cedar authorization resolves the
stored identity before evaluating policy. Authenticate atomically validates and
updates the pin, history and lifecycle; same-certificate retries preserve state.
Disabled enrollments and children of disabled devices cannot obtain commands,
secrets or declarations, or reactivate via TokenUpdate.

Reference profile delivery and credential issuance require explicit device or
account admission. Empty policy denies. Verified IdP issuer/subject mappings
select the managed account; configured email mappings require verified email.
SCEP credentials expire within one hour and bind atomically to the first valid
CSR. Same-CSR retries return the same certificate. ACME rechecks admission at
finalization while preserving Apple attestation verification. Persistent CA
material and encrypted storage are required for persistent enrollment servers.

Verified TLS chains establish direct mTLS identity. Certificate forwarding
requires configured roots and trusted socket peers, header sanitization and a
protected backend. Conflicting certificate evidence is rejected. Browser OIDC
state requires a per-flow host cookie and shared atomic storage. Outbound
credential clients refuse redirects. Reference revocation is enabled by default.

Command eligibility uses tri-state recorded capabilities; unknown supervision,
ADE or user approval cannot satisfy requirements. Reference SCEP payloads prohibit
private-key extraction and unrestricted app access. Status limits reject deep,
wide or excessively long-path JSON before updates. Private DDM envelopes bind
requests and responses and reject stale or replayed requests over required HTTPS.

## Rationale

Admission, certificate possession and enrollment association answer distinct
questions. Atomic storage contracts make decisions hold across replicas and
failures. Conservative unknown capabilities avoid inventing device privileges.
Purpose/row authenticated encryption covers retained credential-bearing data as
well as dedicated secret fields.

## Constraints

SCEP credentials are bearer credentials before first use; supported hardware
attestation is needed to prove device hardware properties. DEP admission uses
synchronized inventory. Account association confirmation crosses separate stores
and retains a reservation for safe retry. Administrative exports and database
metadata require separate operational protection. Physical Apple-device
interoperability is outside simulator acceptance.

## Verification

Shared memory/SQLite/PostgreSQL/MySQL contracts cover identity collisions,
competing authentication, lifecycle gates and capability provenance. Regressions
cover browser transfer, cookie/state replay, private-hop substitution and replay,
raw SQL ciphertext inspection/rotation, issuance admission and CSR competition,
TLS/proxy evidence and credential redirects. See the
[validation record](../../wip/security-audit-implementation-plan.md).

## References

- [Apple certificate management](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices)
- [Apple SCEP payload](https://developer.apple.com/documentation/devicemanagement/scep/payloadcontent-data.dictionary)
- [Apple managed device attestation validation](https://developer.apple.com/documentation/devicemanagement/validating-a-managed-device-attestation-attestation)
- [RFC 9440: client certificate forwarding](https://www.rfc-editor.org/rfc/rfc9440.html)
- [RFC 9700: OAuth security](https://www.rfc-editor.org/rfc/rfc9700.html)
- [Enrollment security operations](../../operations/enrollment-security.md)
