package canonjson_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/canonjson"
)

// The JSON escapes and non-ASCII characters below are built from code points
// at run time so that every test vector states its bytes unambiguously.
var (
	backslash = "\\"
	euro      = string(rune(0x20ac))
	del       = string(rune(0x7f))
	pad       = string(rune(0x80))
	oUmlaut   = string(rune(0xf6))
	eAcute    = string(rune(0xe9))
	smiley    = string(rune(0x1f602))
	dalet     = string(rune(0xfb33))
	lineSep   = string(rune(0x2028))
)

// esc returns the lowercase JSON escape for one UTF-16 code unit.
func esc(unit int) string { return backslash + "u" + fmt.Sprintf("%04x", unit) }

// escUpper returns the same escape with uppercase hex digits.
func escUpper(unit int) string { return backslash + "u" + fmt.Sprintf("%04X", unit) }

// canonCase pairs an input document with its expected canonical form.
type canonCase struct {
	name string
	in   string
	want string
}

// rfcSampleInput is the sample input from RFC 8785 section 3.2.3 (also the
// RFC's test data), covering numbers, string escapes, and literals.
var rfcSampleInput = "{\n" +
	`  "numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],` + "\n" +
	`  "string": "` + esc(0x20ac) + `$` + escUpper(0x0f) + esc(0x0a) + `A'` +
	esc(0x42) + esc(0x22) + esc(0x5c) + `\\\"\/",` + "\n" +
	`  "literals": [null, true, false]` + "\n}"

// rfcSampleOutput is the expected output published for rfcSampleInput. The
// RFC prints the Euro sign as an escape for readability; the bytes are UTF-8.
var rfcSampleOutput = `{"literals":[null,true,false],` +
	`"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],` +
	`"string":"` + euro + `$` + esc(0x0f) + `\nA'B\"\\\\\"/"}`

// rfcOrderInput is the property sorting example from RFC 8785 section 3.2.3.
var rfcOrderInput = "{\n" +
	`  "` + esc(0x20ac) + `": "Euro Sign",` + "\n" +
	`  "\r": "Carriage Return",` + "\n" +
	`  "` + esc(0x0a) + `": "Newline",` + "\n" +
	`  "1": "One",` + "\n" +
	`  "` + esc(0x80) + `": "Control` + esc(0x7f) + `",` + "\n" +
	`  "` + esc(0xd83d) + esc(0xde02) + `": "Smiley",` + "\n" +
	`  "` + esc(0xf6) + `": "Latin Small Letter O With Diaeresis",` + "\n" +
	`  "` + esc(0xfb33) + `": "Hebrew Letter Dalet With Dagesh",` + "\n" +
	`  "</script>": "Browser Challenge"` + "\n}"

// rfcOrderOutput is the RFC's expected output with the non-ASCII names and
// U+007F emitted as literal UTF-8 rather than the escapes the RFC prints.
var rfcOrderOutput = `{"\n":"Newline","\r":"Carriage Return","1":"One","</script>":"Browser Challenge","` +
	pad + `":"Control` + del + `","` +
	oUmlaut + `":"Latin Small Letter O With Diaeresis","` +
	euro + `":"Euro Sign","` +
	smiley + `":"Smiley","` +
	dalet + `":"Hebrew Letter Dalet With Dagesh"}`

// rfcCases are the examples published in RFC 8785 section 3.2.3.
var rfcCases = []canonCase{
	{"sample", rfcSampleInput, rfcSampleOutput},
	{"key order", rfcOrderInput, rfcOrderOutput},
}

// keyOrderCases exercise ordering of member names by UTF-16 code units.
var keyOrderCases = []canonCase{
	{
		// U+10000 is D800 DC00 in UTF-16, so it sorts before U+E000 and
		// U+FFFF even though its code point is larger.
		"utf16 order beats code point order",
		`{"` + esc(0xffff) + `":1,"` + esc(0xd800) + esc(0xdc00) + `":2,"` + esc(0xe000) + `":3}`,
		`{"` + string(rune(0x10000)) + `":2,"` + string(rune(0xe000)) + `":3,"` +
			string(rune(0xffff)) + `":1}`,
	},
	{
		"ascii key order",
		`{"b":1,"ab":2,"a":3,"":4,"B":5,"10":6,"2":7}`,
		`{"":4,"10":6,"2":7,"B":5,"a":3,"ab":2,"b":1}`,
	},
}

