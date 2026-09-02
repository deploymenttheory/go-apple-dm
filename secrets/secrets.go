package secrets

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Redacted is what a Secret prints as.
const Redacted = "[redacted]"

// ErrNotFound is returned when a provider has no value for a name.
var ErrNotFound = errors.New("secrets: not found")

// ErrName is returned for names a provider cannot map safely.
var ErrName = errors.New("secrets: invalid name")

// maxSize bounds a secret read from a file.
const maxSize = 1 << 20

// Secret holds a credential. Its String, GoString, Format, MarshalJSON,
// and MarshalText outputs are always Redacted; use Bytes to get the value.
type Secret struct{ b []byte }

// New wraps a value (copied).
func New(b []byte) Secret { return Secret{b: append([]byte(nil), b...)} }

// Bytes returns a copy of the value.
func (s Secret) Bytes() []byte { return append([]byte(nil), s.b...) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return len(s.b) == 0 }

// Equal compares in constant time.
func (s Secret) Equal(o Secret) bool { return subtle.ConstantTimeCompare(s.b, o.b) == 1 }

// String implements fmt.Stringer with a constant.
func (Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer with a constant.
func (Secret) GoString() string { return "secrets.Secret(" + Redacted + ")" }

// Format implements fmt.Formatter so every verb prints the constant.
func (Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, Redacted) }

// MarshalJSON implements json.Marshaler with the constant.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler with the constant.
func (Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue implements slog.LogValuer indirectly through String; slog uses
// the Stringer for values that are not primitives.

// Provider resolves secrets by name.
type Provider interface {
	Get(ctx context.Context, name string) (Secret, error)
}

// Static serves secrets from memory, for tests and embedded config.
type Static map[string][]byte

// Get implements Provider.
func (s Static) Get(_ context.Context, name string) (Secret, error) {
	v, ok := s[name]
	if !ok {
		return Secret{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return New(v), nil
}

// Env reads secrets from environment variables. The name is upper-cased
// with dots and dashes replaced by underscores, then prefixed.
type Env struct {
	Prefix string
	// Lookup defaults to os.LookupEnv.
	Lookup func(string) (string, bool)
}

// Key returns the variable name for a secret name.
func (e Env) Key(name string) string {
	r := strings.NewReplacer(".", "_", "-", "_", "/", "_")
	return e.Prefix + strings.ToUpper(r.Replace(name))
}

// Get implements Provider.
func (e Env) Get(_ context.Context, name string) (Secret, error) {
	lookup := e.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	v, ok := lookup(e.Key(name))
	if !ok || v == "" {
		return Secret{}, fmt.Errorf("%w: %s (%s)", ErrNotFound, name, e.Key(name))
	}
	return New([]byte(v)), nil
}

// Dir reads each secret from a file named after it inside one directory,
// the layout Docker and Kubernetes secrets mount. Trailing newlines are
// trimmed. Names must not contain path separators.
type Dir struct{ root *os.Root }

// NewDir opens the directory.
func NewDir(path string) (*Dir, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: open %s: %w", path, err)
	}
	return &Dir{root: root}, nil
}

// Close releases the directory handle.
func (d *Dir) Close() error {
	if err := d.root.Close(); err != nil {
		return fmt.Errorf("secrets: close: %w", err)
	}
	return nil
}

// Get implements Provider.
func (d *Dir) Get(_ context.Context, name string) (Secret, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return Secret{}, fmt.Errorf("%w: %q", ErrName, name)
	}
	f, err := d.root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Secret{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return Secret{}, fmt.Errorf("secrets: open %s: %w", name, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return Secret{}, fmt.Errorf("secrets: read %s: %w", name, err)
	}
	if len(b) > maxSize {
		return Secret{}, fmt.Errorf("secrets: %s exceeds %d bytes", name, maxSize)
	}
	return New([]byte(strings.TrimRight(string(b), "\r\n"))), nil
}

// Chain queries providers in order and returns the first hit.
type Chain []Provider

// Get implements Provider. Errors other than ErrNotFound stop the chain.
func (c Chain) Get(ctx context.Context, name string) (Secret, error) {
	for _, p := range c {
		s, err := p.Get(ctx, name)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Secret{}, err
		}
	}
	return Secret{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}
