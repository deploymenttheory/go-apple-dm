package predicate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Sentinel errors. Errors returned by Parse and Validate wrap ErrSyntax or
// ErrUnsupported and are always of type *SyntaxError. Errors returned by
// Eval wrap ErrType when operand types are incompatible.
var (
	ErrSyntax      = errors.New("predicate: syntax error")
	ErrUnsupported = errors.New("predicate: unsupported construct")
	ErrType        = errors.New("predicate: type mismatch")
)

// SyntaxError describes why a predicate failed to parse.
type SyntaxError struct {
	// Offset is the byte offset in the source at which the problem was found.
	Offset int
	// Msg describes the problem and, for unsupported constructs, names the
	// construct.
	Msg string
	// Err is ErrSyntax or ErrUnsupported.
	Err error
}

// Error implements error.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%v at offset %d: %s", e.Err, e.Offset, e.Msg)
}

// Unwrap returns the sentinel error.
func (e *SyntaxError) Unwrap() error {
	return e.Err
}

func syntaxErr(offset int, format string, args ...any) error {
	return &SyntaxError{Offset: offset, Msg: fmt.Sprintf(format, args...), Err: ErrSyntax}
}

func unsupportedErr(offset int, format string, args ...any) error {
	return &SyntaxError{Offset: offset, Msg: fmt.Sprintf(format, args...) + " is not supported", Err: ErrUnsupported}
}

// Env supplies the values that @property and @status references resolve to.
// A reference whose lookup reports false evaluates to nil. Values may be
// nil, bool, string, any integer or float type, json.Number, or a slice of
// those.
type Env interface {
	// Property returns the activation property for key.
	Property(key string) (any, bool)
	// Status returns the status item at path.
	Status(path string) (any, bool)
}

// Properties is an Env backed by a map of activation properties. Status
// lookups are never found.
type Properties map[string]any

// Property implements Env.
func (p Properties) Property(key string) (any, bool) {
	v, ok := p[key]
	return v, ok
}

// Status implements Env and always reports a missing item.
func (Properties) Status(string) (any, bool) {
	return nil, false
}

// MapEnv is an Env backed by two maps. A nil map simply has no entries.
type MapEnv struct {
	// Properties resolves @property(key).
	Properties map[string]any
	// StatusItems resolves @status(path).
	StatusItems map[string]any
}

// Property implements Env.
func (m MapEnv) Property(key string) (any, bool) {
	v, ok := m.Properties[key]
	return v, ok
}

// Status implements Env.
func (m MapEnv) Status(path string) (any, bool) {
	v, ok := m.StatusItems[path]
	return v, ok
}

// Predicate is a parsed activation predicate.
type Predicate struct {
	root expr
	src  string
}

// Parse parses a predicate format string. The returned error wraps ErrSyntax
// or ErrUnsupported and is of type *SyntaxError.
func Parse(s string) (*Predicate, error) {
	root, err := parse(s)
	if err != nil {
		return nil, err
	}
	return &Predicate{root: root, src: s}, nil
}

// MustParse is like Parse but panics on error. It is intended for
// predicates fixed at compile time.
func MustParse(s string) *Predicate {
	p, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return p
}

// Validate reports whether s is a predicate this package can evaluate.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

// Source returns the string the predicate was parsed from.
func (p *Predicate) Source() string {
	if p == nil {
		return ""
	}
	return p.src
}

// Eval evaluates the predicate against env. A nil env resolves every
// reference to nil. Errors wrap ErrType.
func (p *Predicate) Eval(env Env) (bool, error) {
	if p == nil || p.root == nil {
		return false, fmt.Errorf("%w: empty predicate", ErrSyntax)
	}
	if env == nil {
		env = emptyEnv{}
	}
	return evalExpr(p.root, env)
}

// String renders the predicate canonically: upper-case keywords, single
// quoted strings, symbolic operators in their primary spelling and
// parentheses only where precedence requires them. Parsing the result yields
// a predicate equal to p.
func (p *Predicate) String() string {
	if p == nil || p.root == nil {
		return ""
	}
	var sb strings.Builder
	renderExpr(&sb, p.root)
	return sb.String()
}

func renderExpr(sb *strings.Builder, e expr) {
	switch n := e.(type) {
	case *constExpr:
		if n.value {
			sb.WriteString("TRUEPREDICATE")
		} else {
			sb.WriteString("FALSEPREDICATE")
		}
	case *notExpr:
		sb.WriteString("NOT ")
		renderChild(sb, n.operand, precNot)
	case *compoundExpr:
		renderCompound(sb, n)
	case *compareExpr:
		renderCompare(sb, n)
	}
}

// renderChild writes e, wrapping it in parentheses when its precedence is
// lower than min.
func renderChild(sb *strings.Builder, e expr, minPrec int) {
	if e.precedence() < minPrec {
		sb.WriteByte('(')
		renderExpr(sb, e)
		sb.WriteByte(')')
		return
	}
	renderExpr(sb, e)
}

// renderCompound keeps the tree shape: the left child may share the
// operator's precedence because the operators are left-associative, but the
// right child needs parentheses at the same level.
func renderCompound(sb *strings.Builder, n *compoundExpr) {
	prec := n.precedence()
	renderChild(sb, n.left, prec)
	if n.op == logicalAnd {
		sb.WriteString(" AND ")
	} else {
		sb.WriteString(" OR ")
	}
	renderChild(sb, n.right, prec+1)
}

func renderCompare(sb *strings.Builder, n *compareExpr) {
	renderOperand(sb, n.left)
	sb.WriteByte(' ')
	sb.WriteString(n.op.String())
	if n.caseInsensitive {
		sb.WriteString("[c]")
	}
	sb.WriteByte(' ')
	renderOperand(sb, n.right)
}

func renderOperand(sb *strings.Builder, o operand) {
	switch v := o.(type) {
	case *propertyRef:
		sb.WriteString("@property(")
		renderKey(sb, v.key)
		sb.WriteByte(')')
	case *statusRef:
		sb.WriteString("@status(")
		renderKey(sb, v.path)
		sb.WriteByte(')')
	case *literal:
		renderLiteral(sb, v)
	}
}

// renderKey writes a reference key bare when the grammar allows it and
// quoted otherwise.
func renderKey(sb *strings.Builder, key string) {
	bare := key != ""
	for i := 0; i < len(key) && bare; i++ {
		bare = isKeyChar(key[i])
	}
	if bare {
		sb.WriteString(key)
		return
	}
	renderString(sb, key)
}

func renderLiteral(sb *strings.Builder, lit *literal) {
	switch lit.kind {
	case litNull:
		sb.WriteString("NULL")
	case litBool:
		if lit.boolean {
			sb.WriteString("TRUE")
		} else {
			sb.WriteString("FALSE")
		}
	case litNumber:
		sb.WriteString(strconv.FormatFloat(lit.number, 'g', -1, 64))
	case litString:
		renderString(sb, lit.text)
	case litAggregate:
		sb.WriteByte('{')
		for i := range lit.items {
			if i > 0 {
				sb.WriteString(", ")
			}
			renderLiteral(sb, &lit.items[i])
		}
		sb.WriteByte('}')
	}
}

// renderString writes s single-quoted. It works on bytes so that the
// original byte sequence survives a round trip even when it is not valid
// UTF-8.
func renderString(sb *strings.Builder, s string) {
	sb.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\r':
			sb.WriteString(`\r`)
		case c == '\t':
			sb.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(sb, `\u%04x`, c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('\'')
}
