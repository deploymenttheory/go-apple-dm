// Package collection collects Apple organization-device source evidence into a
// caller-owned transactional sink without an inventory model.
//
// # Persistence and resumption
//
// Each stream is bound to an account, generation, operation and optional opaque
// resource ID. The caller owns credentials, connection selection, scheduling,
// concurrency fencing and transactions. Commit each Batch's evidence, outcome and
// checkpoint atomically. Reload committed state after an uncertain commit result;
// a failed request preserves its prior checkpoint and never erases successful
// evidence. New generations start from a zero Checkpoint. Do not reuse credentials
// or checkpoints across accounts. A checkpoint is sensitive operational state.
//
// Device-list, detail, assignment and coverage streams are independently usable.
// Callers choose which listed resource IDs need follow-up requests. Collection
// does not infer serial correlation, eligibility, deletion or assignment policy.
// A successful null assignment is distinct from 404, authentication, permission
// and transport failures. Only a successfully exhausted enumeration emits its
// completion marker; it does not make Apple pagination a consistent snapshot.
//
// Source resources and list documents retain exact JSON. Failed outcomes retain
// bounded classifications separately from evidence. Runtime errors are returned
// for errors.Is/As; avoid logging their URLs or bodies into public status output.
//
// # References
//
// Apple Business API: https://developer.apple.com/documentation/applebusinessapi
package collection
