// Package adminclient implements dmctl's internal HTTP access to the reference
// server admin API.
//
// # Design
//
// The client centralizes bearer authentication, bounded response reads, typed
// error handling and cursor iteration. Response bodies remain unchanged for
// machine-readable CLI output. Redirects are refused to keep credentials bound
// to the selected destination. Callers choose whether to fetch one page or
// iterate, and cancellation propagates through requests.
//
// # References
//
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0035-dmctl-structure-and-credentials.md
//   - Decision record: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0034-admin-api-and-authorization.md
//   - RFC 6750: bearer token usage
package adminclient
