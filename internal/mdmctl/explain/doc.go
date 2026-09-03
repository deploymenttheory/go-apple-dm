// Package explain answers what a command, declaration, profile payload, or
// status item is, and where Apple says it applies.
//
// # Why
//
// The generator keeps Apple's supportedOS metadata as data rather than
// flattening it into comments, and nothing has read it back for a human until
// now. `mdmctl explain DeviceLock -target macos:15.0` answers, from the same
// tables the server validates against, whether a key applies to a supervised
// Mac on 15.0 and why not when it does not.
//
// Two rules make the answers trustworthy. A tri-state that Apple left unstated
// renders as "-", never as "no", because the generator kept those as pointers
// precisely so "forbidden" and "unsaid" stay distinguishable. And the two
// answers that mean "we do not know" -- an entry with no support data, and a
// query with no target OS -- render as "unknown", never "OK", so the command
// never asserts a fact Apple did not state.
//
// It is offline by construction: it reads compiled-in tables, builds no
// client, and reads neither server nor token.
//
// # References
//
//   - Decision record: docs/research/decisions/0036-mdmctl-explain-over-schema-support.md
//   - Decision record: docs/research/decisions/0035-mdmctl-structure-and-credentials.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Schema: third_party/device-management/docs/schema.yaml (supportedOS)
package explain
