package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
)

func TestContract(t *testing.T) {
	deptest.RunStoreSuite(t, func(_ *testing.T, k *crypt.Keyring) dep.Store {
		if k != nil {
			return inmem.New(inmem.WithKeyring(k))
		}
		return inmem.New()
	})
}
