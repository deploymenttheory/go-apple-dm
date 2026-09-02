package predicate

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// tokKind classifies a lexical token.
type tokKind int

// Token kinds.
const (
	tokEOF tokKind = iota
	tokLParen
	tokRParen
	tokLBrace
	tokRBrace
	tokComma
	tokString
	tokNumber
	tokIdent
	tokOp
	tokModifier
	tokProperty
	tokStatus
)

// token is a lexical token with its byte offset in the source.
type token struct {
	kind   tokKind
	text   string
	number float64
	pos    int
	// callFollows is set on an identifier whose next significant character
	// is "(", which marks a function call.
	callFollows bool
}

// describe returns a short human-readable description for error messages.
func (t token) describe() string {
	switch t.kind {
	case tokEOF:
		return "end of input"
	case tokLParen:
		return `"("`
	case tokRParen:
		return `")"`
	case tokLBrace:
		return `"{"`
	case tokRBrace:
		return `"}"`
	case tokComma:
		return `","`
	case tokString:
		return "string " + strconv.Quote(t.text)
	case tokNumber:
		return "number " + t.text
	case tokIdent:
		return "identifier " + t.text
	case tokOp:
		return `operator "` + t.text + `"`
	case tokModifier:
		return "modifier [" + t.text + "]"
	case tokProperty:
		return "@property(" + t.text + ")"
	case tokStatus:
		return "@status(" + t.text + ")"
	}
	return "token"
}

// operatorWords are keywords after which a sign character starts a number
// rather than an arithmetic operator.
var operatorWords = map[string]bool{
	"AND": true, "OR": true, "NOT": true, "IN": true,
	"CONTAINS": true, "BEGINSWITH": true, "ENDSWITH": true,
	"MATCHES": true, "LIKE": true, "BETWEEN": true,
	"ANY": true, "ALL": true, "NONE": true, "SOME": true,
}

// lexer produces tokens on demand from a predicate source string.
type lexer struct {
	src string
	pos int
	// prevOperand records whether the previous token ended an operand, which
	// decides whether "+" and "-" begin a signed number.
	prevOperand bool
}

