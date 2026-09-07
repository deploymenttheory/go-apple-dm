# 0008: SCEP endpoint and pluggable CA

## Context

Enrollment profiles can request an identity certificate through SCEP. Issuance and renewal need caller-controlled authorization and certificate policy.

## Decision

`ca.Signer` and `ca.Depot` separate signing from certificate storage. `ca.Local` applies configured validity, usage and SAN policy and allocates random serial numbers. SCEP uses `smallstep/scep` for message parsing and response construction, with static, one-time and expiring HMAC challenge implementations and a CSR-verification hook.

A renewal can skip the challenge only after verifying the existing signer against the CA and the CSR subject. When status enforcement is configured, a revoked, expired or unknown signer cannot bypass it with a challenge. Trusted issuance callbacks register certificates before returning them to the client.

## Rationale

Policy hooks support different enrollment admission rules without coupling the protocol implementation to a CA deployment. A shared client exercises issuance from the device side.

## Constraints

The SCEP recipient certificate must support RSA envelope decryption. Static and HMAC challenges are not one-time credentials; account-driven profiles use a separate credential bound to the first CSR. Operators supply persistent CA material for persistent deployments.

## Verification

CA and SCEP tests cover policy, serial generation, challenge consumption/expiry, renewal identity checks, CSR rejection, client enrollment and optional revocation checks.

## References

- [pki/ca](../../../pki/ca)
- [pki/scep](../../../pki/scep)
- [pki/revocation](../../../pki/revocation)
- <https://developer.apple.com/documentation/devicemanagement/scep>
- <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>

Reference source identifiers and paths (relative to the named project):

- `smallstep/scep@main`, `scep.go`, `ParsePKIMessage`, `DecryptPKIEnvelope`, `Success`, `Fail`, `NewCSRRequest`, `CACerts`, `DegenerateCertificates`
- `micromdm/scep@main`, `server/service.go`, `server/csrsigner.go`, `depot/depot.go`, `csrverifier`
- `jessepeterson/mysqlscepserver`