// structureCases cover whitespace, nesting, empty containers, and literals.
var structureCases = []canonCase{
	{"whitespace removed", " \t\r\n{ \"a\" : [ 1 , 2 ] , \"b\" : { } } \n", `{"a":[1,2],"b":{}}`},
	{
		"nested containers",
		`{"b":{"d":[{"f":null,"e":[]},{}],"c":true},"a":[[[]]]}`,
		`{"a":[[[]]],"b":{"c":true,"d":[{"e":[],"f":null},{}]}}`,
	},
	{"empty object", `{ }`, `{}`},
	{"empty array", `[ ]`, `[]`},
	{"scalar string", ` "x" `, `"x"`},
	{"literal true", ` true`, `true`},
	{"literal false", `false `, `false`},
	{"literal null", `null`, `null`},
}

// stringCases cover the JCS string escaping rules.
var stringCases = []canonCase{
	{
		"control escapes",
		`"` + esc(0) + escUpper(0x1f) + esc(0x7f) + `\b\f\n\r\t\"\\\/` + esc(0x2028) + esc(0xe9) + `"`,
		`"` + esc(0) + esc(0x1f) + del + `\b\f\n\r\t\"\\/` + lineSep + eAcute + `"`,
	},
	{
		"long escapes become short escapes",
		`"` + esc(8) + esc(0xc) + esc(0xa) + esc(0xd) + esc(9) + esc(0x22) + esc(0x5c) + esc(0x2f) + `"`,
		`"\b\f\n\r\t\"\\/"`,
	},
	{"literal del byte", `"` + del + `"`, `"` + del + `"`},
	{"surrogate pair", `"` + esc(0xd83d) + esc(0xde02) + `"`, `"` + smiley + `"`},
	{"escaped bmp becomes utf8", `"` + esc(0x20ac) + `"`, `"` + euro + `"`},
}

// numberCases cover ECMAScript Number::toString formatting of doubles.
var numberCases = []canonCase{
	{"number rfc 333333333.33333329", `333333333.33333329`, `333333333.3333333`},
	{"number 1E+30", `1E+30`, `1e+30`},
	{"number 1E30", `1E30`, `1e+30`},
	{"number 4.50", `4.50`, `4.5`},
	{"number 2e-3", `2e-3`, `0.002`},
	{"number 2.5e-7", `2.5e-7`, `2.5e-7`},
	{"number -0", `-0`, `0`},
	{"number -0.0", `-0.0`, `0`},
	{"number 0.0e5", `0.0e5`, `0`},
	{"number 1.0", `1.0`, `1`},
	{"number 100", `100`, `100`},
	{"number 1.5e3", `1.5e3`, `1500`},
	{"number -1.5", `-1.5`, `-1.5`},
	{"number 1e20 plain", `1e20`, `100000000000000000000`},
	{"number below 1e21 plain", `999999999999999900000`, `999999999999999900000`},
	{"number 1e21 exponent", `1e21`, `1e+21`},
	{"number 2^68 integer", `295147905179352825856`, `295147905179352830000`},
	{"number 2^68 exponent", `2.9514790517935283e+20`, `295147905179352830000`},
	{"number 0.000001 plain", `0.000001`, `0.000001`},
	{"number 1e-6 plain", `1e-6`, `0.000001`},
	{"number 1e-5 plain", `1e-5`, `0.00001`},
	{"number 1e-7 exponent", `1e-7`, `1e-7`},
	{"number 1e-27", `0.000000000000000000000000001`, `1e-27`},
	{"number -3.3333333333333333e-6", `-3.3333333333333333e-6`, `-0.0000033333333333333333`},
	{"number 2^53+1 loses precision", `9007199254740993`, `9007199254740992`},
	{"number -2^53-1 loses precision", `-9007199254740993`, `-9007199254740992`},
	{"number 20 digit integer", `12345678901234567890`, `12345678901234567000`},
	{"number smallest subnormal", `5e-324`, `5e-324`},
	{"number underflow to zero", `1e-400`, `0`},
	{"number negative underflow to zero", `-1e-400`, `0`},
	{"number max double", `1.7976931348623157e+308`, `1.7976931348623157e+308`},
	{"number in object", `{"n":[1E2,1E-2]}`, `{"n":[100,0.01]}`},
}

