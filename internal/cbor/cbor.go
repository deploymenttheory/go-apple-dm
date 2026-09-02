package cbor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
)

// Limits applied to every decode.
const (
	// MaxBytes is the largest input Unmarshal and Wellformed accept.
	MaxBytes = 1 << 20
	// MaxDepth is the deepest nesting of arrays and maps accepted.
	MaxDepth = 16
)

// Decoding and encoding errors.
var (
	ErrTooLarge    = errors.New("cbor: input too large")
	ErrSyntax      = errors.New("cbor: malformed input")
	ErrUnsupported = errors.New("cbor: item outside the supported subset")
	ErrTrailing    = errors.New("cbor: trailing data")
	ErrType        = errors.New("cbor: cannot decode into target")
	ErrDuplicate   = errors.New("cbor: duplicate map key")
	ErrDepth       = errors.New("cbor: nesting too deep")
	ErrTarget      = errors.New("cbor: target must be a non-nil pointer")
)

// RawMessage is an encoded item kept verbatim. Decoding into one copies the
// bytes of exactly one item, which lets a caller defer interpretation (an
// attestation statement is read only once its format name is known) and lets
// a server persist what it received byte for byte.
type RawMessage []byte

// Major types. Tags (6) are rejected; simple values (7) are accepted only
// for false, true, and null.
const (
	majUint   = 0
	majNegInt = 1
	majBytes  = 2
	majText   = 3
	majArray  = 4
	majMap    = 5
	majTag    = 6
	majSimple = 7
)

// Simple values inside major type 7.
const (
	simpleFalse = 20
	simpleTrue  = 21
	simpleNull  = 22
)

// Unmarshal decodes exactly one item from data into v, which must be a
// non-nil pointer. Input larger than MaxBytes, anything outside the subset
// described on the package, and any bytes after the item are errors.
func Unmarshal(data []byte, v any) error {
	if len(data) > MaxBytes {
		return fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: %T", ErrTarget, v)
	}
	d := &decoder{buf: data}
	if err := d.value(rv.Elem(), 0); err != nil {
		return err
	}
	if d.off != len(data) {
		return fmt.Errorf("%w: %d bytes after the item", ErrTrailing, len(data)-d.off)
	}
	return nil
}

