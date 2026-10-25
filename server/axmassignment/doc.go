// Package axmassignment persists explicit named-account assignment workflows.
//
// # Design
//
// The inventory backend provides atomic documents and shared SQL fencing. Apple
// calls run outside transactions. A durable submitting marker precedes each POST;
// an interrupted submission is reconciled using read-only assignment observations
// and is never automatically resubmitted. Activity IDs and per-device CSV outcomes
// are distinct from observed assignment convergence. Configuration binds each
// workflow to one account incarnation, revision, credential, binding generation
// and MDM service. Revoking authority retains overlap protection for submitted
// work. Remote attempt deadlines do not cancel worker-lifetime bookkeeping;
// expired leases still reject all stale commits.
//
// # References
//
// https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity
// https://developer.apple.com/documentation/applebusinessapi/get-orgdeviceactivity-information
package axmassignment
