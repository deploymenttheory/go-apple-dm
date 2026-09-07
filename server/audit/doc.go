// Package audit defines an append-and-prune store for projected device and
// administrative events.
//
// # Design
//
// Records retain metadata, actor strings and fields selected by
// server/eventsink. The interface supports append, filtered cursor queries and
// age-based pruning, with no update or delete-by-ID operation. Backends live in
// server/audit/inmem and server/audit/sqlstore and share the audittest suite.
//
// The interface does not make the database tamper-evident. Persistence is
// optional, write failures do not roll back device operations, and asynchronous
// events can be lost on abrupt shutdown. Configure durable storage, access
// controls, retention and backups when records must survive process failure.
//
// # References
//
//   - Decision record 0038: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0038-persisted-audit-trail.md
//   - Decision record 0037: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0037-event-sinks-and-redaction.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (repudiation)
//   - Apple documents no audit surface; what is recorded is the protocol vocabulary cited by service and ddm.
package audit