// allCases returns every successful case for the idempotency check and the
// fuzz seed corpus.
func allCases() []canonCase {
	return slices.Concat(rfcCases, keyOrderCases, structureCases, stringCases, numberCases)
}

// The subtest names below are cited as evidence by decision record 0019.
func TestCanonicalize(t *testing.T) {
	t.Parallel()
	t.Run("RFCVectors", func(t *testing.T) {
		t.Parallel()
		runCanonCases(t, rfcCases)
		t.Run("AppendixB", testAppendixB)
	})
	t.Run("KeysSortedByUTF16", func(t *testing.T) { t.Parallel(); runCanonCases(t, keyOrderCases) })
	t.Run("Structure", func(t *testing.T) { t.Parallel(); runCanonCases(t, structureCases) })
	t.Run("Strings", func(t *testing.T) { t.Parallel(); runCanonCases(t, stringCases) })
	t.Run("Numbers", func(t *testing.T) { t.Parallel(); runCanonCases(t, numberCases) })
	t.Run("Idempotent", func(t *testing.T) {
		t.Parallel()
		for _, tc := range allCases() {
			got, err := canonjson.Canonicalize([]byte(tc.in))
			if err != nil {
				t.Fatalf("%s: Canonicalize(%q) error: %v", tc.name, tc.in, err)
			}
			again, err := canonjson.Canonicalize(got)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("%s: not idempotent: %q -> %q (err %v)", tc.name, got, again, err)
			}
			if !canonjson.Valid(got) {
				t.Fatalf("%s: Valid(%q) = false, want true", tc.name, got)
			}
		}
	})
}

// runCanonCases checks each case's canonical form, the Valid verdict on the
// input, and agreement with the standard library's own RFC 8785 implementation
// in jsontext, which must match wherever both accept the input.
func runCanonCases(t *testing.T, cases []canonCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonjson.Canonicalize([]byte(tc.in))
			if err != nil {
				t.Fatalf("Canonicalize(%q) error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Fatalf("Canonicalize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
			if canonjson.Valid([]byte(tc.in)) != (tc.in == tc.want) {
				t.Fatalf("Valid(%q) = %v, want %v", tc.in, tc.in != tc.want, tc.in == tc.want)
			}
			ref := jsontext.Value(tc.in)
			if err := ref.Canonicalize(); err != nil {
				t.Fatalf("jsontext.Value.Canonicalize(%q) error: %v", tc.in, err)
			}
			if !bytes.Equal(ref, got) {
				t.Fatalf("disagrees with jsontext: got %q, jsontext %q", got, ref)
			}
		})
	}
}