func newLexer(src string) *lexer {
	return &lexer{src: src}
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isKeyChar reports whether c may appear in an identifier or an unquoted key.
func isKeyChar(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '.' || c == '-'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func (l *lexer) byteAt(i int) byte {
	if i < len(l.src) {
		return l.src[i]
	}
	return 0
}

func (l *lexer) digitAt(i int) bool {
	return isDigit(l.byteAt(i))
}

func (l *lexer) skipSpace() {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
}

// consume advances past c when it is the next byte.
func (l *lexer) consume(c byte) bool {
	if l.byteAt(l.pos) == c {
		l.pos++
		return true
	}
	return false
}

// readKeyChars returns the run of key characters at the current position.
func (l *lexer) readKeyChars() string {
	start := l.pos
	for l.pos < len(l.src) && isKeyChar(l.src[l.pos]) {
		l.pos++
	}
	return l.src[start:l.pos]
}

func (l *lexer) readDigits() int {
	start := l.pos
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.pos++
	}
	return l.pos - start
}

// signStartsNumber reports whether a "+" or "-" at the current position
// begins a signed number literal.
func (l *lexer) signStartsNumber() bool {
	if l.prevOperand {
		return false
	}
	return l.digitAt(l.pos+1) || (l.byteAt(l.pos+1) == '.' && l.digitAt(l.pos+2))
}

// next returns the next token. Errors are always *SyntaxError.
func (l *lexer) next() (token, error) {
	l.skipSpace()
	if l.pos >= len(l.src) {
		l.prevOperand = false
		return token{kind: tokEOF, pos: l.pos}, nil
	}
	start := l.pos
	c := l.src[l.pos]
	var (
		tok token
		err error
	)
	switch {
	case c == '\'' || c == '"':
		tok, err = l.scanString()
	case c == '@':
		tok, err = l.scanReference()
	case c == '[':
		tok, err = l.scanModifier()
	case c == '%':
		err = l.unsupportedFormatArgument()
	case c == '$':
		err = l.unsupportedVariable()
	case isDigit(c) || (c == '.' && l.digitAt(l.pos+1)):
		tok, err = l.scanNumber()
	case (c == '+' || c == '-') && l.signStartsNumber():
		tok, err = l.scanNumber()
	case isIdentStart(c):
		tok = l.scanIdent()
	default:
		tok, err = l.scanPunct()
	}
	if err != nil {
		return token{}, err
	}
	tok.pos = start
	l.prevOperand = endsOperand(tok)
	return tok, nil
}

// endsOperand reports whether tok can be the last token of an operand.
func endsOperand(tok token) bool {
	switch tok.kind {
	case tokString, tokNumber, tokProperty, tokStatus, tokRParen, tokRBrace:
		return true
	case tokIdent:
		return !operatorWords[strings.ToUpper(tok.text)]
	case tokEOF, tokLParen, tokLBrace, tokComma, tokOp, tokModifier:
		return false
	}
	return false
}

func (l *lexer) scanPunct() (token, error) {
	c := l.src[l.pos]
	switch c {
	case '(':
		l.pos++
		return token{kind: tokLParen, text: "("}, nil
	case ')':
		l.pos++
		return token{kind: tokRParen, text: ")"}, nil
	case '{':
		l.pos++
		return token{kind: tokLBrace, text: "{"}, nil
	case '}':
		l.pos++
		return token{kind: tokRBrace, text: "}"}, nil
	case ',':
		l.pos++
		return token{kind: tokComma, text: ","}, nil
	case '=', '!', '<', '>', '&', '|':
		return l.scanOperator()
	case '+', '-', '*', '/':
		return token{}, l.unsupportedArithmetic()
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
	return token{}, syntaxErr(l.pos, "unexpected character %q", r)
}

func (l *lexer) scanOperator() (token, error) {
	if l.pos+2 <= len(l.src) {
		switch two := l.src[l.pos : l.pos+2]; two {
		case "==", "!=", "<>", "<=", ">=", "&&", "||":
			l.pos += 2
			return token{kind: tokOp, text: two}, nil
		}
	}
	c := l.src[l.pos]
	switch c {
	case '=', '!', '<', '>':
		l.pos++
		return token{kind: tokOp, text: string(c)}, nil
	}
	return token{}, syntaxErr(l.pos, "unexpected character %q, expected %q", c, string([]byte{c, c}))
}

func (l *lexer) unsupportedArithmetic() error {
	start := l.pos
	op := l.src[l.pos : l.pos+1]
	if l.byteAt(l.pos+1) == '*' {
		op = "**"
	}
	l.pos += len(op)
	return unsupportedErr(start, "arithmetic operator %s", op)
}

func (l *lexer) unsupportedFormatArgument() error {
	start := l.pos
	l.pos++
	name := "%"
	if c := l.byteAt(l.pos); c != 0 && !isSpace(c) {
		name += string(c)
		l.pos++
	}
	return unsupportedErr(start, "format argument %s", name)
}

func (l *lexer) unsupportedVariable() error {
	start := l.pos
	l.pos++
	name := "$" + l.readKeyChars()
	return unsupportedErr(start, "variable %s", name)
}

// scanIdent reads an identifier or keyword and records whether a "("
// follows it.
func (l *lexer) scanIdent() token {
	text := l.readKeyChars()
	i := l.pos
	for i < len(l.src) && isSpace(l.src[i]) {
		i++
	}
	return token{kind: tokIdent, text: text, callFollows: l.byteAt(i) == '('}
}

func (l *lexer) scanNumber() (token, error) {
	start := l.pos
	if c := l.src[l.pos]; c == '+' || c == '-' {
		l.pos++
	}
	l.readDigits()
	if l.consume('.') {
		l.readDigits()
	}
	if c := l.byteAt(l.pos); c == 'e' || c == 'E' {
		l.pos++
		if c := l.byteAt(l.pos); c == '+' || c == '-' {
			l.pos++
		}
		if l.readDigits() == 0 {
			return token{}, syntaxErr(start, "invalid number %q", l.src[start:l.pos])
		}
	}
	text := l.src[start:l.pos]
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return token{}, syntaxErr(start, "invalid number %q", text)
	}
	return token{kind: tokNumber, text: text, number: v}, nil
}

// scanModifier reads a bracketed comparison modifier such as [c].
func (l *lexer) scanModifier() (token, error) {
	start := l.pos
	end := strings.IndexByte(l.src[l.pos:], ']')
	if end < 0 {
		return token{}, syntaxErr(start, "unterminated modifier")
	}
	text := strings.TrimSpace(l.src[l.pos+1 : l.pos+end])
	l.pos += end + 1
	return token{kind: tokModifier, text: text}, nil
}

// scanReference reads @property(key) or @status(path) as a single token.
func (l *lexer) scanReference() (token, error) {
	start := l.pos
	l.pos++
	name := l.readKeyChars()
	var kind tokKind
	switch strings.ToLower(name) {
	case "property":
		kind = tokProperty
	case "status":
		kind = tokStatus
	default:
		return token{}, syntaxErr(start, "unknown reference @%s, expected @property or @status", name)
	}
	l.skipSpace()
	if !l.consume('(') {
		return token{}, syntaxErr(l.pos, "expected \"(\" after @%s", name)
	}
	l.skipSpace()
	key, err := l.scanKey(name)
	if err != nil {
		return token{}, err
	}
	l.skipSpace()
	if !l.consume(')') {
		return token{}, syntaxErr(l.pos, "expected \")\" to close @%s(", name)
	}
	return token{kind: kind, text: key}, nil
}

// scanKey reads the argument of a reference, either quoted or bare.
func (l *lexer) scanKey(ref string) (string, error) {
	if c := l.byteAt(l.pos); c == '\'' || c == '"' {
		tok, err := l.scanString()
		if err != nil {
			return "", err
		}
		return tok.text, nil
	}
	key := l.readKeyChars()
	if key == "" {
		return "", syntaxErr(l.pos, "expected a key inside @%s(...)", ref)
	}
	return key, nil
}

// scanString reads a single- or double-quoted string and decodes escapes.
func (l *lexer) scanString() (token, error) {
	start := l.pos
	quote := l.src[l.pos]
	l.pos++
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case quote:
			l.pos++
			return token{kind: tokString, text: sb.String()}, nil
		case '\\':
			if err := l.scanEscape(&sb); err != nil {
				return token{}, err
			}
		default:
			sb.WriteByte(c)
			l.pos++
		}
	}
	return token{}, syntaxErr(start, "unterminated string")
}

