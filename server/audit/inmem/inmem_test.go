package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit/inmem"
)

func TestContract(t *testing.T) {
	audittest.RunSuite(t, func(t *testing.T) audit.Store {
		t.Helper()
		return inmem.New()
	})
}
