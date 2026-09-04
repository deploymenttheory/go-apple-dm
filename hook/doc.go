// Package hook is the vocabulary a service hook is written against: the
// Call describing one operation, and the Hook interface that observes or
// vetoes it.
//
// # Why
//
// A hook is how a feature joins the check-in path without the service
// knowing it exists: account-driven enrollment vetoes a check-in that
// carries no enrollment token, and the declarative engine clears its state
// when a device checks out. Both are extensions of the protocol, and
// neither needs a database.
//
// These two declarations used to live in service, so writing a hook meant
// importing the package that owns storage, enrollment lifecycle, and
// command delivery. That put the account-driven enrollment package and the
// DDM engine in the server tier for the sake of a struct of five pointers
// and a two-method interface, neither of which touches persistence
// (decision record 0044).
//
// service keeps Call and Hook as aliases of these, so a hook written against
// either package satisfies the other and no caller changed when they moved.
//
// # References
//
//   - Decision record 0001: docs/research/decisions/0001-architecture.md (the hook chain)
//   - Decision record 0004: docs/research/decisions/0004-checkin-and-command-core.md
//   - Decision record 0028: docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Plan of record: docs/research/implementation_plan.md (section 3, core domain model)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
package hook
