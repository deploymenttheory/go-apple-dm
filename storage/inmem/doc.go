// Package inmem is the reference storage backend: a mutex-protected map
// store that every unit test uses.
//
// # Why
//
// The service layer, the HTTP handlers, and the simulator all need a
// storage.Store that is always compiled, needs no driver or database, and
// behaves exactly as the contract says. Phase 2 of the plan of record
// delivers this backend with the interfaces (decision record 0005), and
// the suite in server/storage/storagetest defines its behaviour, so it doubles as
// the executable specification the SQL backends are measured against.
//
// It is not persistent and is not tuned: no pagination cost model, no
// encryption at rest, no concurrency beyond one mutex. Anything that must
// survive a restart uses sqlite, postgres, or mysql.
//
// # References
//
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Plan of record: docs/research/implementation_plan.md (phase 2)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package inmem
