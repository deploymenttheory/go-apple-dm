package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// TestContract runs the shared MDM store suite against in-memory storage.
func TestContract(t *testing.T) {
	t.Parallel()
	storagetest.RunAll(t, func(t *testing.T) storage.Store {
		t.Helper()
		return inmem.New()
	})
}
