package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/audit"
	"github.com/deploymenttheory/go-apple-dm/server/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/server/audit/inmem"
)

func TestContract(t *testing.T) {
	audittest.RunSuite(t, func(t *testing.T) audit.Store {
		t.Helper()
		return inmem.New()
	})
}
