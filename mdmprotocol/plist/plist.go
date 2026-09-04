package plist

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	mplist "github.com/micromdm/plist"
)

// Format is the detected plist serialisation.
type Format int

// Formats.
const (
	FormatUnknown Format = iota
	FormatXML
	FormatBinary
)

// String implements fmt.Stringer.
func (f Format) String() string {
	switch f {
	case FormatXML:
		return "xml"
	case FormatBinary:
		return "binary"
	case FormatUnknown:
	}
	return "unknown"
}

// Defaults applied by Unmarshal. Apple devices send command results of a
// few hundred KB at most (large inventories); 4 MiB leaves headroom.
const (
	DefaultMaxBytes = 4 << 20
	DefaultMaxDepth = 64
)

// Errors returned by the decoders.
var (
	ErrTooLarge      = errors.New("plist: input exceeds size limit")
	ErrTooDeep       = errors.New("plist: nesting exceeds depth limit")
	ErrUnknownFormat = errors.New("plist: input is neither XML nor binary plist")
)

// Marshaler and Unmarshaler are re-exported so callers do not import the
// underlying library.
type (
	Marshaler   = mplist.Marshaler
	Unmarshaler = mplist.Unmarshaler
)

var binaryMagic = []byte("bplist0")

// DetectFormat inspects the first bytes of data.
func DetectFormat(data []byte) Format {
	if bytes.HasPrefix(data, binaryMagic) {
		return FormatBinary
	}
	trimmed := bytes.TrimLeft(data, " \t\r\n\xef\xbb\xbf")
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<plist")) || bytes.HasPrefix(trimmed, []byte("<!DOCTYPE")) {
		return FormatXML
	}
	return FormatUnknown
}

// Marshal encodes v as an XML plist.
func Marshal(v any) ([]byte, error) {
	out, err := mplist.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("plist: marshal: %w", err)
	}
	return out, nil
}

// MarshalIndent encodes v as an indented XML plist.
func MarshalIndent(v any, indent string) ([]byte, error) {
	out, err := mplist.MarshalIndent(v, indent)
	if err != nil {
		return nil, fmt.Errorf("plist: marshal: %w", err)
	}
	return out, nil
}

// Unmarshal decodes an XML or binary plist with the default limits.
func Unmarshal(data []byte, v any) error {
	return Decoder{}.Unmarshal(data, v)
}

// Decoder decodes with explicit limits. Zero values mean the defaults; a
// negative value disables that limit.
type Decoder struct {
	MaxBytes int
	MaxDepth int
}

func (d Decoder) limits() (maxBytes, maxDepth int) {
	maxBytes, maxDepth = d.MaxBytes, d.MaxDepth
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	return maxBytes, maxDepth
}

// Unmarshal decodes data into v after checking the limits.
func (d Decoder) Unmarshal(data []byte, v any) error {
	maxBytes, maxDepth := d.limits()
	if maxBytes > 0 && len(data) > maxBytes {
		return fmt.Errorf("%w: %d > %d", ErrTooLarge, len(data), maxBytes)
	}
	switch DetectFormat(data) {
	case FormatBinary:
		// The binary parser bounds object nesting itself (128) and detects
		// reference cycles.
	case FormatXML:
		if maxDepth > 0 {
			if err := checkXMLDepth(data, maxDepth); err != nil {
				return err
			}
		}
	case FormatUnknown:
		return ErrUnknownFormat
	}
	if err := mplist.Unmarshal(data, v); err != nil {
		return fmt.Errorf("plist: unmarshal: %w", err)
	}
	return nil
}

// checkXMLDepth scans element nesting without building a tree.
func checkXMLDepth(data []byte, maxDepth int) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			// Leave malformed XML to the real decoder for a better error.
			return nil //nolint:nilerr // intentional: depth check is advisory
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return fmt.Errorf("%w: %d > %d", ErrTooDeep, depth, maxDepth)
			}
		case xml.EndElement:
			depth--
		}
	}
}
