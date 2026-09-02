package predicate

import (
	"encoding/json"
	"errors"
	"testing"
)

// evalEnv covers every value type the evaluator normalises.
var evalEnv = MapEnv{
	Properties: map[string]any{
		"int":   5,
		"shard": 50,
		"i8":    int8(5),
		"i16":   int16(5),
		"i32":   int32(5),
		"i64":   int64(7),
		"u":     uint(5),
		"u8":    uint8(5),
		"u16":   uint16(5),
		"u32":   uint32(5),
		"u64":   uint64(5),
		"f32":   float32(2.5),
		"f":     2.5,
		"jn":    json.Number("3"),
		"badjn": json.Number("x"),
		"s":     "Hello",
		"b":     true,
		"nilv":  nil,
		"list":  []any{1, "a", nil},
		"strs":  []string{"x", "y"},
		"ints":  []int{1, 2},
		"bad":   []any{struct{}{}},
		"strct": struct{}{},
	},
	StatusItems: map[string]any{
		"device.identifier.serial-number": "ZYXW4321",
		"count":                           int64(3),
	},
}

type evalCase struct {
	input string
	want  bool
	err   bool
}

var evalCases = []evalCase{
	// Numbers of every width.
	{`@property(int) == 5`, true, false},
	{`@property(int) == 5.0`, true, false},
	{`@property(i8) == 5`, true, false},
	{`@property(i16) == 5`, true, false},
	{`@property(i32) == 5`, true, false},
	{`@property(i64) > 6`, true, false},
	{`@property(u) == 5`, true, false},
	{`@property(u8) == 5`, true, false},
	{`@property(u16) == 5`, true, false},
	{`@property(u32) == 5`, true, false},
	{`@property(u64) == 5`, true, false},
	{`@property(f32) == 2.5`, true, false},
	{`@property(f) < 3`, true, false},
	{`@property(jn) >= 3`, true, false},
	{`@property(int) != 5`, false, false},
	{`@property(int) <= 5`, true, false},
	{`@property(int) >= 6`, false, false},
	{`@property(int) < 5`, false, false},
	{`@property(int) > 4`, true, false},
	{`@property(int) > 5`, false, false},
	{`-1 < @property(int)`, true, false},
	{`1e1 == 10`, true, false},
	{`1==0`, false, false},
	{`2 <> 2`, false, false},
	// Strings.
	{`@property(s) == 'Hello'`, true, false},
	{`@property(s) == 'hello'`, false, false},
	{`@property(s) ==[c] 'hello'`, true, false},
	{`@property(s) !=[c] 'HELLO'`, false, false},
	{`@property(s) != 'HELLO'`, true, false},
	{`@property(s) < 'Zed'`, true, false},
	{`@property(s) > 'Hello'`, false, false},
	{`'a' <= 'a'`, true, false},
	{`'b' >= 'a'`, true, false},
	{`'B' < 'a'`, true, false},
	{`@property(s) == "Hello"`, true, false},
	// Substrings.
	{`@property(s) CONTAINS 'ell'`, true, false},
	{`@property(s) CONTAINS 'ELL'`, false, false},
	{`@property(s) CONTAINS[c] 'ELL'`, true, false},
	{`@property(s) BEGINSWITH 'He'`, true, false},
	{`@property(s) BEGINSWITH 'he'`, false, false},
	{`@property(s) BEGINSWITH[c] 'he'`, true, false},
	{`@property(s) ENDSWITH 'lo'`, true, false},
	{`@property(s) ENDSWITH 'Lo'`, false, false},
	{`@property(s) ENDSWITH[c] 'LO'`, true, false},
	// Booleans.
	{`@property(b) == TRUE`, true, false},
	{`@property(b) == YES`, true, false},
	{`@property(b) != FALSE`, true, false},
	{`@property(b) == NO`, false, false},
	{`TRUE == TRUE`, true, false},
	// Nil.
	{`@property(missing) == NULL`, true, false},
	{`NULL == NIL`, true, false},
	{`@property(missing) != 1`, true, false},
	{`@property(missing) == 1`, false, false},
	{`1 != @property(missing)`, true, false},
	{`@property(nilv) == NULL`, true, false},
	{`@property(missing) == @property(other)`, true, false},
	{`@property(missing) < 1`, false, false},
	{`1 > @property(missing)`, false, false},
	{`@property(missing) <= 'a'`, false, false},
	{`@property(missing) >= TRUE`, false, false},
	{`@property(missing) IN {1, NULL}`, false, false},
	{`@property(missing) CONTAINS 'a'`, false, false},
	{`'a' CONTAINS @property(missing)`, false, false},
	{`@property(missing) BEGINSWITH 'a'`, false, false},
	{`@property(missing) ENDSWITH 'a'`, false, false},
	{`NULL IN {NULL}`, false, false},
	// IN.
	{`@property(int) IN {1, 5}`, true, false},
	{`@property(int) IN {1, 2}`, false, false},
	{`@property(int) IN {}`, false, false},
	{`'a' IN {'A'}`, false, false},
	{`'a' IN[c] {'A'}`, true, false},
	{`'x' IN @property(strs)`, true, false},
	{`'z' IN @property(strs)`, false, false},
	{`1 IN @property(list)`, true, false},
	{`'a' IN @property(list)`, false, true},
	{`2 IN @property(ints)`, true, false},
	{`'a' IN {NULL, 'a'}`, true, false},
	// Status.
	{`@status(device.identifier.serial-number) == 'ZYXW4321'`, true, false},
	{`@status(device.identifier.serial-number) BEGINSWITH 'ZYXW'`, true, false},
	{`@status(count) == 3`, true, false},
	{`@status(missing) == NULL`, true, false},
	// Constants and compounds.
	{`TRUEPREDICATE`, true, false},
	{`FALSEPREDICATE`, false, false},
	{`NOT TRUEPREDICATE`, false, false},
	{`NOT NOT TRUEPREDICATE`, true, false},
	{`TRUEPREDICATE AND FALSEPREDICATE`, false, false},
	{`TRUEPREDICATE AND TRUEPREDICATE`, true, false},
	{`FALSEPREDICATE OR TRUEPREDICATE`, true, false},
	{`FALSEPREDICATE OR FALSEPREDICATE`, false, false},
	{`FALSEPREDICATE OR TRUEPREDICATE AND TRUEPREDICATE`, true, false},
	{`TRUEPREDICATE OR TRUEPREDICATE AND FALSEPREDICATE`, true, false},
	{`(TRUEPREDICATE OR TRUEPREDICATE) AND FALSEPREDICATE`, false, false},
	{`(@property(shard) <= 75)`, true, false},
	{`@property(int) == 5 && @property(s) == 'Hello' || FALSEPREDICATE`, true, false},
	// Short-circuit.
	{`FALSEPREDICATE AND @property(s) == 1`, false, false},
	{`TRUEPREDICATE OR @property(s) == 1`, true, false},
	// Type mismatches.
	{`TRUEPREDICATE AND @property(s) == 1`, false, true},
	{`FALSEPREDICATE OR @property(s) == 1`, false, true},
	{`@property(s) == 1 AND TRUEPREDICATE`, false, true},
	{`@property(s) == 1 OR TRUEPREDICATE`, false, true},
	{`NOT @property(s) == 1`, false, true},
	{`@property(s) == 1`, false, true},
	{`@property(s) != 1`, false, true},
	{`@property(int) == 'x'`, false, true},
	{`@property(b) == 1`, false, true},
	{`@property(b) == 'true'`, false, true},
	{`@property(b) < TRUE`, false, true},
	{`@property(b) > FALSE`, false, true},
	{`@property(int) < 'a'`, false, true},
	{`@property(s) >= 1`, false, true},
	{`@property(s) CONTAINS 1`, false, true},
	{`1 CONTAINS 'a'`, false, true},
	{`@property(int) BEGINSWITH 'a'`, false, true},
	{`@property(s) ENDSWITH TRUE`, false, true},
	{`@property(int) IN 5`, false, true},
	{`1 IN @property(missing)`, false, true},
	{`1 IN @property(s)`, false, true},
	{`'a' IN {1}`, false, true},
	{`{1} == {1}`, false, true},
	{`{1} < {1}`, false, true},
	{`@property(badjn) == 1`, false, true},
	{`@property(strct) == 1`, false, true},
	{`@property(bad) == 1`, false, true},
	{`@property(list) == 'a'`, false, true},
	{`@property(list) IN {1}`, false, true},
	{`@property(int) == @property(bad)`, false, true},
	{`@status(count) == @property(strct)`, false, true},
}

