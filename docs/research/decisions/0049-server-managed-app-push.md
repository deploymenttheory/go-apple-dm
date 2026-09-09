# 0049: Server-managed app push credentials and sending

## Context

The certificate workflow initially demonstrated ordinary app notifications through
an independent APNs CLI client. The reference server needs to demonstrate the same
workflow and provide an administrative surface usable by acceptance and live tests.
MDM certificates and app certificates authorize different topics and operations.

## Decision

The authenticated `/admin/v1/apppush` family lists credential metadata, imports or
renews an app certificate/private-key pair, and sends alert/background notifications.
`manageAppPushCredentials` and `sendAppPush` are separate Cedar actions. MDM push
certificates continue to use the existing MDM store and `/pushcerts` routes.

The app store validates pairing, validity and the certificate's app-topic
capability before committing. Records have an atomic version and are held in the
shared transactional state backend under `apppush/v1/`. Persistent records are
sealed with the configured storage keyring; the record key is authenticated as
additional data. The complete credential record is encrypted, not just the key.
Metadata responses contain topic, validity dates and version. No key export route
is provided.

The provider keeps separate clients for development and production. Each client
isolates its connections by topic and certificate identity. Every send resolves
the stored credential; a committed replacement is visible to other processes on
their next send. Local imports retire that topic's connections immediately.
Expiration and topic authorization are revalidated by the APNs transport.

Sends require an explicit environment, topic, hexadecimal device token, push type
and JSON payload. Responses report APNs acceptance, classification, HTTP status,
reason and APNs request ID. Transport errors and private material are not serialized.
Device delivery is established separately by correlated app receipts.

## Rationale

A separate app credential namespace preserves the MDM-only storage contract.
The existing encrypted transactional backend avoids another database or external
secret service while supporting the same memory, SQLite, PostgreSQL and MySQL
composition. No token-based APNs authentication or additional push types are added.

## Constraints

Only the app topic capability authorizes alert/background sends. An ordinary app
certificate cannot become an MDM identity. Keep accepted retired storage keys until
credentials have been re-imported under the active key; the existing MDM-column
rewrap operation does not rewrite this new state namespace.

The admin API is powerful and must use normal deployment authorization. A break-glass
token retains its existing unrestricted semantics. Simulated provider certificates
only prove local mutual TLS and request handling; Apple-issued credentials and
real app registrations are still required for live acceptance.

## Verification

App credential tests cover encryption, mismatched topics, MDM rejection, renewal,
metadata and reopening. Shared APP scenarios exercise the server API; existing APNs
transport tests cover connection retirement and certificate validation. The live
adapter additionally requires a matching device receipt.

## References

- [Bench design](0048-reference-server-bench.md)
- [Bench/server operations](../../operations/reference-bench.md)
- [Encrypted persistence](0015-push-cert-store.md)
