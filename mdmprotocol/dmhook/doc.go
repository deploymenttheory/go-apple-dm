// Package dmhook defines service-operation metadata and the hook interface for
// observing or vetoing operations.
//
// # Design
//
// Call and Hook let protocol packages supply behavior without importing
// server/service. Account-driven authentication checks and declarative lifecycle
// cleanup use this interface. server/service exposes aliases, so implementations
// written against either package satisfy the same contract. Hook ordering and
// error handling are defined by the service that invokes them.
//
// # References
//
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md (the hook chain)
//   - Decision record 0004: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0028: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
package dmhook
