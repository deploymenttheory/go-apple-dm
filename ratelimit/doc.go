// Package ratelimit implements optional atomic GCRA quotas with bounded state.
//
// # Why
//
// Public enrollment and authentication endpoints need configurable throughput
// controls. Callers select routes, quotas and peer identity; the library imposes
// no enrollment policy. Shared state uses store time and an all-or-none decision
// across buckets. No raw credential belongs in a bucket key.
//
// # References
//
//   - docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - RFC 6585 section 4 (429), RFC 9110 section 10.2.3 (Retry-After)
//   - Fleet server/platform/middleware/ratelimit and server/datastore/redis
package ratelimit
