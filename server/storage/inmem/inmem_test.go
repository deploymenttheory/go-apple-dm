package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/storagetest"
)

func TestContract(t *testing.T) {
	t.Parallel()
	storagetest.RunAll(t, func(t *testing.T) storage.Store { return inmem.New() })
}
