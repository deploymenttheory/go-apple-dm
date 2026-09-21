# 0054: FileVault encryption identities

## Context

FileVault rotation responses and recovery-key escrow require a retained recipient
private key. Losing or replacing it after enqueueing makes dependent encrypted
material unrecoverable, including replies received after a restart or expiry.

## Decision

`server/replycerts.Manager` generates a distinct RSA-2048 key and self-signed X.509
certificate entirely in Go. The reference admin workflow requires encrypted
persistent SQL and commits the binding before enqueueing. Persistence failure
prevents enqueueing; a queue failure may leave a prepared binding for retry.
Embedders using `service.Core` directly call the manager before enqueueing.

The full enrollment identity and command UUID select a binding. A digest of the
normalized command rejects conflicting retries. Atomic state updates make
concurrent preparation reuse the same key. External encryption certificates are
rejected; a retry may include the identical certificate already generated.
Escrow-profile bindings also retain stable profile and payload UUIDs. Preparation
idempotency does not replace the queue's existing command-UUID conflict rules.

Personal rotation supplies `ReplyEncryptionCertificate`; institutional rotation
supplies `NewCertificate`. `EscrowProfile` creates a system profile containing the
certificate and its referencing `com.apple.security.FDERecoveryKeyEscrow` payload.
The admin escrow route authorizes `enqueueCommand.InstallProfile` for an enabled device enrollment.
It prepares and queues; the caller checks prerequisites, pushes, tracks the result,
retrieves encrypted material and persists the recovered secret.

Certificates last one year. Bindings have no automatic expiry or renewal.
`Recipient` returns the exact retained identity even after expiry; `Forget` is an
explicit caller decision after recovery or deliberate abandonment. An acknowledged
escrow profile can continue producing encrypted material, so retain its key while
the profile or any encrypted recovery key depends on it. There is no admin key
export route. Backup must preserve both SQL state and its original named storage keys.

## Rationale

Persisting before enqueueing closes the gap between device delivery and recipient
retention. Stable retry identities preserve decryptability without sharing keys
between commands. Certificate validity governs issuance/use on the device; it is
not a safe deletion deadline for an existing decryption key.

## Constraints

Explicit rotation still requires Apple's FileVault unlock credentials. The macOS
26 escrow flow requires an existing personal recovery key and bootstrap token;
it does not substitute for rotation-command credentials. Check installed escrow
profiles and command access rights before changing ownership. Remote SecurityInfo
retrieval, documented local-file extraction, CMS decryption and disk unlock are
distinct acceptance checks. Removing an escrow profile does not restore the old
recovery key. CMS decryption alone does not authenticate the sender.

## Verification

Manager tests cover concurrent preparation, conflicting retries, storage failure,
encrypted SQL persistence, reopening and delayed replies after expiry. CMS tests
use independent BER/DER fixtures and check recipient selection and wrong keys.
Follow the [workflow](../../operations/protocol-helpers.md#automatic-filevault-encryption-certificates)
and [live acceptance checks](../../testing/bench.md#apple-management-feature-checks).

## References

- [Manager and tests](../../../server/replycerts)
- [CMS decryption](../../../devicemanagement/mdmprotocol/cms/envelope.go)
- [Backup and recovery](../../operations/recovery.md)
- [Rotation certificate fields](https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeycommand/command-data.dictionary)
- [Encrypted rotation result](https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeyresponse/rotateresult-data.dictionary)
- [Escrow payload and retrieval](https://developer.apple.com/documentation/devicemanagement/fderecoverykeyescrow)
- [macOS 26 bootstrap-token behavior](https://support.apple.com/en-us/124963)
- [RFC 5652 EnvelopedData](https://www.rfc-editor.org/rfc/rfc5652.html#section-6)
