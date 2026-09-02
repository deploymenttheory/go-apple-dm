// Package conformance holds the helpers the generated conformance tests
// call: RoundTrip through JSON, XML plist, and binary plist, and Validates
// for the generated Validate methods.
//
// # Why
//
// Phase 1 of the plan of record promises that every YAML file Apple
// publishes has a generated type that survives a round trip through every
// encoding a device might use (decision record 0003). The generator emits
// one conformance test per package with every documented key populated;
// the checks themselves live here so they are written once and the
// generated files stay small. RoundTrip compares decoded generic values
// rather than bytes, which keeps the check meaningful for fields typed as
// any, and decodes binary plist produced by an independent encoder to
// catch tag mistakes our own encoder would mask. The JSON-only path
// follows the policy of decision record 0018 for declaration and status
// types that never travel as plists.
//
// The package is internal to schema/ so generated packages can import it
// and nothing else can. It is test support only and holds no protocol
// behaviour.
//
// # References
//
//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md
//   - Decision record 0018: docs/research/decisions/0018-go-1.27-baseline.md
//   - Plan of record: docs/research/implementation_plan.md (section 2, the generator; phase 1)
//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md
//   - Schema: third_party/device-management/docs/schema.yaml (meta-schema)
package conformance
