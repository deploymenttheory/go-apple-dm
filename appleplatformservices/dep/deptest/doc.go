// Package deptest is the test bed for the DEP feature: a fake DEP service
// over httptest that speaks Apple's Device assignment API, the contract
// suite every dep.Store backend must satisfy, and a Failing store that
// injects errors by method name.
//
// # Why
//
// Apple's service cannot be driven from a test, and the behaviours the
// client, syncer, and assigner exist to get right (session rotation,
// re-authentication, cursor ageing, page repetition, throttling, per-serial
// outcomes) only show under scripted failure. Phase 6 of the plan of
// record delivers this fake alongside the client (decision record 0026)
// so every claim in the record is proved against it: OAuth 1.0a
// verification with a clock window and nonce replay check, /session
// issuing tokens, X-ADM-Auth-Session rotation and invalidation, scripted
// 401, 403 FORBIDDEN, T_C_NOT_SIGNED, 429 with Retry-After, THROTTLED with
// retry_after_seconds, NOT_ACCESSIBLE and FAILED, opaque cursors that
// expire, fetch pages without op_type and sync pages with it, error bodies
// in bare and quoted forms, POST and PUT assignment, .p7m token files
// produced through dep.Wrap, and a request log. The store suite pins the
// storage semantics (accounts sealed at rest, atomic keypair upstage,
// cursor with its timestamp, every device key and tombstones, assignment
// outcomes with next-attempt times) once, so dep/inmem and dep/sqlstore
// are held to the same rule.
//
// The fake is not a simulator of Apple's data model beyond what the
// client observes; it takes no code from depsim or nanodep.
//
// # References
//
//   - Decision record 0026: docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Plan of record: docs/research/implementation_plan.md (section 5, DEP / ABM; phase 6)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-011)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sync-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/assign-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/define-profile
//   - RFC 5849 (OAuth 1.0): https://www.rfc-editor.org/rfc/rfc5849
package deptest
