package contentcache_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache/storetest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestReportStorage(t *testing.T) {
	store := state.NewMemory()
	storetest.Run(t, store, store)
}
