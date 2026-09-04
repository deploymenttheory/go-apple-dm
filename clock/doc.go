// Package clock abstracts time behind a Clock interface with a Real
// implementation for production and a manually advanced Fake for tests.
//
// # Why
//
// Protocol code is full of time: the signing-time skew tolerance in cms,
// NotNow backoff in the command queue, SCEP challenge expiry, certificate
// validity checks in ca and appleplatformservices/push/apns, and DDM token timestamps. Testing
// those paths against the wall clock is slow and flaky, so the plan of
// record's package layout reserves this internal package and every
// time-sensitive package takes a Clock. Fake is safe for concurrent use so
// tests can advance it while a goroutine waits.
//
// The package has no opinion on timers beyond After; scheduling, retry
// policy, and deadlines belong to the callers.
//
// # References
//
//   - Decision record 0006: docs/research/decisions/0006-mdm-signature-verification.md (signing-time skew)
//   - Decision record 0019: docs/research/decisions/0019-canonical-json-and-ddm-tokens.md (token timestamps)
//   - Plan of record: docs/research/implementation_plan.md (section 1, package layout; phase 2)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package clock
