package cbor

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// Marshal encodes v into the supported subset. Map and struct keys are
// written in the deterministic order of RFC 8949 section 4.2.1: by the bytes
// of the encoded key, which for text keys sorts shorter before longer and
// then bytewise. A RawMessage is copied verbatim after a well-formedness
// check, so a value that came from Unmarshal round-trips unchanged.
func Marshal(v any) ([]byte, error) {
	var e encoder
	if err := e.value(reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	return e.buf, nil
}

type encoder struct {
	buf []byte
}

// head writes an item header in the shortest form that holds arg, which is
// what makes the encoding deterministic.
//
// #nosec G115 -- every conversion is bounded by the case that selects it.
func (e *encoder) head(major byte, arg uint64) {
	switch {
	case arg < 24:
		e.buf = append(e.buf, major<<5|byte(arg))
	case arg <= math.MaxUint8:
		e.buf = append(e.buf, major<<5|24, byte(arg))
	case arg <= math.MaxUint16:
		e.buf = binary.BigEndian.AppendUint16(append(e.buf, major<<5|25), uint16(arg))
	case arg <= math.MaxUint32:
		e.buf = binary.BigEndian.AppendUint32(append(e.buf, major<<5|26), uint32(arg))
	default:
		e.buf = binary.BigEndian.AppendUint64(append(e.buf, major<<5|27), arg)
	}
}

func (e *encoder) value(rv reflect.Value, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: over %d levels", ErrDepth, MaxDepth)
	}
	if !rv.IsValid() {
		e.head(majSimple, simpleNull)
		return nil
	}
	if rv.Type() == rawMessageType {
		raw := rv.Bytes()
		if err := Wellformed(raw); err != nil {
			return fmt.Errorf("cbor: raw message: %w", err)
		}
		e.buf = append(e.buf, raw...)
		return nil
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			e.head(majSimple, simpleNull)
			return nil
		}
		return e.value(rv.Elem(), depth)
	case reflect.Bool:
		if rv.Bool() {
			e.head(majSimple, simpleTrue)
		} else {
			e.head(majSimple, simpleFalse)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.head(majUint, rv.Uint())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n := rv.Int(); n < 0 {
			e.head(majNegInt, uint64(-1-n))
		} else {
			e.head(majUint, uint64(n))
		}
	case reflect.String:
		s := rv.String()
		e.head(majText, uint64(len(s))) //#nosec G115 -- a length is never negative
		e.buf = append(e.buf, s...)
	case reflect.Slice, reflect.Array:
		return e.sequence(rv, depth)
	case reflect.Map:
		return e.mapping(rv, depth)
	case reflect.Struct:
		return e.structure(rv, depth)
	default:
		return fmt.Errorf("%w: cannot encode %s", ErrType, rv.Type())
	}
	return nil
}

func (e *encoder) sequence(rv reflect.Value, depth int) error {
	if rv.Type().Elem().Kind() == reflect.Uint8 && rv.Kind() == reflect.Slice {
		b := rv.Bytes()
		e.head(majBytes, uint64(len(b))) //#nosec G115 -- a length is never negative
		e.buf = append(e.buf, b...)
		return nil
	}
	e.head(majArray, uint64(rv.Len())) //#nosec G115 -- a length is never negative
	for i := range rv.Len() {
		if err := e.value(rv.Index(i), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// pair is one encoded map entry, kept whole so entries can be sorted by
// their encoded key before they are written.
type pair struct {
	key   []byte
	value []byte
}

func (e *encoder) writePairs(pairs []pair) {
	slices.SortFunc(pairs, func(a, b pair) int { return compareKeys(a.key, b.key) })
	e.head(majMap, uint64(len(pairs))) //#nosec G115 -- a length is never negative
	for _, p := range pairs {
		e.buf = append(e.buf, p.key...)
		e.buf = append(e.buf, p.value...)
	}
}

// compareKeys orders encoded keys as RFC 8949 section 4.2.1 requires:
// shorter first, then bytewise. Comparing the encoded bytes directly gives
// the same order for text keys, since a shorter string has a smaller head.
func compareKeys(a, b []byte) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return slices.Compare(a, b)
}

func (e *encoder) mapping(rv reflect.Value, depth int) error {
	if rv.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("%w: map with %s keys", ErrType, rv.Type().Key())
	}
	pairs := make([]pair, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		p, err := e.pairFor(iter.Key().String(), iter.Value(), depth)
		if err != nil {
			return err
		}
		pairs = append(pairs, p)
	}
	e.writePairs(pairs)
	return nil
}

func (e *encoder) structure(rv reflect.Value, depth int) error {
	t := rv.Type()
	pairs := make([]pair, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("cbor"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if strings.Contains(opts, "omitempty") && rv.Field(i).IsZero() {
			continue
		}
		p, err := e.pairFor(name, rv.Field(i), depth)
		if err != nil {
			return fmt.Errorf("%q: %w", name, err)
		}
		pairs = append(pairs, p)
	}
	e.writePairs(pairs)
	return nil
}

// pairFor encodes one key and its value into separate buffers.
func (e *encoder) pairFor(name string, v reflect.Value, depth int) (pair, error) {
	var ke encoder
	ke.head(majText, uint64(len(name))) //#nosec G115 -- a length is never negative
	ke.buf = append(ke.buf, name...)
	var ve encoder
	if err := ve.value(v, depth+1); err != nil {
		return pair{}, err
	}
	return pair{key: ke.buf, value: ve.buf}, nil
}
