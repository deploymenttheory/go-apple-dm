package contentcache_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache/storetest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// TestReportStorage checks persistent content-cache report storage and retrieval.
func TestReportStorage(t *testing.T) {
	store := state.NewMemory()
	storetest.Run(t, store, store)
}
