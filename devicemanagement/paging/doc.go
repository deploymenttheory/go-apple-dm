// Package paging defines cursor-page requests, generic results and shared
// page-size bounds.
//
// # Design
//
// Page and Result are transport-neutral values used by multiple storage and
// service contracts. Size applies the default and maximum before allocation or
// querying, keeping limits consistent across backends. The package does not
// define ordering, query filters or cursor encoding; each issuing backend owns
// those semantics. Callers should treat cursors as opaque.
//
// # References
//
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md (KMFDDM #6, pagination)
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (denial of service through unbounded queries)
package paging
