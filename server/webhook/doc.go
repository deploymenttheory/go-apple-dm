// Package webhook implements the reference server's native webhook contract,
// subscription management, encrypted capture and receiver authentication.
//
// Delivery uses eventstore's persistent leases. Payloads are immutable for a
// delivery; replay creates another delivery for the same occurrence. Network
// delivery is at least once and unordered. Consumers deduplicate delivery IDs.
// Full decoded bodies and original bytes require explicit sensitive-webhook
// authority supplied by the host. The reference server evaluates Cedar actions
// for sensitive subscription management and replay separately from ordinary
// route authorization. The boolean named root in the API carries this decision;
// it does not authenticate a principal or evaluate policies itself.
//
// Managed delivery requires SQL persistence and an encryption keyring. Signing
// secrets are returned only on creation or rotation; payload tokens authorize
// receiver retrieval of retained payloads. Retention and replay preserve the
// disclosure ceiling of the selected captured representation.
//
// # References
//
//   - Webhook contract: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/webhooks.md
//   - Authorization: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/internal/app/webhooks.go
//   - Delivery implementation: https://github.com/deploymenttheory/go-apple-dm/blob/main/server/webhook/transport.go
//   - Cedar authorization: https://docs.cedarpolicy.com/auth/authorization.html
//   - RFC 2104 (HMAC): https://www.rfc-editor.org/rfc/rfc2104
//   - This webhook envelope is a project contract, independent of Apple's device-facing MDM and declarative management protocols.
package webhook
