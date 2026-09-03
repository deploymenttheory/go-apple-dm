package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/adminauth"
	"github.com/deploymenttheory/go-apple-dm/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-dm/adminauth/inmem"
)

func TestContract(t *testing.T) {
	adminauthtest.RunSuite(t, func(t *testing.T) adminauth.Store {
		t.Helper()
		return inmem.New()
	})
}
