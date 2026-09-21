package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
)

// TestContract runs the shared authorization-store suite against in-memory storage.
func TestContract(t *testing.T) {
	adminauthtest.RunSuite(t, func(t *testing.T) adminauth.Store {
		t.Helper()
		return inmem.New()
	})
}
