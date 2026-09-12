package predicate

// Precedence levels used when rendering a predicate back to source. A child
// expression is parenthesised when its precedence is lower than the level
// its parent requires.
const (
	precOr = iota + 1
	precAnd
	precNot
	precPrimary
)

// logicalOp identifies a compound predicate operator.
type logicalOp int

// Compound operators.
const (
	logicalAnd logicalOp = iota
	logicalOr
)

// cmpOp identifies a comparison operator.
type cmpOp int

// Comparison operators in canonical order.
const (
	cmpEq cmpOp = iota
	cmpNe
	cmpLt
	cmpLe
	cmpGt
	cmpGe
	cmpIn
	cmpContains
	cmpBeginsWith
	cmpEndsWith
)

var cmpOpNames = [...]string{
	cmpEq:         "==",
	cmpNe:         "!=",
	cmpLt:         "<",
	cmpLe:         "<=",
	cmpGt:         ">",
	cmpGe:         ">=",
	cmpIn:         "IN",
	cmpContains:   "CONTAINS",
	cmpBeginsWith: "BEGINSWITH",
	cmpEndsWith:   "ENDSWITH",
}

// String returns the canonical spelling of the operator.
func (op cmpOp) String() string {
	return cmpOpNames[op]
}

// allowsCaseModifier reports whether the [c] modifier may be attached.
func (op cmpOp) allowsCaseModifier() bool {
	switch op {
	case cmpEq, cmpNe, cmpIn, cmpContains, cmpBeginsWith, cmpEndsWith:
		return true
	case cmpLt, cmpLe, cmpGt, cmpGe:
		return false
	}
	return false
}

// expr is a node of the predicate tree.
type expr interface {
	precedence() int
}

// compoundExpr is an AND or OR node.
type compoundExpr struct {
	op    logicalOp
	left  expr
	right expr
}

func (e *compoundExpr) precedence() int {
	if e.op == logicalAnd {
		return precAnd
	}
	return precOr
}

// notExpr negates its operand.
type notExpr struct {
	operand expr
}

func (*notExpr) precedence() int { return precNot }

// constExpr is TRUEPREDICATE or FALSEPREDICATE.
type constExpr struct {
	value bool
}

func (*constExpr) precedence() int { return precPrimary }

// compareExpr compares two operands.
type compareExpr struct {
	left            operand
	op              cmpOp
	caseInsensitive bool
	right           operand
}

func (*compareExpr) precedence() int { return precPrimary }

// operand is one side of a comparison.
type operand interface {
	isOperand()
}

// propertyRef is an @property(key) reference.
type propertyRef struct {
	key string
}

func (*propertyRef) isOperand() {}

// statusRef is an @status(path) reference.
type statusRef struct {
	path string
}

func (*statusRef) isOperand() {}

// litKind identifies the type of a literal.
type litKind int

// Literal kinds.
const (
	litNull litKind = iota
	litBool
	litNumber
	litString
	litAggregate
)

// literal is a constant operand. Only the field matching kind is meaningful.
type literal struct {
	kind    litKind
	boolean bool
	number  float64
	text    string
	items   []literal
}

func (*literal) isOperand() {}