func TestEvalTable(t *testing.T) {
	t.Parallel()
	for _, tc := range evalCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			p, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.input, err)
			}
			got, err := p.Eval(evalEnv)
			if tc.err {
				if !errors.Is(err, ErrType) {
					t.Fatalf("Eval(%q) error = %v, want ErrType", tc.input, err)
				}
				if got {
					t.Fatalf("Eval(%q) = true with error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Eval(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("Eval(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestEvalProperties(t *testing.T) {
	t.Parallel()
	env := Properties{"shard": 42, "name": "alpha"}
	cases := map[string]bool{
		`@property(shard) <= 75`:        true,
		`@property(name) ==[c] 'ALPHA'`: true,
		`@status(anything) == NULL`:     true,
		`@status(anything) == 1`:        false,
		`@property(other) == NULL`:      true,
	}
	for input, want := range cases {
		got, err := MustParse(input).Eval(env)
		if err != nil {
			t.Fatalf("Eval(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("Eval(%q) = %v, want %v", input, got, want)
		}
	}
	if _, ok := env.Status("x"); ok {
		t.Error("Properties.Status found an item")
	}
	if _, ok := env.Property("missing"); ok {
		t.Error("Properties.Property found a missing key")
	}
}

func TestEvalNilEnv(t *testing.T) {
	t.Parallel()
	got, err := MustParse(`@property(a) == NULL AND @status(b) == NULL`).Eval(nil)
	if err != nil || !got {
		t.Errorf("Eval(nil) = (%v, %v), want (true, nil)", got, err)
	}
	got, err = MustParse(`@property(a) == 1`).Eval(MapEnv{})
	if err != nil || got {
		t.Errorf("Eval(MapEnv{}) = (%v, %v), want (false, nil)", got, err)
	}
	if _, ok := (MapEnv{}).Status("x"); ok {
		t.Error("empty MapEnv.Status found an item")
	}
}

func TestEvalNilPredicate(t *testing.T) {
	t.Parallel()
	var p *Predicate
	if _, err := p.Eval(evalEnv); !errors.Is(err, ErrSyntax) {
		t.Errorf("nil Eval error = %v, want ErrSyntax", err)
	}
	if _, err := (&Predicate{}).Eval(evalEnv); !errors.Is(err, ErrSyntax) {
		t.Errorf("zero Eval error = %v, want ErrSyntax", err)
	}
}

type bogusExpr struct{}

func (bogusExpr) precedence() int { return precPrimary }

type bogusOperand struct{}

func (bogusOperand) isOperand() {}

// TestEvalInternalGuards covers the defensive branches that a parsed tree
// can never reach.
func TestEvalInternalGuards(t *testing.T) {
	t.Parallel()
	if _, err := evalExpr(bogusExpr{}, MapEnv{}); !errors.Is(err, ErrSyntax) {
		t.Errorf("bogus expr error = %v", err)
	}
	if _, err := operandValue(bogusOperand{}, MapEnv{}); !errors.Is(err, ErrSyntax) {
		t.Errorf("bogus operand error = %v", err)
	}
	if _, err := compareValues(cmpOp(99), false, 1.0, 1.0); !errors.Is(err, ErrSyntax) {
		t.Errorf("bogus op error = %v", err)
	}
	if got, err := orderValues(cmpEq, false, 1.0, 1.0); got || err != nil {
		t.Errorf("orderValues with equality op = (%v, %v)", got, err)
	}
	if got, err := orderValues(cmpOp(99), false, 1.0, 1.0); got || err != nil {
		t.Errorf("orderValues with bogus op = (%v, %v)", got, err)
	}
	if got, err := substringValues(cmpEq, false, "a", "a"); got || err != nil {
		t.Errorf("substringValues with equality op = (%v, %v)", got, err)
	}
	if got, err := substringValues(cmpOp(99), false, "a", "a"); got || err != nil {
		t.Errorf("substringValues with bogus op = (%v, %v)", got, err)
	}
	if got := literalValue(&literal{kind: litKind(99)}); got != nil {
		t.Errorf("bogus literal value = %v", got)
	}
	if got := typeName(struct{}{}); got != "struct {}" {
		t.Errorf("typeName = %q", got)
	}
	if got := compareFloats(1, 1); got != 0 {
		t.Errorf("compareFloats(1, 1) = %d", got)
	}
	if p := (&Predicate{root: bogusExpr{}}); p.String() != "" {
		t.Errorf("bogus expr String() = %q", p.String())
	}
	if p := (&Predicate{root: &compareExpr{left: bogusOperand{}, right: bogusOperand{}}}); p.String() != " == " {
		t.Errorf("bogus operand String() = %q", p.String())
	}
}
