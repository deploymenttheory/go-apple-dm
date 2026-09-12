//go:build schema_seed_os_27

package contentcache_test

import (
	"bytes"
	"os"
	"testing"
)

func TestSeedOS27ContentCacheContract(t *testing.T) {
	t.Parallel()
	pinned, err := os.ReadFile("testdata/metrics_report.json")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := os.ReadFile("../../third_party/device-management/openapi/content-cache/metrics_report.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pinned, candidate) {
		t.Fatal("Apple content-cache schema changed; review types, validation, and receiver contract before updating the fixture")
	}
}
