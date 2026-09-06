package predicate

import (
	"strings"
)

// maxDepth bounds nesting of parentheses, NOT and aggregates so that hostile
// input cannot exhaust the stack.
const maxDepth = 256

// unsupportedWords are NSPredicate keywords that this subset rejects when
// they appear in operand position.
var unsupportedWords = map[string]bool{
	"SELF": true, "MATCHES": true, "LIKE": true, "BETWEEN": true,
	"ANY": true, "ALL": true, "NONE": true, "SOME": true,
	"SUBQUERY": true, "FUNCTION": true, "CAST": true,
}

// reservedWords are keywords that may not be used as operands.
var reservedWords = map[string]bool{
	"AND": true, "OR": true, "NOT": true, "IN": true,
	"CONTAINS": true, "BEGINSWITH": true, "ENDSWITH": true,
	"TRUEPREDICATE": true, "FALSEPREDICATE": true,
}

// keywordOps maps word operators to their comparison.
var keywordOps = map[string]cmpOp{
	"IN":         cmpIn,
	"CONTAINS":   cmpContains,
	"BEGINSWITH": cmpBeginsWith,
	"ENDSWITH":   cmpEndsWith,
}

// symbolOps maps symbolic operators, including alternate spellings.
var symbolOps = map[string]cmpOp{
	"==": cmpEq,
	"=":  cmpEq,
	"!=": cmpNe,
	"<>": cmpNe,
	"<":  cmpLt,
	"<=": cmpLe,
	">":  cmpGt,
	">=": cmpGe,
}

// parser is a recursive-descent parser with one token of lookahead.
type parser struct {
	lx    *lexer
	tok   token
	depth int
}

func parse(src string) (expr, error) {
	p := &parser{lx: newLexer(src)}
	if err := p.advance(); err != nil {
		return nil, err
	}
	if p.tok.kind == tokEOF {
		return nil, syntaxErr(p.tok.pos, "empty predicate")
	}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.tok.kind != tokEOF {
		return nil, syntaxErr(p.tok.pos, "unexpected %s", p.tok.describe())
	}
	return e, nil
}

func (p *parser) advance() error {
	tok, err := p.lx.next()
	if err != nil {
		return err
	}
	p.tok = tok
	return nil
}

// keyword returns the upper-cased identifier text, or "" for other tokens.
func (p *parser) keyword() string {
	if p.tok.kind != tokIdent {
		return ""
	}
	return strings.ToUpper(p.tok.text)
}

func (p *parser) isOp(text string) bool {
	return p.tok.kind == tokOp && p.tok.text == text
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return syntaxErr(p.tok.pos, "nesting deeper than %d levels", maxDepth)
	}
	return nil
}

func (p *parser) leave() {
	p.depth--
}

