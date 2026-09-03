package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/storage/storagetest"
)

func TestContract(t *testing.T) {
	t.Parallel()
	storagetest.RunAll(t, func(t *testing.T) storage.Store { return inmem.New() })
}
