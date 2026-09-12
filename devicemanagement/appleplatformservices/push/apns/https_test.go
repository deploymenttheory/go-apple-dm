package apns_test

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push/apns"
)

func TestEndpointRejectedBeforeLoadingPushCredentials(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:1234/secret", "https://user:secret@example.com/", "https://example.com/#secret", "https:///secret"} {
		// Nil certificate storage panics if configuration validation reaches it.
		c := apns.New(nil, apns.WithHost(raw))
		device := target("device", []byte("token"))
		results, err := c.Push(t.Context(), []push.Target{device})
		if err != nil || len(results) != 1 || results[device.ID].Err == nil ||
			strings.Contains(results[device.ID].Err.Error(), "secret") {
			t.Fatalf("unsafe endpoint: %v %v", results, err)
		}
	}
}
