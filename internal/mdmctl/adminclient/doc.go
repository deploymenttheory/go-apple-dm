// Package adminclient is the typed HTTP client mdmctl uses against the
// reference server's admin API.
//
// # Why
//
// It exists so the CLI has one place that knows about bearer tokens, error
// bodies, and cursors, and so those are testable without a process. Three
// properties matter, and each answers something a reference CLI got wrong:
// a response body is handed back byte for byte so canonical JSON survives to
// jq, where nanohubctl re-indents it; cursors are followed on request, where
// none of the reference CLIs paginate at all; and a redirect is refused, so a
// bearer token cannot be replayed to a host the operator did not name.
//
// # References
//
//   - Decision record: docs/research/decisions/0035-mdmctl-structure-and-credentials.md
//   - Decision record: docs/research/decisions/0034-admin-api-and-authorization.md
//   - Plan of record: docs/research/implementation_plan.md (phase 8)
//   - RFC 6750: bearer token usage
package adminclient