func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.keyword() == "OR" || p.isOp("||") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &compoundExpr{op: logicalOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.keyword() == "AND" || p.isOp("&&") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &compoundExpr{op: logicalAnd, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (expr, error) {
	if p.keyword() != "NOT" && !p.isOp("!") {
		return p.parsePrimary()
	}
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	if err := p.advance(); err != nil {
		return nil, err
	}
	operand, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	return &notExpr{operand: operand}, nil
}

func (p *parser) parsePrimary() (expr, error) {
	switch {
	case p.tok.kind == tokLParen:
		return p.parseParenthesised()
	case p.keyword() == "TRUEPREDICATE":
		return p.parseConst(true)
	case p.keyword() == "FALSEPREDICATE":
		return p.parseConst(false)
	case p.tok.kind == tokEOF:
		return nil, syntaxErr(p.tok.pos, "unexpected end of input, expected a predicate")
	}
	return p.parseComparison()
}

func (p *parser) parseConst(value bool) (expr, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	return &constExpr{value: value}, nil
}

func (p *parser) parseParenthesised() (expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	if err := p.advance(); err != nil {
		return nil, err
	}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.tok.kind != tokRParen {
		return nil, syntaxErr(p.tok.pos, "expected \")\", got %s", p.tok.describe())
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	return e, nil
}

func (p *parser) parseComparison() (expr, error) {
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	modifierPos := p.tok.pos
	ci, err := p.parseModifier()
	if err != nil {
		return nil, err
	}
	op, err := p.parseOp()
	if err != nil {
		return nil, err
	}
	if p.tok.kind == tokModifier {
		if ci {
			return nil, syntaxErr(p.tok.pos, "duplicate modifier [%s]", p.tok.text)
		}
		modifierPos = p.tok.pos
		if ci, err = p.parseModifier(); err != nil {
			return nil, err
		}
	}
	if ci && !op.allowsCaseModifier() {
		return nil, syntaxErr(modifierPos, "modifier [c] is not allowed on %s", op)
	}
	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	return &compareExpr{left: left, op: op, caseInsensitive: ci, right: right}, nil
}

// parseModifier consumes a bracketed modifier when present and reports
// whether it requested case-insensitive comparison.
func (p *parser) parseModifier() (bool, error) {
	if p.tok.kind != tokModifier {
		return false, nil
	}
	if !strings.EqualFold(p.tok.text, "c") {
		return false, unsupportedErr(p.tok.pos, "modifier [%s]", p.tok.text)
	}
	if err := p.advance(); err != nil {
		return false, err
	}
	return true, nil
}

func (p *parser) parseOp() (cmpOp, error) {
	var (
		op cmpOp
		ok bool
	)
	switch p.tok.kind {
	case tokOp:
		op, ok = symbolOps[p.tok.text]
	case tokIdent:
		word := p.keyword()
		if word == "MATCHES" || word == "LIKE" || word == "BETWEEN" {
			return 0, unsupportedErr(p.tok.pos, "operator %s", word)
		}
		op, ok = keywordOps[word]
	case tokEOF, tokLParen, tokRParen, tokLBrace, tokRBrace, tokComma,
		tokString, tokNumber, tokModifier, tokProperty, tokStatus:
		ok = false
	}
	if !ok {
		return 0, syntaxErr(p.tok.pos, "expected a comparison operator, got %s", p.tok.describe())
	}
	if err := p.advance(); err != nil {
		return 0, err
	}
	return op, nil
}

func (p *parser) parseOperand() (operand, error) {
	switch p.tok.kind {
	case tokProperty:
		ref := &propertyRef{key: p.tok.text}
		return ref, p.advance()
	case tokStatus:
		ref := &statusRef{path: p.tok.text}
		return ref, p.advance()
	case tokEOF, tokLParen, tokRParen, tokLBrace, tokRBrace, tokComma,
		tokString, tokNumber, tokIdent, tokOp, tokModifier:
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		return lit, nil
	}
	return nil, syntaxErr(p.tok.pos, "unexpected %s", p.tok.describe())
}

func (p *parser) parseLiteral() (*literal, error) {
	switch p.tok.kind {
	case tokString:
		lit := &literal{kind: litString, text: p.tok.text}
		return lit, p.advance()
	case tokNumber:
		lit := &literal{kind: litNumber, number: p.tok.number}
		return lit, p.advance()
	case tokLBrace:
		return p.parseAggregate()
	case tokIdent:
		return p.parseWordLiteral()
	case tokEOF:
		return nil, syntaxErr(p.tok.pos, "unexpected end of input, expected an operand")
	case tokLParen, tokRParen, tokRBrace, tokComma, tokOp, tokModifier, tokProperty, tokStatus:
		return nil, syntaxErr(p.tok.pos, "expected an operand, got %s", p.tok.describe())
	}
	return nil, syntaxErr(p.tok.pos, "expected an operand, got %s", p.tok.describe())
}

// parseWordLiteral handles identifiers in operand position: the boolean and
// null keywords are literals, everything else is unsupported.
func (p *parser) parseWordLiteral() (*literal, error) {
	word := p.keyword()
	var lit *literal
	switch word {
	case "TRUE", "YES":
		lit = &literal{kind: litBool, boolean: true}
	case "FALSE", "NO":
		lit = &literal{kind: litBool, boolean: false}
	case "NULL", "NIL":
		lit = &literal{kind: litNull}
	}
	if lit != nil {
		return lit, p.advance()
	}
	return nil, p.unsupportedWord(word)
}

func (p *parser) unsupportedWord(word string) error {
	pos := p.tok.pos
	// A key path such as SELF.name is reported by its head segment.
	head, _, _ := strings.Cut(word, ".")
	switch {
	case p.tok.callFollows:
		return unsupportedErr(pos, "function call %s(...)", p.tok.text)
	case unsupportedWords[head]:
		return unsupportedErr(pos, "%s", head)
	case reservedWords[word]:
		return syntaxErr(pos, "unexpected keyword %s", word)
	}
	return unsupportedErr(pos, "bare key path %q, use @property(%s) or @status(%s)", p.tok.text, p.tok.text, p.tok.text)
}

func (p *parser) parseAggregate() (*literal, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	if err := p.advance(); err != nil {
		return nil, err
	}
	items := []literal{}
	for p.tok.kind != tokRBrace {
		if len(items) > 0 {
			if p.tok.kind != tokComma {
				return nil, syntaxErr(p.tok.pos, "expected \",\" or \"}\" in aggregate, got %s", p.tok.describe())
			}
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		item, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	return &literal{kind: litAggregate, items: items}, nil
}
