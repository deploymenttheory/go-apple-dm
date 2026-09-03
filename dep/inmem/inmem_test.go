package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

func TestContract(t *testing.T) {
	deptest.RunStoreSuite(t, func(_ *testing.T, k *crypt.Keyring) dep.Store {
		if k != nil {
			return inmem.New(inmem.WithKeyring(k))
		}
		return inmem.New()
	})
}
