package crypt

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/deploymenttheory/go-apple-mdm/secrets"
)

// material returns a deterministic key value of the given length.
func material(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

// provider holds two strong keys used by most tests.
var provider = secrets.Static{
	"v1": material(1, 32),
	"v2": material(2, 32),
}

// newRing constructs a Keyring or fails the test.
func newRing(t *testing.T, keys Keys, p secrets.Provider) *Keyring {
	t.Helper()
	k, err := NewKeyring(context.Background(), Options{Keys: keys, Provider: p})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return k
}

// failingProvider returns its error for every name.
type failingProvider struct{ err error }

func (f failingProvider) Get(context.Context, string) (secrets.Secret, error) {
	return secrets.Secret{}, f.err
}

// countingProvider records how many times each name was fetched.
type countingProvider struct {
	inner secrets.Provider
	calls map[string]int
}

func (c *countingProvider) Get(ctx context.Context, name string) (secrets.Secret, error) {
	c.calls[name]++
	return c.inner.Get(ctx, name)
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := newRing(t, Keys{Active: "v1"}, provider)
	if k.Active() != "v1" {
		t.Fatalf("Active = %q, want v1", k.Active())
	}
	if k.Strict() {
		t.Fatal("Strict should be false by default")
	}
	aad := AAD("unlock_token", "device-1")

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{name: "text", plaintext: []byte("hello, world")},
		{name: "binary", plaintext: material(0, 300)},
		{name: "empty", plaintext: []byte{}},
		{name: "nil", plaintext: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sealed, err := k.Seal(tc.plaintext, aad)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if !IsSealed(sealed) {
				t.Fatal("Seal output should report IsSealed")
			}
			if name, ok := KeyName(sealed); !ok || name != "v1" {
				t.Fatalf("KeyName = %q, %v; want v1, true", name, ok)
			}
			if want := headerLen + len("v1") + nonceLen + len(tc.plaintext) + tagLen; len(sealed) != want {
				t.Fatalf("len(sealed) = %d, want %d", len(sealed), want)
			}
			got, keyName, err := k.Open(sealed, aad)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if keyName != "v1" {
				t.Fatalf("keyName = %q, want v1", keyName)
			}
			if !bytes.Equal(got, tc.plaintext) {
				t.Fatalf("Open = %x, want %x", got, tc.plaintext)
			}
			again, err := k.Seal(tc.plaintext, aad)
			if err != nil {
				t.Fatalf("Seal again: %v", err)
			}
			if bytes.Equal(sealed, again) {
				t.Fatal("two seals of the same plaintext should differ")
			}
		})
	}
}

func TestSealStrict(t *testing.T) {
	k := newRing(t, Keys{Active: "v1", Strict: true}, provider)
	if !k.Strict() {
		t.Fatal("Strict should be true")
	}
}

func TestSealNonceFailure(t *testing.T) {
	k := newRing(t, Keys{Active: "v1"}, provider)
	orig := randReader
	t.Cleanup(func() { randReader = orig })
	boom := errors.New("boom")
	randReader = iotest.ErrReader(boom)
	if _, err := k.Seal([]byte("x"), nil); !errors.Is(err, boom) {
		t.Fatalf("Seal with failing nonce source = %v, want %v", err, boom)
	}
}

func TestOpenRejectsTamper(t *testing.T) {
	k := newRing(t, Keys{Active: "v1"}, provider)
	aad := AAD("bootstrap_token", "device-1")
	sealed, err := k.Seal([]byte("secret material"), aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	nameStart := headerLen
	nonceStart := nameStart + len("v1")
	bodyStart := nonceStart + nonceLen
	minLen := bodyStart + tagLen

	flip := func(i int) []byte {
		c := append([]byte(nil), sealed...)
		c[i] ^= 0x01
		return c
	}
	zeroNameLen := func(b []byte) []byte {
		c := append([]byte(nil), b...)
		c[magicLen] = 0
		return c
	}
	tests := []struct {
		name  string
		input []byte
		want  error
	}{
		{name: "flip magic", input: flip(1), want: ErrBadFormat},
		{name: "flip version", input: flip(magicLen - 1), want: ErrBadFormat},
		{name: "flip name", input: flip(nameStart), want: ErrUnknownKey},
		{name: "flip nonce", input: flip(nonceStart), want: ErrTampered},
		{name: "flip body", input: flip(bodyStart), want: ErrTampered},
		{name: "flip tag", input: flip(len(sealed) - 1), want: ErrTampered},
		{name: "zero name length", input: zeroNameLen(sealed), want: ErrBadFormat},
		{name: "empty", input: []byte{}, want: ErrBadFormat},
		{name: "nil", input: nil, want: ErrBadFormat},
		{name: "truncated to 3", input: sealed[:3], want: ErrBadFormat},
		{name: "truncated to magic", input: sealed[:magicLen], want: ErrBadFormat},
		{name: "truncated to length byte", input: sealed[:headerLen], want: ErrBadFormat},
		{name: "truncated inside name", input: sealed[:nameStart+1], want: ErrBadFormat},
		{name: "truncated inside nonce", input: sealed[:nonceStart+5], want: ErrBadFormat},
		{name: "truncated before tag", input: sealed[:minLen-1], want: ErrBadFormat},
		{name: "truncated to tag only", input: sealed[:minLen], want: ErrTampered},
		{name: "truncated last byte", input: sealed[:len(sealed)-1], want: ErrTampered},
		{name: "extra byte", input: append(append([]byte(nil), sealed...), 0), want: ErrTampered},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pt, name, err := k.Open(tc.input, aad)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Open = %v, want %v", err, tc.want)
			}
			if pt != nil || name != "" {
				t.Fatalf("Open returned %x, %q with error; want nil, empty", pt, name)
			}
		})
	}
}