func (l *lexer) scanEscape(sb *strings.Builder) error {
	start := l.pos
	l.pos++
	if l.pos >= len(l.src) {
		return syntaxErr(start, "unterminated escape sequence")
	}
	c := l.src[l.pos]
	l.pos++
	switch c {
	case '\\', '\'', '"':
		sb.WriteByte(c)
	case 'n':
		sb.WriteByte('\n')
	case 'r':
		sb.WriteByte('\r')
	case 't':
		sb.WriteByte('\t')
	case 'u':
		return l.scanUnicodeEscape(sb, start)
	default:
		return syntaxErr(start, "invalid escape sequence \\%c", c)
	}
	return nil
}

// hex4 decodes four hexadecimal digits at the current position.
func (l *lexer) hex4() (rune, bool) {
	if l.pos+4 > len(l.src) {
		return 0, false
	}
	v, err := strconv.ParseUint(l.src[l.pos:l.pos+4], 16, 32)
	if err != nil {
		return 0, false
	}
	l.pos += 4
	return rune(v), true
}

func (l *lexer) scanUnicodeEscape(sb *strings.Builder, start int) error {
	r, ok := l.hex4()
	if !ok {
		return syntaxErr(start, "invalid \\u escape, expected four hexadecimal digits")
	}
	if utf16.IsSurrogate(r) && strings.HasPrefix(l.src[l.pos:], "\\u") {
		save := l.pos
		l.pos += 2
		if low, ok := l.hex4(); ok {
			if pair := utf16.DecodeRune(r, low); pair != utf8.RuneError {
				sb.WriteRune(pair)
				return nil
			}
		}
		l.pos = save
	}
	// A lone surrogate is written as the replacement character.
	sb.WriteRune(r)
	return nil
}
