package predicate

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// typeErr builds an error wrapping ErrType.
func typeErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrType, fmt.Sprintf(format, args...))
}

// emptyEnv is used when Eval receives a nil Env.
type emptyEnv struct{}

func (emptyEnv) Property(string) (any, bool) { return nil, false }
func (emptyEnv) Status(string) (any, bool)   { return nil, false }

func evalExpr(e expr, env Env) (bool, error) {
	switch n := e.(type) {
	case *constExpr:
		return n.value, nil
	case *notExpr:
		v, err := evalExpr(n.operand, env)
		if err != nil {
			return false, err
		}
		return !v, nil
	case *compoundExpr:
		return evalCompound(n, env)
	case *compareExpr:
		return evalCompare(n, env)
	}
	return false, fmt.Errorf("%w: unknown expression %T", ErrSyntax, e)
}

// evalCompound short-circuits, so the right operand is not evaluated when
// the left one decides the result.
func evalCompound(n *compoundExpr, env Env) (bool, error) {
	left, err := evalExpr(n.left, env)
	if err != nil {
		return false, err
	}
	if n.op == logicalAnd && !left {
		return false, nil
	}
	if n.op == logicalOr && left {
		return true, nil
	}
	return evalExpr(n.right, env)
}

func evalCompare(n *compareExpr, env Env) (bool, error) {
	left, err := operandValue(n.left, env)
	if err != nil {
		return false, err
	}
	right, err := operandValue(n.right, env)
	if err != nil {
		return false, err
	}
	return compareValues(n.op, n.caseInsensitive, left, right)
}

// operandValue resolves an operand to one of nil, bool, float64, string or
// []any.
func operandValue(o operand, env Env) (any, error) {
	switch v := o.(type) {
	case *propertyRef:
		raw, ok := env.Property(v.key)
		if !ok {
			return nil, nil
		}
		return normalize(raw)
	case *statusRef:
		raw, ok := env.Status(v.path)
		if !ok {
			return nil, nil
		}
		return normalize(raw)
	case *literal:
		return literalValue(v), nil
	}
	return nil, fmt.Errorf("%w: unknown operand %T", ErrSyntax, o)
}

func literalValue(lit *literal) any {
	switch lit.kind {
	case litNull:
		return nil
	case litBool:
		return lit.boolean
	case litNumber:
		return lit.number
	case litString:
		return lit.text
	case litAggregate:
		items := make([]any, len(lit.items))
		for i := range lit.items {
			items[i] = literalValue(&lit.items[i])
		}
		return items
	}
	return nil
}

// normalize converts a value supplied by an Env into the evaluator's value
// model.
func normalize(raw any) (any, error) {
	switch v := raw.(type) {
	case nil, bool, string, float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, typeErr("invalid JSON number %q", string(v))
		}
		return f, nil
	case []any:
		return normalizeSlice(len(v), func(i int) any { return v[i] })
	case []string:
		return normalizeSlice(len(v), func(i int) any { return v[i] })
	}
	rv := reflect.ValueOf(raw)
	if rv.Kind() == reflect.Slice {
		return normalizeSlice(rv.Len(), func(i int) any { return rv.Index(i).Interface() })
	}
	return nil, typeErr("unsupported value type %T", raw)
}

func normalizeSlice(n int, at func(int) any) (any, error) {
	items := make([]any, n)
	for i := range n {
		v, err := normalize(at(i))
		if err != nil {
			return nil, err
		}
		items[i] = v
	}
	return items, nil
}

// typeName names a normalized value for error messages.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "aggregate"
	}
	return fmt.Sprintf("%T", v)
}

func compareValues(op cmpOp, ci bool, left, right any) (bool, error) {
	switch op {
	case cmpEq:
		return equalValues(ci, left, right)
	case cmpNe:
		eq, err := equalValues(ci, left, right)
		if err != nil {
			return false, err
		}
		return !eq, nil
	case cmpLt, cmpLe, cmpGt, cmpGe:
		return orderValues(op, ci, left, right)
	case cmpIn:
		return inValues(ci, left, right)
	case cmpContains, cmpBeginsWith, cmpEndsWith:
		return substringValues(op, ci, left, right)
	}
	return false, fmt.Errorf("%w: unknown operator %d", ErrSyntax, op)
}

func fold(ci bool, s string) string {
	if ci {
		return strings.ToLower(s)
	}
	return s
}

func equalValues(ci bool, left, right any) (bool, error) {
	if left == nil || right == nil {
		return left == nil && right == nil, nil
	}
	switch a := left.(type) {
	case float64:
		if b, ok := right.(float64); ok {
			return a == b, nil
		}
	case string:
		if b, ok := right.(string); ok {
			return fold(ci, a) == fold(ci, b), nil
		}
	case bool:
		if b, ok := right.(bool); ok {
			return a == b, nil
		}
	}
	return false, typeErr("cannot compare %s with %s for equality", typeName(left), typeName(right))
}

func orderValues(op cmpOp, ci bool, left, right any) (bool, error) {
	if left == nil || right == nil {
		return false, nil
	}
	var cmp int
	switch a := left.(type) {
	case float64:
		b, ok := right.(float64)
		if !ok {
			return false, typeErr("cannot order %s against %s", typeName(left), typeName(right))
		}
		cmp = compareFloats(a, b)
	case string:
		b, ok := right.(string)
		if !ok {
			return false, typeErr("cannot order %s against %s", typeName(left), typeName(right))
		}
		cmp = strings.Compare(fold(ci, a), fold(ci, b))
	default:
		return false, typeErr("cannot order %s against %s", typeName(left), typeName(right))
	}
	switch op {
	case cmpLt:
		return cmp < 0, nil
	case cmpLe:
		return cmp <= 0, nil
	case cmpGt:
		return cmp > 0, nil
	case cmpGe:
		return cmp >= 0, nil
	case cmpEq, cmpNe, cmpIn, cmpContains, cmpBeginsWith, cmpEndsWith:
		return false, nil
	}
	return false, nil
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func inValues(ci bool, left, right any) (bool, error) {
	items, ok := right.([]any)
	if !ok {
		return false, typeErr("IN requires an aggregate on the right, got %s", typeName(right))
	}
	if left == nil {
		return false, nil
	}
	for _, item := range items {
		eq, err := equalValues(ci, left, item)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
	return false, nil
}

func substringValues(op cmpOp, ci bool, left, right any) (bool, error) {
	if left == nil || right == nil {
		return false, nil
	}
	a, aok := left.(string)
	b, bok := right.(string)
	if !aok || !bok {
		return false, typeErr("%s requires strings, got %s and %s", op, typeName(left), typeName(right))
	}
	a, b = fold(ci, a), fold(ci, b)
	switch op {
	case cmpContains:
		return strings.Contains(a, b), nil
	case cmpBeginsWith:
		return strings.HasPrefix(a, b), nil
	case cmpEndsWith:
		return strings.HasSuffix(a, b), nil
	case cmpEq, cmpNe, cmpLt, cmpLe, cmpGt, cmpGe, cmpIn:
		return false, nil
	}
	return false, nil
}
