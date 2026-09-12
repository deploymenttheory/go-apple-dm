// Package clock abstracts time with a real clock and a manually advanced
// concurrent-safe fake.
//
// # Design
//
// Injected time makes certificate validity, token expiry, queue backoff and
// other time-dependent behavior testable without wall-clock delays. Fake
// coordinates Now, After and manual advancement under a lock. Scheduling and
// retry policy remain with callers; the package's timer interface is limited to
// After.
//
// # References
//
//   - Decision record 0006: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0006-mdm-signature-verification.md (signing-time skew)
//   - Decision record 0019: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0019-canonical-json-and-ddm-tokens.md (token timestamps)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package clock
