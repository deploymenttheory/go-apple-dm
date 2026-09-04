package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/dep/inmem"
)

func TestContract(t *testing.T) {
	deptest.RunStoreSuite(t, func(_ *testing.T, k *crypt.Keyring) dep.Store {
		if k != nil {
			return inmem.New(inmem.WithKeyring(k))
		}
		return inmem.New()
	})
}