// testAppendixB reproduces the RFC 8785 Appendix B serialization samples by
// feeding each IEEE 754 bit pattern through Go's shortest formatting, which is
// a valid JSON lexeme, and checking the JCS rendering.
func testAppendixB(t *testing.T) {
	t.Parallel()
	vectors := []struct {
		bits uint64
		want string
	}{
		{0x0000000000000000, "0"},
		{0x8000000000000000, "0"},
		{0x0000000000000001, "5e-324"},
		{0x7fefffffffffffff, "1.7976931348623157e+308"},
		{0x4340000000000000, "9007199254740992"},
		{0xc340000000000000, "-9007199254740992"},
		{0x4430000000000000, "295147905179352830000"},
		{0x44b52d02c7e14af5, "9.999999999999997e+22"},
		{0x44b52d02c7e14af6, "1e+23"},
		{0x44b52d02c7e14af7, "1.0000000000000001e+23"},
		{0x444b1ae4d6e2ef4e, "999999999999999700000"},
		{0x444b1ae4d6e2ef4f, "999999999999999900000"},
		{0x444b1ae4d6e2ef50, "1e+21"},
		{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7"},
		{0x3eb0c6f7a0b5ed8d, "0.000001"},
		{0x41b3de4355555553, "333333333.3333332"},
		{0x41b3de4355555554, "333333333.33333325"},
		{0x41b3de4355555555, "333333333.3333333"},
		{0x41b3de4355555556, "333333333.3333334"},
		{0x41b3de4355555557, "333333333.33333343"},
		{0xbecbf647612f3696, "-0.0000033333333333333333"},
		{0x43143ff3c1cb0959, "1424953923781206.2"},
	}
	for _, v := range vectors {
		in := strconv.FormatFloat(math.Float64frombits(v.bits), 'g', -1, 64)
		got, err := canonjson.Canonicalize([]byte(in))
		if err != nil {
			t.Fatalf("%016x (%s): %v", v.bits, in, err)
		}
		if string(got) != v.want {
			t.Errorf("%016x (%s): got %s, want %s", v.bits, in, got, v.want)
		}
	}
}

// errorCase pairs malformed input with the sentinel it must match.
type errorCase struct {
	name string
	in   string
	want error
}

var errorCases = []errorCase{
	{"empty", "", canonjson.ErrInvalidJSON},
	{"whitespace only", " \n\t", canonjson.ErrInvalidJSON},
	{"trailing garbage", `[1] x`, canonjson.ErrInvalidJSON},
	{"second value", `[1][2]`, canonjson.ErrInvalidJSON},
	{"trailing comma", `[1,]`, canonjson.ErrInvalidJSON},
	{"duplicate names", `{"a":1,"a":2}`, canonjson.ErrInvalidJSON},
	{"duplicate names after unescaping", `{"a":1,"` + esc(0x61) + `":2}`, canonjson.ErrInvalidJSON},
	{"duplicate empty names", `{"":1,"":2}`, canonjson.ErrInvalidJSON},
	{"invalid utf8 in string", `"` + string([]byte{0xff}) + `"`, canonjson.ErrInvalidJSON},
	{"invalid utf8 in name", `{"` + string([]byte{0xc0}) + `":1}`, canonjson.ErrInvalidJSON},
	{"lone high surrogate", `"` + esc(0xd800) + `"`, canonjson.ErrInvalidJSON},
	{"lone low surrogate", `"` + esc(0xdc00) + `"`, canonjson.ErrInvalidJSON},
	{"raw control character", `"` + string(rune(1)) + `"`, canonjson.ErrInvalidJSON},
	{"NaN literal", `NaN`, canonjson.ErrInvalidJSON},
	{"Infinity literal", `Infinity`, canonjson.ErrInvalidJSON},
	{"negative Infinity literal", `-Infinity`, canonjson.ErrInvalidJSON},
	{"NaN in array", `[NaN]`, canonjson.ErrInvalidJSON},
	{"unterminated string", `"abc`, canonjson.ErrInvalidJSON},
	{"unterminated array", `[1,`, canonjson.ErrInvalidJSON},
	{"unterminated object", `{"a":`, canonjson.ErrInvalidJSON},
	{"missing value", `{"a"}`, canonjson.ErrInvalidJSON},
	{"leading zero", `01`, canonjson.ErrInvalidJSON},
	{"leading plus", `+1`, canonjson.ErrInvalidJSON},
	{"bare fraction", `.5`, canonjson.ErrInvalidJSON},
	{"trailing point", `1.`, canonjson.ErrInvalidJSON},
	{"single quotes", `'a'`, canonjson.ErrInvalidJSON},
	{"truncated literal", `tru`, canonjson.ErrInvalidJSON},
	{"overflow", `1e400`, canonjson.ErrNumber},
	{"negative overflow", `-1e400`, canonjson.ErrNumber},
	{"overflow in array", `[1, 1e400]`, canonjson.ErrNumber},
	{"overflow in object", `{"a":1e400}`, canonjson.ErrNumber},
	{"overflow by digits", "1" + strings.Repeat("0", 400), canonjson.ErrNumber},
	{"arrays one too deep", nested("[", "]", canonjson.MaxDepth+1), canonjson.ErrDepth},
	{"arrays 130 deep", nested("[", "]", 130), canonjson.ErrDepth},
	{"objects one too deep", nested(`{"a":`, "}", canonjson.MaxDepth+1), canonjson.ErrDepth},
	{
		"array inside objects one too deep",
		`{"a":` + nested("[", "]", canonjson.MaxDepth) + `}`,
		canonjson.ErrDepth,
	},
}

// nested returns n copies of open followed by n copies of closing.
func nested(open, closing string, n int) string {
	return strings.Repeat(open, n) + strings.Repeat(closing, n)
}

func TestCanonicalizeErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonjson.Canonicalize([]byte(tc.in))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Canonicalize(%.40q) error = %v, want %v", tc.in, err, tc.want)
			}
			if got != nil {
				t.Fatalf("Canonicalize(%.40q) returned %q with error", tc.in, got)
			}
			if canonjson.Valid([]byte(tc.in)) {
				t.Fatalf("Valid(%.40q) = true for invalid input", tc.in)
			}
		})
	}
}

