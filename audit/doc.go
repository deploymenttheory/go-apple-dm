// Package audit is the persistent record of everything the server did: an
// append-and-prune store of projected events, with the in-memory and SQL
// backends behind one contract.
//
// # Why
//
// The threat model's repudiation control states that every state change emits
// an event and that subscribers persist them. Phase 9 made the first half
// true with event/sink, which decides what an event may say. This package is
// the second half: the place those records go so a question can be asked
// afterwards. An slog sink is attributable but only as persistent as the log
// stream someone remembered to ship; proving who erased a device three weeks
// ago needs a table.
//
// The interface is deliberately append-and-prune. There is no update and no
// delete by id, because a trail whose rows can be edited answers no question
// worth asking. The only way a record leaves is age, so retention is a stated
// policy rather than a way to lose one inconvenient row.
//
// What is stored is the projection from event/sink, never event.Data. The
// payload of a TokenUpdate carries UnlockToken, the secret that clears a
// device passcode, so a trail that persisted raw payloads would be a
// long-lived copy of every device's secrets. Fields holds what the projection
// allowed and nothing else.
//
// This package owns the contract and the domain type. Persistence is
// audit/inmem and audit/sqlstore, which keeps its own migration set, and
// audit/audittest is the suite both must pass.
//
// # References
//
//   - Decision record 0038: docs/research/decisions/0038-persisted-audit-trail.md
//   - Decision record 0037: docs/research/decisions/0037-event-sinks-and-redaction.md
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
//   - Threat model: docs/security/threat-model.md (repudiation)
//   - Apple documents no audit surface; what is recorded is the protocol
//     vocabulary cited by service and ddm.
package audit
