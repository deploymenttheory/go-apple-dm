package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/dep/deptest"
	"github.com/deploymenttheory/go-apple-mdm/dep/inmem"
	"github.com/deploymenttheory/go-apple-mdm/storage/crypt"
)

func TestContract(t *testing.T) {
	deptest.RunStoreSuite(t, func(_ *testing.T, k *crypt.Keyring) dep.Store {
		if k != nil {
			return inmem.New(inmem.WithKeyring(k))
		}
		return inmem.New()
	})
}
