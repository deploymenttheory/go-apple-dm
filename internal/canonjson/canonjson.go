package canonjson

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"unicode/utf16"
)

var (
	// ErrInvalidJSON reports malformed input: a syntax error, trailing data
	// after the value, invalid UTF-8, or a duplicate object member name.
	ErrInvalidJSON = errors.New("canonjson: invalid JSON")
	// ErrDepth reports nesting deeper than MaxDepth.
	ErrDepth = errors.New("canonjson: nesting too deep")
	// ErrNumber reports a number outside the finite IEEE 754 double range.
	ErrNumber = errors.New("canonjson: number not representable")
)

// MaxDepth bounds the nesting of arrays and objects. A value nested inside
// MaxDepth containers is accepted; one nested inside MaxDepth+1 is rejected
// with ErrDepth.
const MaxDepth = 128

// Canonicalize returns the JCS form of src: object members sorted by the
// UTF-16 code units of their names, no insignificant whitespace, strings
// escaped per JCS (only \" \\ \b \f \n \r \t and \u00xx for the remaining
// control characters; everything else, including U+007F and the solidus, is
// literal UTF-8), and numbers rendered per ECMAScript Number::toString
// (shortest round-trip digits, exponent form below 1e-6 and at or above
// 1e21, integers without a fraction, negative zero as 0). Duplicate names and
// invalid UTF-8 are errors.
func Canonicalize(src []byte) ([]byte, error) {
	return Append(nil, src)
}

// Append appends the canonical form of src to dst and returns the extended
// slice. On error the returned slice holds exactly the original contents of
// dst.
func Append(dst, src []byte) ([]byte, error) {
	n := len(dst)
	p := parser{dec: jsontext.NewDecoder(bytes.NewReader(src))}
	out, err := p.root(dst)
	if err != nil {
		return dst[:n], err
	}
	return out, nil
}

// Marshal encodes v with encoding/json/v2 and canonicalizes the result.
// Integers beyond 2^53 lose precision because JCS numbers are doubles.
func Marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonjson: marshal: %w", err)
	}
	return Canonicalize(b)
}

// Valid reports whether src is already in canonical form, that is, whether
// Canonicalize would return src unchanged.
func Valid(src []byte) bool {
	out, err := Canonicalize(src)
	return err == nil && bytes.Equal(out, src)
}

// parser walks the token stream of one decoder and appends canonical bytes.
type parser struct {
	dec *jsontext.Decoder
}

// root canonicalizes the single top-level value and rejects anything after it.
func (p *parser) root(dst []byte) ([]byte, error) {
	tok, err := p.dec.ReadToken()
	if err != nil {
		return nil, invalid(err)
	}
	dst, err = p.value(dst, tok, 0)
	if err != nil {
		return nil, err
	}
	if _, err := p.dec.ReadToken(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing data after value", ErrInvalidJSON)
		}
		return nil, invalid(err)
	}
	return dst, nil
}

// value appends the canonical form of the value that tok begins. depth is the
// number of containers already open around it.
func (p *parser) value(dst []byte, tok jsontext.Token, depth int) ([]byte, error) {
	switch tok.Kind() {
	case '{':
		return p.object(dst, depth+1)
	case '[':
		return p.array(dst, depth+1)
	case '"':
		return appendString(dst, tok.String()), nil
	case '0':
		return appendNumber(dst, tok.String())
	default:
		// The decoder validates structure, so only null, true, and false
		// reach this branch.
		return append(dst, tok.String()...), nil
	}
}

// array appends a canonical array whose opening bracket has been consumed.
func (p *parser) array(dst []byte, depth int) ([]byte, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%w: depth %d exceeds %d", ErrDepth, depth, MaxDepth)
	}
	dst = append(dst, '[')
	for i := 0; ; i++ {
		tok, err := p.dec.ReadToken()
		if err != nil {
			return nil, invalid(err)
		}
		if tok.Kind() == ']' {
			break
		}
		if i > 0 {
			dst = append(dst, ',')
		}
		if dst, err = p.value(dst, tok, depth); err != nil {
			return nil, err
		}
	}
	return append(dst, ']'), nil
}

// member is one object member: the name as UTF-16 code units for ordering and
// the canonical bytes of name, colon, and value ready to emit.
type member struct {
	key   []uint16
	bytes []byte
}

// object appends a canonical object whose opening brace has been consumed.
// Members are buffered, sorted by the UTF-16 code units of their names as RFC
// 8785 section 3.2.3 requires, and then emitted.
func (p *parser) object(dst []byte, depth int) ([]byte, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("%w: depth %d exceeds %d", ErrDepth, depth, MaxDepth)
	}
	var members []member
	for {
		tok, err := p.dec.ReadToken()
		if err != nil {
			return nil, invalid(err)
		}
		if tok.Kind() == '}' {
			break
		}
		name := tok.String()
		valTok, err := p.dec.ReadToken()
		if err != nil {
			return nil, invalid(err)
		}
		buf := append(appendString(nil, name), ':')
		if buf, err = p.value(buf, valTok, depth); err != nil {
			return nil, err
		}
		// The decoder has validated name as UTF-8, so the rune conversion
		// introduces no replacement characters.
		members = append(members, member{key: utf16.Encode([]rune(name)), bytes: buf})
	}
	slices.SortFunc(members, func(a, b member) int { return slices.Compare(a.key, b.key) })
	dst = append(dst, '{')
	for i, m := range members {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, m.bytes...)
	}
	return append(dst, '}'), nil
}

// hexDigits spells \u00xx escapes in lowercase as RFC 8785 requires.
const hexDigits = "0123456789abcdef"

// appendString appends s as a JCS string literal.
func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// appendNumber parses the JSON number lexeme as a double and appends it in
// ECMAScript Number::toString form (RFC 8785 section 3.2.2.3).
func appendNumber(dst []byte, lexeme string) ([]byte, error) {
	f, err := strconv.ParseFloat(lexeme, 64)
	if err != nil {
		// The decoder guarantees the lexeme is syntactically a JSON number,
		// so the only possible error is a value outside the double range.
		return nil, fmt.Errorf("%w: %s: %w", ErrNumber, lexeme, err)
	}
	abs := math.Abs(f)
	switch {
	case f == 0:
		return append(dst, '0'), nil
	case abs >= 1e-6 && abs < 1e21:
		return strconv.AppendFloat(dst, f, 'f', -1, 64), nil
	default:
		return appendExponent(dst, f), nil
	}
}

// appendExponent appends f in exponent form with the exponent written the
// ECMAScript way: an explicit sign and no leading zeros, so 1e+21 and 1e-7
// rather than Go's 1e+21 and 1e-07.
func appendExponent(dst []byte, f float64) []byte {
	n := len(dst)
	dst = strconv.AppendFloat(dst, f, 'e', -1, 64)
	e := n + bytes.LastIndexByte(dst[n:], 'e')
	// dst[e+1] is the sign and dst[e+2:] holds at least two exponent digits.
	digits := dst[e+2:]
	i := 0
	for i < len(digits)-1 && digits[i] == '0' {
		i++
	}
	return append(dst[:e+2], digits[i:]...)
}

// invalid wraps a decoder error in ErrInvalidJSON. A bare io.EOF means the
// input held no value at all; a truncated value surfaces as a syntactic error
// wrapping io.ErrUnexpectedEOF instead.
func invalid(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: empty input", ErrInvalidJSON)
	}
	return fmt.Errorf("%w: %w", ErrInvalidJSON, err)
}