func TestOpenRejectsWrongAAD(t *testing.T) {
	k := newRing(t, Keys{Active: "v1"}, provider)
	sealed, err := k.Seal([]byte("secret"), AAD("unlock_token", "device-1"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	tests := []struct {
		name string
		aad  []byte
	}{
		{name: "other row", aad: AAD("unlock_token", "device-2")},
		{name: "other column", aad: AAD("bootstrap_token", "device-1")},
		{name: "nil", aad: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := k.Open(sealed, tc.aad); !errors.Is(err, ErrTampered) {
				t.Fatalf("Open with wrong aad = %v, want ErrTampered", err)
			}
		})
	}
}

func TestOpenRejectsUnknownKey(t *testing.T) {
	a := newRing(t, Keys{Active: "v1"}, provider)
	b := newRing(t, Keys{Active: "v2"}, provider)
	sealed, err := a.Seal([]byte("secret"), nil)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	_, _, err = b.Open(sealed, nil)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Open = %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), `"v1"`) {
		t.Fatalf("error %q should name the unknown key", err)
	}
}

func TestOpenAcceptsRetiredKey(t *testing.T) {
	a := newRing(t, Keys{Active: "v1"}, provider)
	b := newRing(t, Keys{Active: "v2", Accepted: []string{"v1"}}, provider)
	aad := AAD("unlock_token", "device-1")
	sealed, err := a.Seal([]byte("secret"), aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	pt, name, err := b.Open(sealed, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if name != "v1" || string(pt) != "secret" {
		t.Fatalf("Open = %q, %q; want secret, v1", pt, name)
	}
	resealed, err := b.Seal(pt, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if name, ok := KeyName(resealed); !ok || name != "v2" {
		t.Fatalf("resealed KeyName = %q, %v; want v2", name, ok)
	}
	if _, _, err := a.Open(resealed, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("old ring opening new key = %v, want ErrUnknownKey", err)
	}
}

func TestNewKeyringErrors(t *testing.T) {
	other := errors.New("backend down")
	tests := []struct {
		name     string
		keys     Keys
		provider secrets.Provider
		want     error
		contains string
	}{
		{name: "empty active", keys: Keys{}, provider: provider, want: ErrNoActive},
		{name: "nil provider", keys: Keys{Active: "v1"}, provider: nil, want: ErrNoKeyring},
		{
			name:     "active not found",
			keys:     Keys{Active: "missing"},
			provider: provider,
			want:     secrets.ErrNotFound,
			contains: `"missing"`,
		},
		{
			name:     "accepted not found",
			keys:     Keys{Active: "v1", Accepted: []string{"gone"}},
			provider: provider,
			want:     secrets.ErrNotFound,
			contains: `"gone"`,
		},
		{name: "provider error", keys: Keys{Active: "v1"}, provider: failingProvider{err: other}, want: other},
		{
			name:     "weak key",
			keys:     Keys{Active: "weak"},
			provider: secrets.Static{"weak": material(0, 15)},
			want:     ErrWeakKey,
		},
		{name: "name with NUL", keys: Keys{Active: "v\x001"}, provider: provider, want: ErrBadFormat},
		{
			name:     "accepted name with NUL",
			keys:     Keys{Active: "v1", Accepted: []string{"bad\x00"}},
			provider: provider,
			want:     ErrBadFormat,
		},
		{
			name:     "name too long",
			keys:     Keys{Active: strings.Repeat("n", 256)},
			provider: provider,
			want:     ErrBadFormat,
		},
		{
			name:     "empty accepted name",
			keys:     Keys{Active: "v1", Accepted: []string{""}},
			provider: provider,
			want:     ErrBadFormat,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := NewKeyring(context.Background(), Options{Keys: tc.keys, Provider: tc.provider})
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewKeyring = %v, want %v", err, tc.want)
			}
			if k != nil {
				t.Fatal("NewKeyring should return a nil ring on error")
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error %q should contain %q", err, tc.contains)
			}
		})
	}
}

func TestNewKeyringAcceptsEdgeNames(t *testing.T) {
	long := strings.Repeat("n", 255)
	p := &countingProvider{
		inner: secrets.Static{"v1": material(1, 16), long: material(3, 64)},
		calls: map[string]int{},
	}
	k := newRing(t, Keys{Active: "v1", Accepted: []string{"v1", long, long}}, p)
	if len(k.keys) != 2 {
		t.Fatalf("ring holds %d keys, want 2", len(k.keys))
	}
	if p.calls["v1"] != 1 || p.calls[long] != 1 {
		t.Fatalf("provider calls = %v, want one per distinct name", p.calls)
	}
	sealed, err := k.Seal([]byte("x"), nil)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, _, err := k.Open(sealed, nil); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// A ring whose active key is the long name round trips through the
	// one byte length field.
	l := newRing(t, Keys{Active: long}, p)
	sealed, err = l.Seal([]byte("y"), nil)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if name, ok := KeyName(sealed); !ok || name != long {
		t.Fatalf("KeyName = %d bytes, %v; want 255 byte name", len(name), ok)
	}
	if pt, name, err := k.Open(sealed, nil); err != nil || name != long || string(pt) != "y" {
		t.Fatalf("Open = %q, %d bytes, %v", pt, len(name), err)
	}
}

func TestDistinctNamesDeriveDistinctKeys(t *testing.T) {
	same := secrets.Static{"a": material(9, 32), "b": material(9, 32)}
	a := newRing(t, Keys{Active: "a"}, same)
	b := newRing(t, Keys{Active: "b", Accepted: []string{"a"}}, same)
	sealed, err := a.Seal([]byte("secret"), nil)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Rewrite the header so the value claims key "b"; the same provider
	// bytes under a different name must not open it.
	forged := append([]byte(nil), sealed...)
	forged[headerLen] = 'b'
	if _, _, err := b.Open(forged, nil); !errors.Is(err, ErrTampered) {
		t.Fatalf("Open under renamed key = %v, want ErrTampered", err)
	}
	if _, name, err := b.Open(sealed, nil); err != nil || name != "a" {
		t.Fatalf("Open under original name = %q, %v", name, err)
	}
}

func TestIsSealedAndKeyName(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		sealed   bool
		keyName  string
		keyFound bool
	}{
		{name: "plist", input: []byte("<?xml version=\"1.0\"?><plist/>")},
		{name: "binary plist", input: []byte("bplist00")},
		{name: "pem", input: []byte("-----BEGIN CERTIFICATE-----\n")},
		{name: "empty", input: []byte{}},
		{name: "nil", input: nil},
		{name: "five byte prefix", input: magic[:5]},
		{name: "wrong version", input: []byte{0x00, 'g', 'a', 'm', 'c', 0x02, 1, 'a'}},
		{name: "magic only", input: magic[:], sealed: true},
		{name: "zero length name", input: append(append([]byte(nil), magic[:]...), 0, 'a'), sealed: true},
		{name: "length exceeds input", input: append(append([]byte(nil), magic[:]...), 5, 'a', 'b'), sealed: true},
		{
			name:     "exact name",
			input:    append(append([]byte(nil), magic[:]...), 2, 'a', 'b'),
			sealed:   true,
			keyName:  "ab",
			keyFound: true,
		},
		{
			name:     "name and more",
			input:    append(append([]byte(nil), magic[:]...), 1, 'k', 0xff),
			sealed:   true,
			keyName:  "k",
			keyFound: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSealed(tc.input); got != tc.sealed {
				t.Fatalf("IsSealed = %v, want %v", got, tc.sealed)
			}
			name, ok := KeyName(tc.input)
			if ok != tc.keyFound || name != tc.keyName {
				t.Fatalf("KeyName = %q, %v; want %q, %v", name, ok, tc.keyName, tc.keyFound)
			}
		})
	}
}

