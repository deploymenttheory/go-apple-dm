// Package dep implements Apple's device enrollment service client, token
// exchange, device synchronization and profile assignment.
//
// # Design
//
// One client supports multiple named accounts with credentials and sessions
// supplied by Store. OAuth 1.0a signing, coordinated session refresh, typed
// service errors and token-expiry checks manage account access. Requests, cached
// sessions and writebacks are bound to the account's Apple identity and OAuth
// credentials. Concurrent renewal cannot publish an old session or stale result.
//
// Syncer commits each cursor with its page after checking account identity,
// credentials and cursor revision. Full fetches record generation membership;
// only completion of the current generation tombstones absent devices. Assigner
// derives work from stored profile state and rechecks its lease, account binding
// and desired profile before saving outcomes or readback. A still-owned lease
// can preserve Apple's cooldown after a same-identity credential or target
// change; it cannot write retry state after identity replacement or lease loss.
// Local rejection cannot undo a request already accepted by Apple.
//
// StoreTokens and ordinary ImportToken calls reject established Apple identity
// or consumer-key changes. ImportOptions.Force permits replacement; an actual
// identity change requires a validated server UUID and atomically clears old
// inventory, profiles, sessions and worker state. The local account name,
// protocol version, creation time and token keypairs survive. Same-identity
// renewals retain inventory and the desired profile. Omitted identity metadata
// does not erase a known binding. These persistence and reconciliation rules
// are project policy around Apple's APIs.
//
// Custom stores must hold Tx.LockAccount until transaction completion even when
// the account is absent and ErrNotFound is returned, including delete/recreate
// transitions. The SQL implementation uses schema 3's stable account-name locks.
//
// Persistence implementations live in storage/dep and server/depstore. The
// separate axm package implements the Apple Business Manager and Apple School
// Manager APIs, and mdmprotocol/enroll/ade handles the device-facing Automated
// Device Enrollment exchange. DEP remains the package/API identifier for
// compatibility.
//
// Endpoint overrides require absolute HTTPS URLs with no user information
// or fragment. Test fixtures supply verified HTTPS clients.
//
// # Errors
//
// ErrInvalid, ErrNotFound, ErrConflict, ErrBodyTooLarge and ErrProfileInvalid
// are catalogued client conditions under DM-DEP-*. Token, terms, seed and cursor
// failures are the operator's. Error, the service's own non-2xx answer,
// classifies itself through Kind (ResourceExhausted, DeadlineExceeded or
// Upstream) and exposes Apple's Retry-After through RetryDelay, so a boundary
// needs neither its type nor its status.
//
// # References
//
//   - Operations: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/dep-synchronization.md
//   - Decision record 0026: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md (sealed token columns)
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device-assignment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/accountdetail
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sync-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetchdeviceresponse
//   - Apple: https://developer.apple.com/documentation/devicemanagement/define-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/assign-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/clear-device-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device-details
//   - Apple: https://developer.apple.com/documentation/devicemanagement/disown-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/activation-lock-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/get-beta-enrollment-tokens
//   - Apple: https://developer.apple.com/documentation/devicemanagement/assign-account-driven-enrollment-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-account-driven-enrollment-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/remove-account-driven-enrollment-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/limit
//   - Apple: https://developer.apple.com/documentation/devicemanagement/url
//   - Schema: third_party/apple-device-management/current/other/skipkeys.yaml (schema/other.SkipKeys, the skip_setup_items vocabulary)
//   - RFC 5849 (OAuth 1.0): https://www.rfc-editor.org/rfc/rfc5849
//   - RFC 5652 (CMS enveloped data, the .p7m token file): https://www.rfc-editor.org/rfc/rfc5652
//   - RFC 8551 (S/MIME 4.0): https://www.rfc-editor.org/rfc/rfc8551
package dep
