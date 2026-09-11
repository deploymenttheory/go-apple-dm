# 0031: ACME server, client identifiers, and the ACME state store

## Context

Apple's ACME payload orders an identity using a `permanent-identifier` and can request device attestation before certificate issuance.

## Decision

The server implements directory, nonce, account, order, authorization, challenge, finalize and certificate endpoints. HMAC client identifiers carry expected device binding and expiry and are claimed atomically with the first order. Nonces are one-use, expiring and prunable. JWS `url` is checked against the configured public URL rather than request-host headers.

Client validation failures settle the challenge/order as appropriate; infrastructure or policy lookup errors leave retryable state. A bad CSR leaves the order ready. Optional revocation support advertises `revokeCert` only when configured. `keyChange` is not implemented.

Public ACME URLs require HTTPS. Existing order transitions use `Store.UpdateOrder`
to lock before reading. Pure signing commits a CSR-bound certificate receipt and
`processing` state before idempotent registry/depot callbacks. A successful
registration commits `valid` and enables certificate download. Order polling and
same-CSR finalize retries can complete a persisted receipt after a restart;
admission and attested key checks apply again. Delayed challenge results cannot
overwrite completed issuance. This implements the project's one-order issuance
policy, which Apple permits through `ClientIdentifier`; Apple does not mandate
this particular locking or receipt design. See the [evidence record](../../wip/apple-conformant-security-hardening-2026-09-11.md).

## Rationale

Bound identifiers authorize a specific enrollment attempt without relying on a public serial number. Transactional state prevents competing requests from consuming the same grant. Distinct client and server errors preserve recoverable orders.

## Constraints

Supported identifiers and JWS forms are intentionally bounded. Revocation requires issuance provenance and the registry described in record 0047. Protocol state and external registration calls do not form a distributed transaction: callers must supply a pure signer and idempotent registration, and all replicas must share issuer material and policy. Stop older writers before deploying the changed store contract. Completed legacy records remain readable without a SQL migration.

## Verification

ACME tests cover identifiers, competing orders, nonce replay/expiry, published URL validation, size limits, challenge failures and CSR retries. All state backends run the same contracts. The simulator uses `golang.org/x/crypto/acme` as an independent client.

## References

- [pki/acme](../../../pki/acme)
- [storage/acme](../../../storage/acme)
- [server/acmestore](../../../server/acmestore)
- <https://developer.apple.com/documentation/devicemanagement/acmecertificate>
- <https://developer.apple.com/documentation/devicemanagement/identity-management>

Reference source identifiers and paths (relative to the named project):

- `brandonweeks/nanoca@df2dba6c`, `ca.go`, `handlers.go`, `jose.go`, `storage.go`, `machine.go`
- `smallstep/certificates@bb481fbf`, `acme/api/middleware.go`, `acme/api/order.go`
- `acme/order.go`, `acme/db/nosql/nonce.go`, `acme/errors.go`
- `authority/provisioner/acme.go`, `url`
- `fleetdm/fleet@e1bbd21c`, `server/mdm/acme/internal/service/`, `server/mdm/acme/api/http/`
- `articles/testing-apple-device-attestation-without-a-commercial-ca.md`
- `hslatman/ios-acme-simulator@8373a8f9`, `main.go`