func TestCanonicalizeErrorMessages(t *testing.T) {
	t.Parallel()
	_, err := canonjson.Canonicalize(nil)
	if err == nil || !strings.Contains(err.Error(), "empty input") {
		t.Fatalf("empty input error = %v", err)
	}
	_, err = canonjson.Canonicalize([]byte(`[1,`))
	if err == nil || strings.Contains(err.Error(), "empty input") {
		t.Fatalf("truncated input error = %v, want a syntax error", err)
	}
	_, err = canonjson.Canonicalize([]byte(`{"k":1,"k":2}`))
	if !errors.Is(err, jsontext.ErrDuplicateName) {
		t.Fatalf("duplicate name error = %v, want it to wrap jsontext.ErrDuplicateName", err)
	}
	_, err = canonjson.Canonicalize([]byte(`1e999`))
	if !errors.Is(err, strconv.ErrRange) {
		t.Fatalf("overflow error = %v, want it to wrap strconv.ErrRange", err)
	}
}

func TestMaxDepth(t *testing.T) {
	t.Parallel()
	in := nested("[", "]", canonjson.MaxDepth)
	got, err := canonjson.Canonicalize([]byte(in))
	if err != nil || string(got) != in {
		t.Fatalf("depth %d arrays: got %.20q, err %v", canonjson.MaxDepth, got, err)
	}
	// MaxDepth-2 wrapping objects plus {"b":[]} put the array at depth MaxDepth.
	in = strings.Repeat(`{"a":`, canonjson.MaxDepth-2) + `{"b":[]}` +
		strings.Repeat("}", canonjson.MaxDepth-2)
	if _, err := canonjson.Canonicalize([]byte(in)); err != nil {
		t.Fatalf("array at depth %d inside objects: %v", canonjson.MaxDepth, err)
	}
	in = strings.Repeat(`{"a":`, canonjson.MaxDepth-1) + `{"b":[]}` +
		strings.Repeat("}", canonjson.MaxDepth-1)
	if _, err := canonjson.Canonicalize([]byte(in)); !errors.Is(err, canonjson.ErrDepth) {
		t.Fatalf("array at depth %d inside objects: err %v, want ErrDepth", canonjson.MaxDepth+1, err)
	}
}

