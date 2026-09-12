package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

func TestContract(t *testing.T) {
	t.Parallel()
	storagetest.RunAll(t, func(t *testing.T) storage.Store { return inmem.New() })
}
