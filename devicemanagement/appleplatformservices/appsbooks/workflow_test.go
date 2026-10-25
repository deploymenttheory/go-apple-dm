package appsbooks_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// TestWorkflowMemory runs the same workflow contract used by sealed SQL backends.
func TestWorkflowMemory(t *testing.T) {
	storagetest.RunAppsBooks(t, func(*testing.T) (state.Store, state.Store) { s := state.NewMemory(); return s, s })
}
