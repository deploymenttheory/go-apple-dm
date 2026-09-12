package dep_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

func TestCredentialRequestsNeverFollowRedirects(t *testing.T) {
	var leaked atomic.Int64
	destination := httptest.NewTLSServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }),
	)
	defer destination.Close()
	source := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) },
		),
	)
	defer source.Close()
	injected := source.Client()
	f := newFixture(
		t,
		withClient(func(c *dep.ClientConfig) { c.BaseURL = source.URL; c.HTTPClient = injected }),
	)
	if _, err := f.client.Account(t.Context(), acct); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked.Load() != 0 {
		t.Fatal("credential request escaped origin")
	}
	if injected.CheckRedirect != nil {
		t.Fatal("caller client mutated")
	}
}
