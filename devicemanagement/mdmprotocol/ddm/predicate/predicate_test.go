package predicate

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// parseCase pairs an input with its canonical rendering.
type parseCase struct {
	name  string
	input string
	want  string
}

// bs is a literal backslash, used to build escape sequences in test inputs.
const bs = "\\"

var parseCases = []parseCase{
	// Observed forms.
	{"observed shard", `(@property(shard) <= 75)`, `@property(shard) <= 75`},
	{"observed serial", `@status(device.identifier.serial-number) == 'ZYXW4321'`, `@status(device.identifier.serial-number) == 'ZYXW4321'`},
	{"observed constant", `1==0`, `1 == 0`},
	// Every operator, both spellings.
	{"eq", `@property(a) == 1`, `@property(a) == 1`},
	{"eq single", `@property(a) = 1`, `@property(a) == 1`},
	{"ne", `@property(a) != 1`, `@property(a) != 1`},
	{"ne diamond", `@property(a) <> 1`, `@property(a) != 1`},
	{"lt", `@property(a) < 1`, `@property(a) < 1`},
	{"le", `@property(a) <= 1`, `@property(a) <= 1`},
	{"gt", `@property(a) > 1`, `@property(a) > 1`},
	{"ge", `@property(a) >= 1`, `@property(a) >= 1`},
	{"in", `@property(a) IN {1, 2, 3}`, `@property(a) IN {1, 2, 3}`},
	{"in lower", `@property(a) in {1,2,3}`, `@property(a) IN {1, 2, 3}`},
	{"contains", `@property(a) contains 'x'`, `@property(a) CONTAINS 'x'`},
	{"beginswith", `@property(a) BeginsWith 'x'`, `@property(a) BEGINSWITH 'x'`},
	{"endswith", `@property(a) ENDSWITH 'x'`, `@property(a) ENDSWITH 'x'`},
	// Case modifier in both positions.
	{"ci after op", `@property(a) ==[c] 'x'`, `@property(a) ==[c] 'x'`},
	{"ci before op", `@property(a) [c] == 'x'`, `@property(a) ==[c] 'x'`},
	{"ci upper", `@property(a) CONTAINS[C] 'x'`, `@property(a) CONTAINS[c] 'x'`},
	{"ci spaced", `@property(a) IN [ c ] {'x'}`, `@property(a) IN[c] {'x'}`},
	{"ci ne", `@property(a) !=[c] 'x'`, `@property(a) !=[c] 'x'`},
	{"ci beginswith", `@property(a) BEGINSWITH[c] 'x'`, `@property(a) BEGINSWITH[c] 'x'`},
	{"ci endswith", `@property(a) ENDSWITH[c] 'x'`, `@property(a) ENDSWITH[c] 'x'`},
	// Compound operators and precedence.
	{"and word", `@property(a) == 1 and @property(b) == 2`, `@property(a) == 1 AND @property(b) == 2`},
	{"and symbol", `@property(a) == 1 && @property(b) == 2`, `@property(a) == 1 AND @property(b) == 2`},
	{"or word", `@property(a) == 1 Or @property(b) == 2`, `@property(a) == 1 OR @property(b) == 2`},
	{"or symbol", `@property(a) == 1 || @property(b) == 2`, `@property(a) == 1 OR @property(b) == 2`},
	{"precedence", `@property(a) == 1 OR @property(b) == 2 AND @property(c) == 3`, `@property(a) == 1 OR @property(b) == 2 AND @property(c) == 3`},
	{"explicit precedence", `@property(a) == 1 OR (@property(b) == 2 AND @property(c) == 3)`, `@property(a) == 1 OR @property(b) == 2 AND @property(c) == 3`},
	{"override precedence", `(@property(a) == 1 OR @property(b) == 2) AND @property(c) == 3`, `(@property(a) == 1 OR @property(b) == 2) AND @property(c) == 3`},
	{"left assoc", `@property(a) == 1 OR @property(b) == 2 OR @property(c) == 3`, `@property(a) == 1 OR @property(b) == 2 OR @property(c) == 3`},
	{"right nested", `@property(a) == 1 OR (@property(b) == 2 OR @property(c) == 3)`, `@property(a) == 1 OR (@property(b) == 2 OR @property(c) == 3)`},
	{"and right nested", `@property(a) == 1 AND (@property(b) == 2 AND @property(c) == 3)`, `@property(a) == 1 AND (@property(b) == 2 AND @property(c) == 3)`},
	{"not", `NOT @property(a) == 1`, `NOT @property(a) == 1`},
	{"not bang", `!@property(a) == 1`, `NOT @property(a) == 1`},
	{"not not", `NOT NOT @property(a) == 1`, `NOT NOT @property(a) == 1`},
	{"not bang word", `! not @property(a) == 1`, `NOT NOT @property(a) == 1`},
	{"not group", `!(@property(a) == 1 && @property(b) == 2)`, `NOT (@property(a) == 1 AND @property(b) == 2)`},
	{"not binds tight", `not @property(a) == 1 and @property(b) == 2`, `NOT @property(a) == 1 AND @property(b) == 2`},
	{"deep nesting", `((((@property(a) == 1))))`, `@property(a) == 1`},
	// Constants.
	{"truepredicate", `truepredicate`, `TRUEPREDICATE`},
	{"falsepredicate", `FALSEPREDICATE`, `FALSEPREDICATE`},
	{"constants compound", `TRUEPREDICATE AND FALSEPREDICATE`, `TRUEPREDICATE AND FALSEPREDICATE`},
	{"not constant", `NOT (TRUEPREDICATE)`, `NOT TRUEPREDICATE`},
	// Literals.
	{"true", `@property(a) == TRUE`, `@property(a) == TRUE`},
	{"yes", `@property(a) == yes`, `@property(a) == TRUE`},
	{"false", `@property(a) == False`, `@property(a) == FALSE`},
	{"no", `@property(a) == NO`, `@property(a) == FALSE`},
	{"null", `@property(a) == NULL`, `@property(a) == NULL`},
	{"nil", `@property(a) == nil`, `@property(a) == NULL`},
	{"double quotes", `@property(a) == "double"`, `@property(a) == 'double'`},
	{"escaped single", `@property(a) == 'it\'s'`, `@property(a) == 'it\'s'`},
	{"escaped double", `@property(a) == "say \"hi\""`, `@property(a) == 'say "hi"'`},
	{"double quote in single", `@property(a) == 'say "hi"'`, `@property(a) == 'say "hi"'`},
	{"control escapes", `@property(a) == 'a\nb\tc\rd\\e'`, `@property(a) == 'a\nb\tc\rd\\e'`},
	{"unicode escape", "@property(a) == '" + bs + "u00e9'", "@property(a) == '\xc3\xa9'"},
	{"surrogate pair", "@property(a) == '" + bs + "ud83d" + bs + "ude00'", "@property(a) == '\xf0\x9f\x98\x80'"},
	{"surrogate then escape", "@property(a) == '" + bs + "ud83d" + bs + "u0041'", "@property(a) == '\xef\xbf\xbdA'"},
	{"lone surrogate", "@property(a) == '" + bs + "ud83dx'", "@property(a) == '\xef\xbf\xbdx'"},
	{"surrogate then bad", "@property(a) == '" + bs + "ud83dA'", "@property(a) == '\xef\xbf\xbdA'"},
	{"raw control", "@property(a) == 'a\x01b'", "@property(a) == 'a" + bs + "u0001b'"},
	{"raw delete", "@property(a) == 'a\x7fb'", "@property(a) == 'a" + bs + "u007fb'"},
	{"invalid utf8", "@property(a) == '\xff'", "@property(a) == '\xff'"},
	{"empty string", `@property(a) == ''`, `@property(a) == ''`},
	{"negative", `@property(a) == -5`, `@property(a) == -5`},
	{"positive", `@property(a) == +5`, `@property(a) == 5`},
	{"float", `@property(a) == 1.5`, `@property(a) == 1.5`},
	{"exponent", `@property(a) == 1e3`, `@property(a) == 1000`},
	{"signed exponent", `@property(a) == 1.5E-3`, `@property(a) == 0.0015`},
	{"leading dot", `@property(a) == .5`, `@property(a) == 0.5`},
	{"trailing dot", `@property(a) == 5.`, `@property(a) == 5`},
	{"negative fraction", `@property(a) == -.5`, `@property(a) == -0.5`},
	{"large", `@property(a) == 12345678901234567890`, `@property(a) == 1.2345678901234567e+19`},
	{"number left", `-1 < @property(a)`, `-1 < @property(a)`},
	{"number after and", `@property(a) == 1 && -1 < @property(b)`, `@property(a) == 1 AND -1 < @property(b)`},
	{"number after not", `NOT -1 == @property(a)`, `NOT -1 == @property(a)`},
	// Aggregates.
	{"empty aggregate", `@property(a) IN {}`, `@property(a) IN {}`},
	{"single aggregate", `@property(a) IN {1}`, `@property(a) IN {1}`},
	{"spaced aggregate", `@property(a) IN { 'a' , 'b' }`, `@property(a) IN {'a', 'b'}`},
	{"nested aggregate", `@property(a) IN {1, {2, 3}}`, `@property(a) IN {1, {2, 3}}`},
	{"mixed aggregate", `@property(a) IN {NULL, TRUE, -1, 'x'}`, `@property(a) IN {NULL, TRUE, -1, 'x'}`},
	{"aggregate left", `{1} == @property(a)`, `{1} == @property(a)`},
	// References.
	{"quoted key", `@property('my key') == 1`, `@property('my key') == 1`},
	{"quoted plain key", `@property("shard") == 1`, `@property(shard) == 1`},
	{"empty key", `@property('') == 1`, `@property('') == 1`},
	{"spaced reference", `@status ( "a b" ) == 1`, `@status('a b') == 1`},
	{"digit key", `@property(1abc) == 1`, `@property(1abc) == 1`},
	{"upper reference", `@PROPERTY(a) == @Status(b)`, `@property(a) == @status(b)`},
	{"literal left", `'x' == @property(a)`, `'x' == @property(a)`},
	{"literals both", `'x' == 'y'`, `'x' == 'y'`},
	// Whitespace variants.
	{"no spaces", `@property(a)==1`, `@property(a) == 1`},
	{"tabs newlines", " \t\n@property( a )\n==\t1 \r\n", `@property(a) == 1`},
	{"tight compound", `@property(a)==1&&@property(b)==2||!@property(c)==3`, `@property(a) == 1 AND @property(b) == 2 OR NOT @property(c) == 3`},
}

