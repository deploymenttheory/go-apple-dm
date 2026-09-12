package axm

import (
	"strings"
	"testing"
)

func TestEndpointOverridesRejectPlaintextAndCredentials(t *testing.T) {
	f := newFixture(t)
	for _, raw := range []string{"http://127.0.0.1:1234/secret", "https://user:secret@example.com/", "https://example.com/#secret", "https:///secret"} {
		for _, token := range []bool{false, true} {
			cfg := f.cfg
			if token {
				cfg.TokenURL = raw
			} else {
				cfg.BaseURL = raw
			}
			if c, err := New(
				t.Context(),
				cfg,
			); c != nil || err == nil ||
				strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe endpoint: %v %v", c, err)
			}
		}
	}
	if f.srv.TokenRequests() != 0 {
		t.Fatal("credentials sent during rejected configuration")
	}
}
