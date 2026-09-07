// Package ratelimit implements optional atomic GCRA quotas with bounded,
// expiring state.
//
// # Design
//
// Callers choose route families, bucket keys and quotas. Each decision accounts
// for all buckets or none, using transactional store time. Keys are hashed,
// capacity is bounded, and typed unavailable errors distinguish storage/capacity
// failures from quota exhaustion. A decision can carry a retry delay.
//
// The limiter does not choose enrollment admission policy or trust forwarded
// addresses. The reference server configures peer and aggregate buckets and
// serializes capacity accounting within its limiter namespace. Do not include
// raw credentials in bucket keys.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - RFC 6585 section 4 (429), RFC 9110 section 10.2.3 (Retry-After)
//   - Fleet server/platform/middleware/ratelimit and server/datastore/redis
package ratelimit
