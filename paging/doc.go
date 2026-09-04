// Package paging is the cursor pagination vocabulary every store contract
// shares: a Page request, a Result page of items, and the bounds a backend
// applies to a caller's limit.
//
// # Why
//
// Pagination is the one piece of the storage contract that is not about
// storage. A Page is a request shape and a Result is a value; neither opens
// a connection, and both appear in interfaces that have no persistence of
// their own. Keeping them in the storage package made every consumer of a
// paginated interface import storage: three quarters of all references to
// that package from elsewhere in the module were these two types, which put
// the declarative engine, the ACME server, and the device enrollment client
// in the storage package's dependency graph for the sake of a struct with
// two fields. This package holds them instead, so a contract can be
// paginated without being persistent, and the layer that stores things is
// depended on only by the layers that store things.
//
// Bounding lives here for the same reason it existed at all: the limit
// arrives from an admin query string and reaches a slice allocation before
// a single row is read, so Size defaults and caps it once rather than in
// every backend. KMFDDM #6 (no pagination anywhere) is the failure this
// contract exists to avoid, recorded in decision 0001.
//
// The package deliberately knows nothing about what is being paged. It has
// no cursor encoding, no ordering, and no query types: how a cursor is
// built and what it points at belong to the backend that issues it.
//
// # References
//
//   - Decision record 0001: docs/research/decisions/0001-architecture.md (KMFDDM #6, pagination)
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0012: docs/research/decisions/0012-sql-storage-backends.md
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Plan of record: docs/research/implementation_plan.md (phase 4)
//   - Threat model: docs/security/threat-model.md (denial of service through unbounded queries)
package paging
