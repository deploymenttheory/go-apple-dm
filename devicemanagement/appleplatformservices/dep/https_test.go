package dep_test

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
)

func TestEndpointOverridesRejectPlaintextAndCredentials(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:1234/secret", "https://user:secret@example.com/", "https://example.com/#secret", "https:///secret"} {
		if c, err := dep.NewClient(
			dep.ClientConfig{Store: inmem.New(), BaseURL: raw},
		); c != nil || err == nil ||
			strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe endpoint: %v %v", c, err)
		}
	}
}