func TestParseTable(t *testing.T) {
	t.Parallel()
	for _, tc := range parseCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.input, err)
			}
			if got := p.String(); got != tc.want {
				t.Fatalf("Parse(%q).String() = %q, want %q", tc.input, got, tc.want)
			}
			if got := p.Source(); got != tc.input {
				t.Fatalf("Source() = %q, want %q", got, tc.input)
			}
		})
	}
}

func propEq(key string, n float64) expr {
	return &compareExpr{left: &propertyRef{key: key}, op: cmpEq, right: &literal{kind: litNumber, number: n}}
}

// TestParseStructure checks the tree shape directly for precedence and
// associativity, independent of the renderer.
func TestParseStructure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  expr
	}{
		{
			`@property(a) == 1 OR @property(b) == 2 AND @property(c) == 3`,
			&compoundExpr{op: logicalOr, left: propEq("a", 1), right: &compoundExpr{op: logicalAnd, left: propEq("b", 2), right: propEq("c", 3)}},
		},
		{
			`@property(a) == 1 OR @property(b) == 2 OR @property(c) == 3`,
			&compoundExpr{op: logicalOr, left: &compoundExpr{op: logicalOr, left: propEq("a", 1), right: propEq("b", 2)}, right: propEq("c", 3)},
		},
		{
			`NOT NOT @property(a) == 1`,
			&notExpr{operand: &notExpr{operand: propEq("a", 1)}},
		},
		{
			`NOT @property(a) == 1 AND @property(b) == 2`,
			&compoundExpr{op: logicalAnd, left: &notExpr{operand: propEq("a", 1)}, right: propEq("b", 2)},
		},
		{
			`@status(s) ==[c] 'x'`,
			&compareExpr{left: &statusRef{path: "s"}, op: cmpEq, caseInsensitive: true, right: &literal{kind: litString, text: "x"}},
		},
		{
			`@property(a) IN {1, {}}`,
			&compareExpr{left: &propertyRef{key: "a"}, op: cmpIn, right: &literal{kind: litAggregate, items: []literal{
				{kind: litNumber, number: 1},
				{kind: litAggregate, items: []literal{}},
			}}},
		},
		{`TRUEPREDICATE`, &constExpr{value: true}},
	}
	for _, tc := range cases {
		p, err := Parse(tc.input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.input, err)
		}
		if !reflect.DeepEqual(p.root, tc.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tc.input, p.root, tc.want)
		}
	}
}