// Wellformed reports whether data is exactly one item of the supported
// subset with nothing after it. It walks the whole input without decoding
// into a target, so a caller can reject a hostile body before spending
// anything on it.
func Wellformed(data []byte) error {
	if len(data) > MaxBytes {
		return fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	d := &decoder{buf: data}
	if err := d.skip(0); err != nil {
		return err
	}
	if d.off != len(data) {
		return fmt.Errorf("%w: %d bytes after the item", ErrTrailing, len(data)-d.off)
	}
	return nil
}

type decoder struct {
	buf []byte
	off int
}

// head reads one item header and returns its major type and argument. It is
// the only place the subset is enforced for headers: indefinite lengths,
// reserved additional information, tags, floats, and simple values other
// than false, true, and null never reach a caller.
func (d *decoder) head() (major byte, arg uint64, err error) {
	if d.off >= len(d.buf) {
		return 0, 0, fmt.Errorf("%w: truncated header at %d", ErrSyntax, d.off)
	}
	b := d.buf[d.off]
	d.off++
	major = b >> 5
	ai := b & 0x1f
	switch {
	case ai < 24:
		arg = uint64(ai)
	case ai == 24:
		arg, err = d.readArg(1)
	case ai == 25:
		arg, err = d.readArg(2)
	case ai == 26:
		arg, err = d.readArg(4)
	case ai == 27:
		arg, err = d.readArg(8)
	case ai == 31:
		return 0, 0, fmt.Errorf("%w: indefinite length at %d", ErrUnsupported, d.off-1)
	default:
		return 0, 0, fmt.Errorf("%w: reserved additional information %d", ErrSyntax, ai)
	}
	if err != nil {
		return 0, 0, err
	}
	switch {
	case major == majTag:
		return 0, 0, fmt.Errorf("%w: tag %d", ErrUnsupported, arg)
	case major == majSimple && ai >= 24:
		// Additional information 24 is an 8-bit simple value; 25, 26, and
		// 27 are half, single, and double precision floats.
		return 0, 0, fmt.Errorf("%w: float or extended simple value", ErrUnsupported)
	case major == majSimple && arg != simpleFalse && arg != simpleTrue && arg != simpleNull:
		return 0, 0, fmt.Errorf("%w: simple value %d", ErrUnsupported, arg)
	}
	return major, arg, nil
}

func (d *decoder) readArg(n int) (uint64, error) {
	if d.off+n > len(d.buf) {
		return 0, fmt.Errorf("%w: truncated argument at %d", ErrSyntax, d.off)
	}
	var v uint64
	switch n {
	case 1:
		v = uint64(d.buf[d.off])
	case 2:
		v = uint64(binary.BigEndian.Uint16(d.buf[d.off:]))
	case 4:
		v = uint64(binary.BigEndian.Uint32(d.buf[d.off:]))
	default:
		v = binary.BigEndian.Uint64(d.buf[d.off:])
	}
	d.off += n
	return v, nil
}

// fits turns a length argument into an int, rejecting one that the bytes
// still unread could not possibly satisfy: a header claiming four billion
// elements costs a comparison rather than an allocation. Checking and
// converting in the same place is what makes every later use of the result
// safe, so this is the only conversion of an attacker-supplied length.
//
// perItem is the smallest number of bytes one element can occupy: one for a
// string byte or an array element, two for a map entry and its key.
//
// #nosec G115 -- the remaining length is never negative, and n is compared
// against it before it is converted.
func (d *decoder) fits(n uint64, perItem int) (int, error) {
	remaining := len(d.buf) - d.off
	if n > uint64(remaining/perItem) {
		return 0, fmt.Errorf(
			"%w: %d items do not fit in the %d bytes left at %d",
			ErrSyntax, n, remaining, d.off,
		)
	}
	return int(n), nil
}

// bytes reads the payload of a string item of length n.
func (d *decoder) bytes(n uint64) ([]byte, error) {
	size, err := d.fits(n, 1)
	if err != nil {
		return nil, err
	}
	b := d.buf[d.off : d.off+size]
	d.off += size
	return b, nil
}

// skip advances past exactly one item.
func (d *decoder) skip(depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: over %d levels", ErrDepth, MaxDepth)
	}
	major, arg, err := d.head()
	if err != nil {
		return err
	}
	switch major {
	case majBytes, majText:
		_, err = d.bytes(arg)
		return err
	case majArray:
		n, err := d.fits(arg, 1)
		if err != nil {
			return err
		}
		for range n {
			if err := d.skip(depth + 1); err != nil {
				return err
			}
		}
	case majMap:
		n, err := d.fits(arg, 2)
		if err != nil {
			return err
		}
		for range n {
			// Keys are checked here too, so Wellformed and Unmarshal
			// accept exactly the same inputs.
			if _, err := d.key(); err != nil {
				return err
			}
			if err := d.skip(depth + 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// value decodes one item into rv.
func (d *decoder) value(rv reflect.Value, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: over %d levels", ErrDepth, MaxDepth)
	}
	if rv.Type() == rawMessageType {
		start := d.off
		if err := d.skip(depth); err != nil {
			return err
		}
		rv.SetBytes(RawMessage(append([]byte(nil), d.buf[start:d.off]...)))
		return nil
	}
	start := d.off
	major, arg, err := d.head()
	if err != nil {
		return err
	}
	if major == majSimple && arg == simpleNull {
		rv.SetZero()
		return nil
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		d.off = start
		return d.value(rv.Elem(), depth)
	}
	switch major {
	case majUint:
		return setUint(rv, arg)
	case majNegInt:
		if arg > math.MaxInt64 {
			return fmt.Errorf("%w: negative integer out of range", ErrSyntax)
		}
		return setInt(rv, -1-int64(arg))
	case majBytes:
		b, err := d.bytes(arg)
		if err != nil {
			return err
		}
		return setBytes(rv, b)
	case majText:
		b, err := d.bytes(arg)
		if err != nil {
			return err
		}
		return setText(rv, b)
	case majArray:
		return d.array(rv, arg, depth)
	case majMap:
		return d.mapping(rv, arg, depth)
	default: // majSimple: false or true, null already handled.
		if rv.Kind() != reflect.Bool {
			return fmt.Errorf("%w: boolean into %s", ErrType, rv.Type())
		}
		rv.SetBool(arg == simpleTrue)
		return nil
	}
}

func (d *decoder) array(rv reflect.Value, arg uint64, depth int) error {
	if rv.Kind() != reflect.Slice {
		return fmt.Errorf("%w: array into %s", ErrType, rv.Type())
	}
	n, err := d.fits(arg, 1)
	if err != nil {
		return err
	}
	out := reflect.MakeSlice(rv.Type(), n, n)
	for i := range n {
		if err := d.value(out.Index(i), depth+1); err != nil {
			return err
		}
	}
	rv.Set(out)
	return nil
}

func (d *decoder) mapping(rv reflect.Value, arg uint64, depth int) error {
	n, err := d.fits(arg, 2)
	if err != nil {
		return err
	}
	switch rv.Kind() {
	case reflect.Struct:
		return d.structure(rv, n, depth)
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("%w: map with %s keys", ErrType, rv.Type().Key())
		}
		out := reflect.MakeMapWithSize(rv.Type(), n)
		for range n {
			key, err := d.textKey()
			if err != nil {
				return err
			}
			if out.MapIndex(reflect.ValueOf(key).Convert(rv.Type().Key())).IsValid() {
				return fmt.Errorf("%w: %q", ErrDuplicate, key)
			}
			elem := reflect.New(rv.Type().Elem()).Elem()
			if err := d.value(elem, depth+1); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(key).Convert(rv.Type().Key()), elem)
		}
		rv.Set(out)
		return nil
	default:
		return fmt.Errorf("%w: map into %s", ErrType, rv.Type())
	}
}

func (d *decoder) structure(rv reflect.Value, n int, depth int) error {
	fields := fieldsOf(rv.Type())
	seen := make(map[string]bool, n)
	for range n {
		key, err := d.textKey()
		if err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("%w: %q", ErrDuplicate, key)
		}
		seen[key] = true
		index, ok := fields[key]
		if !ok {
			// An unknown member is skipped, not rejected: Apple may add
			// one and a device that sends it must still enroll.
			if err := d.skip(depth + 1); err != nil {
				return err
			}
			continue
		}
		if err := d.value(rv.Field(index), depth+1); err != nil {
			return fmt.Errorf("%q: %w", key, err)
		}
	}
	return nil
}

