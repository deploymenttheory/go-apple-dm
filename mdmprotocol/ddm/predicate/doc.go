// Package predicate parses and evaluates the subset of Apple's NSPredicate
// format-string syntax that Declarative Device Management activation
// predicates use.
//
// # Why
//
// The com.apple.activation.simple declaration carries an optional Predicate
// key that Apple's schema describes only as "a predicate format string"
// (third_party/device-management/declarative/declarations/activations/simple.yaml,
// which points at the Predicate Programming Guide). The activation installs
// its configurations only when the predicate is absent or evaluates to
// true, so a server that wants to reject a bad predicate at upload time, or
// a simulator that wants to behave like a device, needs to parse and
// evaluate it. Phase 5 of the plan of record adds both (decision record
// 0024): the engine calls Validate when a declaration is stored, and the
// simulator's DDM client evaluates against its @property values and
// @status items. Forms observed in the wild are `(@property(shard) <= 75)`,
// `@status(device.identifier.serial-number) == 'ZYXW4321'` and `1==0`.
//
// The package implements only what activations need and rejects the rest
// of NSPredicate explicitly, as the Unsupported constructs section lists.
// It does not decide which declarations a device sees; that is ddm's
// membership resolution, which runs before predicates are consulted.
//
// # Grammar
//
// Keywords and operators are case-insensitive. Whitespace between tokens is
// insignificant.
//
//	predicate   := or
//	or          := and { ("OR" | "||") and }
//	and         := not { ("AND" | "&&") not }
//	not         := ("NOT" | "!") not | primary
//	primary     := "(" or ")" | "TRUEPREDICATE" | "FALSEPREDICATE" | comparison
//	comparison  := operand [ "[c]" ] op [ "[c]" ] operand
//	operand     := "@property" "(" key ")" | "@status" "(" path ")" | literal
//	literal     := string | number | "TRUE" | "FALSE" | "YES" | "NO"
//	             | "NULL" | "NIL" | "{" [ literal { "," literal } ] "}"
//	op          := "==" | "=" | "!=" | "<>" | "<" | "<=" | ">" | ">="
//	             | "IN" | "CONTAINS" | "BEGINSWITH" | "ENDSWITH"
//	key, path   := [A-Za-z0-9_.-]+ | string
//	string      := ( "'" ... "'" | '"' ... '"' ) with escapes \\ \' \" \n \r \t \uXXXX
//	number      := [+-] digits [ "." digits ] [ ("e" | "E") [+-] digits ]
//
// The [c] modifier requests case-insensitive string comparison. Apple writes
// it after the operator (`==[c]`); the modifier is also accepted immediately
// before the operator. It is permitted only on ==, !=, IN, CONTAINS,
// BEGINSWITH and ENDSWITH.
//
// # Unsupported constructs
//
// The following NSPredicate features are recognised and rejected with a
// *SyntaxError whose Err is ErrUnsupported and whose Msg names the construct:
// SELF, bare key paths outside @property or @status, %K and %@ format
// arguments, $variables, MATCHES, LIKE, BETWEEN, ANY, ALL, NONE, SOME,
// SUBQUERY, FUNCTION and any identifier followed by "(", arithmetic
// operators, the [d], [cd] and [n] modifiers, and CAST.
//
// # Evaluation
//
// A missing property or status item evaluates to nil. The == and != operators
// compare nil like any other value, so nil == nil is true. Ordering, IN,
// CONTAINS, BEGINSWITH and ENDSWITH involving nil are false. Integers and
// floats from the environment promote to float64, strings compare lexically,
// and booleans support only == and !=. Mixing a string with a number, or a
// boolean with anything else, is a type mismatch and Eval returns an error
// wrapping ErrType. IN requires an aggregate on its right-hand side, either a
// `{...}` literal or a slice supplied by the environment. CONTAINS,
// BEGINSWITH and ENDSWITH require strings on both sides. TRUEPREDICATE and
// FALSEPREDICATE are constants.
//
// # References
//
//   - Decision record 0020: docs/research/decisions/0020-ddm-engine-membership-and-storage.md
//   - Decision record 0024: docs/research/decisions/0024-simulator-ddm-client-and-predicates.md
//   - Plan of record: docs/research/implementation_plan.md (section 4, DDM engine; phase 5)
//   - Threat model: docs/security/threat-model.md (Admin upload of declarations row)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-009)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices
//   - Apple: https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/Predicates/AdditionalChapters/Introduction.html
//   - Schema: third_party/device-management/declarative/declarations/activations/simple.yaml (Predicate)
//   - Schema: third_party/device-management/declarative/declarations/management/properties.yaml (@property values)
//   - Schema: third_party/device-management/declarative/declarations/declarationbase.yaml (Info.Predicate reason code)
package predicate
