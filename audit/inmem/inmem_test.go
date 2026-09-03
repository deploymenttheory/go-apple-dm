package inmem_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/audit"
	"github.com/deploymenttheory/go-apple-dm/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/audit/inmem"
)

func TestContract(t *testing.T) {
	audittest.RunSuite(t, func(t *testing.T) audit.Store {
		t.Helper()
		return inmem.New()
	})
}