// key reads one map key, which must be a text string, and returns its
// bytes.
func (d *decoder) key() ([]byte, error) {
	major, arg, err := d.head()
	if err != nil {
		return nil, err
	}
	if major != majText {
		return nil, fmt.Errorf("%w: map key of major type %d", ErrUnsupported, major)
	}
	return d.bytes(arg)
}

// textKey reads one map key as a string.
func (d *decoder) textKey() (string, error) {
	b, err := d.key()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func setUint(rv reflect.Value, v uint64) error {
	switch rv.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if rv.OverflowUint(v) {
			return fmt.Errorf("%w: %d overflows %s", ErrType, v, rv.Type())
		}
		rv.SetUint(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v > math.MaxInt64 || rv.OverflowInt(int64(v)) {
			return fmt.Errorf("%w: %d overflows %s", ErrType, v, rv.Type())
		}
		rv.SetInt(int64(v))
	default:
		return fmt.Errorf("%w: integer into %s", ErrType, rv.Type())
	}
	return nil
}

func setInt(rv reflect.Value, v int64) error {
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if rv.OverflowInt(v) {
			return fmt.Errorf("%w: %d overflows %s", ErrType, v, rv.Type())
		}
		rv.SetInt(v)
	default:
		return fmt.Errorf("%w: negative integer into %s", ErrType, rv.Type())
	}
	return nil
}

func setBytes(rv reflect.Value, b []byte) error {
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() != reflect.Uint8 {
		return fmt.Errorf("%w: byte string into %s", ErrType, rv.Type())
	}
	rv.SetBytes(append([]byte(nil), b...))
	return nil
}

func setText(rv reflect.Value, b []byte) error {
	if rv.Kind() != reflect.String {
		return fmt.Errorf("%w: text string into %s", ErrType, rv.Type())
	}
	rv.SetString(string(b))
	return nil
}

var (
	rawMessageType = reflect.TypeOf(RawMessage(nil))
	fieldCache     sync.Map // reflect.Type -> map[string]int
)

// fieldsOf maps the encoded name of every exported field to its index. A
// field is named by its cbor tag, or by its Go name when there is none; a
// tag of "-" omits it.
func fieldsOf(t reflect.Type) map[string]int {
	if cached, ok := fieldCache.Load(t); ok {
		return cached.(map[string]int)
	}
	fields := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("cbor"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = i
	}
	fieldCache.Store(t, fields)
	return fields
}
