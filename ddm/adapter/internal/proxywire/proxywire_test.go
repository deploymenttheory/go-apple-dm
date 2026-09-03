package proxywire_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/ddm/adapter/internal/proxywire"
)

func TestSignVerify(t *testing.T) {
	t.Parallel()
	key := []byte("k1")
	body := []byte("<plist/>")
	t.Run("OK", func(t *testing.T) {
		t.Parallel()
		sig := proxywire.Sign(key, body)
		if sig == "" || sig != proxywire.Sign(key, body) {
			t.Fatalf("signature not deterministic: %q", sig)
		}
		if err := proxywire.Verify(key, sig, body); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})
	t.Run("Missing", func(t *testing.T) {
		t.Parallel()
		if err := proxywire.Verify(key, "", body); !errors.Is(err, proxywire.ErrMissingSignature) {
			t.Fatalf("missing header: %v", err)
		}
	})
	t.Run("Wrong", func(t *testing.T) {
		t.Parallel()
		if err := proxywire.Verify([]byte("other"), proxywire.Sign(key, body), body); !errors.Is(err, proxywire.ErrBadSignature) {
			t.Fatalf("wrong key: %v", err)
		}
		if err := proxywire.Verify(key, proxywire.Sign(key, body), []byte("tampered")); !errors.Is(err, proxywire.ErrBadSignature) {
			t.Fatalf("tampered body: %v", err)
		}
		if err := proxywire.Verify(key, "not base64!", body); !errors.Is(err, proxywire.ErrBadSignature) {
			t.Fatalf("garbage header: %v", err)
		}
	})
	t.Run("EmptyBody", func(t *testing.T) {
		t.Parallel()
		sig := proxywire.Sign(key, nil)
		if err := proxywire.Verify(key, sig, []byte{}); err != nil {
			t.Fatalf("empty body: %v", err)
		}
		if err := proxywire.Verify(key, sig, body); !errors.Is(err, proxywire.ErrBadSignature) {
			t.Fatalf("empty-body signature over a body: %v", err)
		}
	})
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestReadBody(t *testing.T) {
	t.Parallel()
	t.Run("OK", func(t *testing.T) {
		t.Parallel()
		got, err := proxywire.ReadBody(strings.NewReader("abc"), 3)
		if err != nil || string(got) != "abc" {
			t.Fatalf("at limit: %q %v", got, err)
		}
		got, err = proxywire.ReadBody(strings.NewReader("abc"), 0)
		if err != nil || string(got) != "abc" {
			t.Fatalf("default limit: %q %v", got, err)
		}
	})
	t.Run("TooLarge", func(t *testing.T) {
		t.Parallel()
		if _, err := proxywire.ReadBody(strings.NewReader("abcd"), 3); !errors.Is(err, proxywire.ErrBodyTooLarge) {
			t.Fatalf("over limit: %v", err)
		}
	})
	t.Run("ReadError", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		if _, err := proxywire.ReadBody(failingReader{err: boom}, 10); !errors.Is(err, boom) {
			t.Fatalf("read error: %v", err)
		}
		if _, err := proxywire.ReadBody(io.MultiReader(strings.NewReader("x"), failingReader{err: boom}), 10); !errors.Is(err, boom) {
			t.Fatalf("read error after data: %v", err)
		}
	})
}