func TestNilKeyring(t *testing.T) {
	var k *Keyring
	if _, err := k.Seal([]byte("x"), nil); !errors.Is(err, ErrNoKeyring) {
		t.Fatalf("nil Seal = %v, want ErrNoKeyring", err)
	}
	if _, _, err := k.Open([]byte("x"), nil); !errors.Is(err, ErrNoKeyring) {
		t.Fatalf("nil Open = %v, want ErrNoKeyring", err)
	}
	if k.Active() != "" {
		t.Fatalf("nil Active = %q, want empty", k.Active())
	}
	if k.Strict() {
		t.Fatal("nil Strict should be false")
	}
}

func TestAAD(t *testing.T) {
	tests := []struct {
		purpose, rowID string
		want           []byte
	}{
		{purpose: "unlock_token", rowID: "device-1", want: []byte("unlock_token\x00device-1")},
		{purpose: "", rowID: "", want: []byte{0}},
		{purpose: "p", rowID: "", want: []byte("p\x00")},
		{purpose: "", rowID: "r", want: []byte("\x00r")},
	}
	for _, tc := range tests {
		if got := AAD(tc.purpose, tc.rowID); !bytes.Equal(got, tc.want) {
			t.Errorf("AAD(%q, %q) = %q, want %q", tc.purpose, tc.rowID, got, tc.want)
		}
	}
	// The separator keeps different splits of the same bytes apart.
	if bytes.Equal(AAD("ab", "c"), AAD("a", "bc")) {
		t.Fatal("AAD should distinguish purpose from rowID")
	}
}