func TestAppend(t *testing.T) {
	t.Parallel()
	dst := []byte("prefix:")
	got, err := canonjson.Append(dst, []byte(`{ "b" : 1 , "a" : 2 }`))
	if err != nil {
		t.Fatalf("Append error: %v", err)
	}
	if string(got) != `prefix:{"a":2,"b":1}` {
		t.Fatalf("Append = %q", got)
	}
	if string(dst) != "prefix:" {
		t.Fatalf("dst mutated to %q", dst)
	}

	// The error path returns exactly the original dst contents, even when
	// the slice has spare capacity that was written to before the failure.
	dst = make([]byte, 0, 64)
	dst = append(dst, "keep"...)
	got, err = canonjson.Append(dst, []byte(`{"a":[1,2,3,`))
	if !errors.Is(err, canonjson.ErrInvalidJSON) {
		t.Fatalf("Append error = %v, want ErrInvalidJSON", err)
	}
	if string(got) != "keep" {
		t.Fatalf("Append on error returned %q, want %q", got, "keep")
	}
	got, err = canonjson.Append(nil, []byte(`[1e400]`))
	if !errors.Is(err, canonjson.ErrNumber) || got != nil {
		t.Fatalf("Append(nil, overflow) = %q, %v", got, err)
	}
}

// marshalDoc has fields declared out of order and a map whose keys json/v2
// emits in insertion order; Marshal must sort both.
type marshalDoc struct {
	Zeta    int            `json:"zeta"`
	Members map[string]int `json:"members"`
	Alpha   string         `json:"alpha"`
	Ratio   float64        `json:"ratio"`
}

func TestMarshal(t *testing.T) {
	t.Parallel()
	doc := marshalDoc{
		Zeta:    1,
		Members: map[string]int{"b": 2, "a": 1, "B": 3},
		Alpha:   "x" + eAcute + "\n",
		Ratio:   1e21,
	}
	got, err := canonjson.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	want := `{"alpha":"x` + eAcute + `\n","members":{"B":3,"a":1,"b":2},"ratio":1e+21,"zeta":1}`
	if string(got) != want {
		t.Fatalf("Marshal =\n got %s\nwant %s", got, want)
	}
	if !canonjson.Valid(got) {
		t.Fatalf("Marshal output is not canonical: %s", got)
	}

	// Integers beyond 2^53 are doubles in JCS and lose precision.
	got, err = canonjson.Marshal(uint64(math.MaxUint64))
	if err != nil || string(got) != "18446744073709552000" {
		t.Fatalf("Marshal(MaxUint64) = %s, %v", got, err)
	}

	if _, err := canonjson.Marshal(make(chan int)); err == nil {
		t.Fatal("Marshal(chan) succeeded, want error")
	}
	if _, err := canonjson.Marshal(map[string]any{"f": func() {}}); err == nil {
		t.Fatal("Marshal(func) succeeded, want error")
	}
}

func TestValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{`{"a":1,"b":[true,null]}`, true},
		{`1`, true},
		{`"x"`, true},
		{`1.0`, false},
		{`{"b":1,"a":2}`, false},
		{`{"a":1} `, false},
		{`{"a" :1}`, false},
		{`"\/"`, false},
		{``, false},
		{`[1e400]`, false},
		{`{"a":1,"a":1}`, false},
	}
	for _, tc := range cases {
		if got := canonjson.Valid([]byte(tc.in)); got != tc.want {
			t.Errorf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func FuzzCanonicalize(f *testing.F) {
	for _, tc := range allCases() {
		f.Add([]byte(tc.in))
	}
	for _, tc := range errorCases {
		f.Add([]byte(tc.in))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := canonjson.Canonicalize(in)
		if err != nil {
			if out != nil {
				t.Fatalf("output %q returned alongside error %v", out, err)
			}
			return
		}
		if !canonjson.Valid(out) {
			t.Fatalf("Valid(%q) = false for canonical output of %q", out, in)
		}
		again, err := canonjson.Canonicalize(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not idempotent: %q -> %q (err %v)", out, again, err)
		}
		var fromIn, fromOut any
		if err := json.Unmarshal(in, &fromIn); err != nil {
			t.Fatalf("json/v2 rejects input %q that canonicalized to %q: %v", in, out, err)
		}
		if err := json.Unmarshal(out, &fromOut); err != nil {
			t.Fatalf("json/v2 rejects canonical output %q: %v", out, err)
		}
		if !reflect.DeepEqual(fromIn, fromOut) {
			t.Fatalf("meaning changed: %q -> %q (%#v vs %#v)", in, out, fromIn, fromOut)
		}
	})
}