// errorCase is a parse failure with the sentinel it must wrap, a substring
// its Msg must contain and the offset it must report.
type errorCase struct {
	name   string
	input  string
	is     error
	msg    string
	offset int
}

var errorCases = []errorCase{
	{"empty", ``, ErrSyntax, "empty", 0},
	{"blank", "  \t\n", ErrSyntax, "empty", 4},
	{"unterminated string", `@property(a) == 'abc`, ErrSyntax, "unterminated string", 16},
	{"unterminated double string", `@property(a) == "abc`, ErrSyntax, "unterminated string", 16},
	{"missing close paren", `(@property(a) == 1`, ErrSyntax, `expected ")"`, 18},
	{"extra close paren", `@property(a) == 1)`, ErrSyntax, `unexpected ")"`, 17},
	{"dangling comparison", `@property(a) ==`, ErrSyntax, "end of input", 15},
	{"dangling and", `@property(a) == 1 AND`, ErrSyntax, "end of input", 21},
	{"dangling or", `@property(a) == 1 ||`, ErrSyntax, "end of input", 20},
	{"dangling not", `NOT`, ErrSyntax, "end of input", 3},
	{"missing operator", `@property(a)`, ErrSyntax, "comparison operator", 12},
	{"missing operator string", `'a' 'b'`, ErrSyntax, "comparison operator", 4},
	{"triple equals", `@property(a) === 1`, ErrSyntax, "expected an operand", 15},
	{"unknown operator", `@property(a) ~ 1`, ErrSyntax, "unexpected character '~'", 13},
	{"single ampersand", `@property(a) == 1 & @property(b) == 2`, ErrSyntax, `expected "&&"`, 18},
	{"single pipe", `@property(a) == 1 | @property(b) == 2`, ErrSyntax, `expected "||"`, 18},
	{"leading modifier", `[c] @property(a) == 1`, ErrSyntax, "modifier [c]", 0},
	{"trailing modifier", `@property(a) == 1 [c]`, ErrSyntax, "unexpected modifier [c]", 18},
	{"modifier on lt", `@property(a) <[c] 1`, ErrSyntax, "not allowed on <", 14},
	{"modifier on ge before", `@property(a) [c] >= 1`, ErrSyntax, "not allowed on >=", 13},
	{"duplicate modifier", `@property(a) [c] ==[c] 1`, ErrSyntax, "duplicate modifier", 19},
	{"unterminated modifier", `@property(a) [c == 1`, ErrSyntax, "unterminated modifier", 13},
	{"bare reference", `@property`, ErrSyntax, `expected "("`, 9},
	{"empty reference", `@property() == 1`, ErrSyntax, "expected a key", 10},
	{"unclosed reference", `@property(a == 1`, ErrSyntax, `expected ")"`, 12},
	{"unterminated key string", `@property('a == 1`, ErrSyntax, "unterminated string", 10},
	{"unknown reference", `@foo(a) == 1`, ErrSyntax, "unknown reference @foo", 0},
	{"lone at", `@`, ErrSyntax, "unknown reference", 0},
	{"invalid exponent", `@property(a) == 1e`, ErrSyntax, "invalid number", 16},
	{"invalid signed exponent", `@property(a) == 1e+`, ErrSyntax, "invalid number", 16},
	{"number out of range", `@property(a) == 1e999`, ErrSyntax, "invalid number", 16},
	{"invalid escape", `@property(a) == 'a\qb'`, ErrSyntax, "invalid escape", 18},
	{"unterminated escape", `@property(a) == 'abc\`, ErrSyntax, "unterminated escape", 20},
	{"short unicode escape", `@property(a) == '\u12'`, ErrSyntax, `\u escape`, 17},
	{"bad unicode escape", `@property(a) == '\uzzzz'`, ErrSyntax, `\u escape`, 17},
	{"aggregate missing comma", `@property(a) IN {1 2}`, ErrSyntax, `expected ","`, 19},
	{"aggregate trailing comma", `@property(a) IN {1,}`, ErrSyntax, "expected an operand", 19},
	{"aggregate unclosed", `@property(a) IN {1`, ErrSyntax, "end of input", 18},
	{"aggregate reference", `@property(a) IN {@property(b)}`, ErrSyntax, "expected an operand", 17},
	{"keyword operand", `1 == 1 AND AND`, ErrSyntax, "unexpected keyword AND", 11},
	{"operator operand", `IN == 1`, ErrSyntax, "unexpected keyword IN", 0},
	{"paren operand", `@property(a) == )`, ErrSyntax, "expected an operand", 16},
	{"operator as operand", `@property(a) == == 1`, ErrSyntax, "expected an operand", 16},
	{"trailing garbage", `@property(a) == 1 2`, ErrSyntax, "unexpected number 2", 18},
	{"trailing string", `TRUEPREDICATE 'x'`, ErrSyntax, `unexpected string "x"`, 14},
	{"nested too deep", strings.Repeat("(", 300) + "TRUEPREDICATE" + strings.Repeat(")", 300), ErrSyntax, "nesting deeper", 256},
	{"not too deep", strings.Repeat("NOT ", 300) + "TRUEPREDICATE", ErrSyntax, "nesting deeper", 256 * 4},
	{"aggregate too deep", `@property(a) IN ` + strings.Repeat("{", 300) + strings.Repeat("}", 300), ErrSyntax, "nesting deeper", 16 + 256},
	{"stray character", `@property(a) == 1 ;`, ErrSyntax, "unexpected character ';'", 18},
	{"unicode character", `@property(a) == 1 é`, ErrSyntax, "unexpected character 'é'", 18},
	// Unsupported constructs.
	{"self", `SELF == 1`, ErrUnsupported, "SELF", 0},
	{"bare key path", `name == 'x'`, ErrUnsupported, `bare key path "name"`, 0},
	{"bare key path right", `@property(a) == name`, ErrUnsupported, `bare key path "name"`, 16},
	{"format K", `%K == 'x'`, ErrUnsupported, "%K", 0},
	{"format at", `@property(a) == %@`, ErrUnsupported, "%@", 16},
	{"format d", `@property(a) == %d`, ErrUnsupported, "%d", 16},
	{"format bare", `@property(a) == %`, ErrUnsupported, "format argument %", 16},
	{"variable", `$x == 1`, ErrUnsupported, "$x", 0},
	{"variable right", `@property(a) == $shard`, ErrUnsupported, "$shard", 16},
	{"matches", `@property(a) MATCHES '.*'`, ErrUnsupported, "MATCHES", 13},
	{"like", `@property(a) like 'a*'`, ErrUnsupported, "LIKE", 13},
	{"between", `@property(a) BETWEEN {1, 2}`, ErrUnsupported, "BETWEEN", 13},
	{"matches operand", `MATCHES == 1`, ErrUnsupported, "MATCHES", 0},
	{"any", `ANY @property(a) == 1`, ErrUnsupported, "ANY", 0},
	{"all", `all @property(a) == 1`, ErrUnsupported, "ALL", 0},
	{"none", `NONE @property(a) == 1`, ErrUnsupported, "NONE", 0},
	{"some", `SOME @property(a) == 1`, ErrUnsupported, "SOME", 0},
	{"subquery", `SUBQUERY(a, $x, $x == 1).@count > 0`, ErrUnsupported, "SUBQUERY", 0},
	{"subquery bare", `SUBQUERY == 1`, ErrUnsupported, "SUBQUERY", 0},
	{"function", `FUNCTION(@property(a), 'foo') == 1`, ErrUnsupported, "FUNCTION", 0},
	{"function call", `foo(1) == 1`, ErrUnsupported, "function call foo(...)", 0},
	{"function call spaced", `@property(a) == now ()`, ErrUnsupported, "function call now(...)", 16},
	{"cast", `CAST(@property(a), 'NSNumber') == 1`, ErrUnsupported, "CAST", 0},
	{"cast bare", `@property(a) == CAST`, ErrUnsupported, "CAST", 16},
	{"plus", `@property(a) + 1 == 2`, ErrUnsupported, "arithmetic operator +", 13},
	{"minus", `@property(a) - 1 == 2`, ErrUnsupported, "arithmetic operator -", 13},
	{"minus tight", `1-1 == 0`, ErrUnsupported, "arithmetic operator -", 1},
	{"plus tight", `@property(a) == 1 +1`, ErrUnsupported, "arithmetic operator +", 18},
	{"times", `@property(a) * 2 == 2`, ErrUnsupported, "arithmetic operator *", 13},
	{"divide", `@property(a) / 2 == 2`, ErrUnsupported, "arithmetic operator /", 13},
	{"power", `@property(a) ** 2 == 2`, ErrUnsupported, "arithmetic operator **", 13},
	{"modifier cd", `@property(a) ==[cd] 'x'`, ErrUnsupported, "modifier [cd]", 15},
	{"modifier d", `@property(a) [d] == 'x'`, ErrUnsupported, "modifier [d]", 13},
	{"modifier n", `@property(a) CONTAINS[n] 'x'`, ErrUnsupported, "modifier [n]", 21},
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := Parse(tc.input)
			if err == nil {
				t.Fatalf("Parse(%q) = %q, want error", tc.input, p.String())
			}
			if p != nil {
				t.Fatalf("Parse(%q) returned a predicate alongside an error", tc.input)
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error %T, want *SyntaxError", tc.input, err)
			}
			if !errors.Is(err, tc.is) {
				t.Errorf("Parse(%q) error %v does not wrap %v", tc.input, err, tc.is)
			}
			if !strings.Contains(se.Msg, tc.msg) {
				t.Errorf("Parse(%q) Msg %q does not contain %q", tc.input, se.Msg, tc.msg)
			}
			if se.Offset != tc.offset {
				t.Errorf("Parse(%q) Offset = %d, want %d", tc.input, se.Offset, tc.offset)
			}
			if se.Offset < 0 || se.Offset > len(tc.input) {
				t.Errorf("Parse(%q) Offset %d outside input", tc.input, se.Offset)
			}
			if !strings.Contains(err.Error(), "offset") || !strings.Contains(err.Error(), se.Msg) {
				t.Errorf("Error() = %q, want offset and Msg", err.Error())
			}
			if !errors.Is(se.Unwrap(), tc.is) {
				t.Errorf("Unwrap() = %v, want %v", se.Unwrap(), tc.is)
			}
		})
	}
}

// roundTripEnv exercises every operand kind during the round-trip check.
var roundTripEnv = MapEnv{
	Properties: map[string]any{
		"a": 1, "b": 2, "c": "three", "shard": 50, "my key": 1, "1abc": 1,
	},
	StatusItems: map[string]any{
		"device.identifier.serial-number": "ZYXW4321", "s": "X", "a b": 1, "b": 2,
	},
}

func TestStringRoundTrip(t *testing.T) {
	t.Parallel()
	inputs := make([]string, 0, len(parseCases)+len(evalCases))
	for _, tc := range parseCases {
		inputs = append(inputs, tc.input)
	}
	for _, tc := range evalCases {
		inputs = append(inputs, tc.input)
	}
	for _, input := range inputs {
		p, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		canonical := p.String()
		again, err := Parse(canonical)
		if err != nil {
			t.Fatalf("Parse(%q) of canonical form of %q: %v", canonical, input, err)
		}
		if !reflect.DeepEqual(p.root, again.root) {
			t.Errorf("round trip of %q via %q changed the tree", input, canonical)
		}
		if got := again.String(); got != canonical {
			t.Errorf("String() not stable: %q then %q", canonical, got)
		}
		r1, e1 := p.Eval(roundTripEnv)
		r2, e2 := again.Eval(roundTripEnv)
		if r1 != r2 || (e1 == nil) != (e2 == nil) || errors.Is(e1, ErrType) != errors.Is(e2, ErrType) {
			t.Errorf("round trip of %q evaluates differently: (%v, %v) vs (%v, %v)", input, r1, e1, r2, e2)
		}
	}
}

func TestStringNil(t *testing.T) {
	t.Parallel()
	var p *Predicate
	if got := p.String(); got != "" {
		t.Errorf("nil String() = %q", got)
	}
	if got := p.Source(); got != "" {
		t.Errorf("nil Source() = %q", got)
	}
	if got := (&Predicate{}).String(); got != "" {
		t.Errorf("zero String() = %q", got)
	}
}

func TestMustParsePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustParse did not panic")
		}
		err, ok := r.(error)
		if !ok || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("MustParse panicked with %v, want ErrUnsupported", r)
		}
	}()
	MustParse(`SELF == 1`)
}

func TestMustParse(t *testing.T) {
	t.Parallel()
	if got := MustParse(`1==0`).String(); got != "1 == 0" {
		t.Errorf("MustParse = %q", got)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	if err := Validate(`@property(shard) <= 75`); err != nil {
		t.Errorf("Validate valid: %v", err)
	}
	err := Validate(`@property(shard) <= `)
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("Validate invalid = %v, want ErrSyntax", err)
	}
	if err := Validate(`@property(a) LIKE 'x'`); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Validate unsupported = %v, want ErrUnsupported", err)
	}
}

func TestInternalHelpers(t *testing.T) {
	t.Parallel()
	for op := cmpEq; op <= cmpEndsWith; op++ {
		if op.String() == "" {
			t.Errorf("op %d has no name", op)
		}
	}
	if cmpOp(99).allowsCaseModifier() {
		t.Error("unknown op allows [c]")
	}
	if (token{kind: tokKind(99)}).describe() != "token" {
		t.Error("unknown token describe")
	}
	if endsOperand(token{kind: tokKind(99)}) {
		t.Error("unknown token ends operand")
	}
	for kind := tokEOF; kind <= tokStatus; kind++ {
		if (token{kind: kind, text: "x"}).describe() == "" {
			t.Errorf("token kind %d has no description", kind)
		}
	}
}
