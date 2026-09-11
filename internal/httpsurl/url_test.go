package httpsurl

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, raw := range []string{"", "http://example.com/secret", "//example.com", "https:///missing", "https://user:secret@example.com", "https://example.com/#secret", "https:opaque", "https://example.com/%zz"} {
		if u, err := Parse(
			raw,
		); u != nil || !errors.Is(err, ErrURL) ||
			strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe URL: %q %v %v", raw, u, err)
		}
	}
	for _, raw := range []string{"https://example.com/path", "https://127.0.0.1:443", "https://[::1]:443/path?query=value"} {
		if _, err := Parse(raw); err != nil {
			t.Fatal(err)
		}
	}
}
