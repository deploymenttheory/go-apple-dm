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
	// The submodule may use CRLF on Windows while the pinned fixture uses LF.
	// Ignore that checkout difference when checking for schema changes.
	pinned = bytes.ReplaceAll(pinned, []byte("\r\n"), []byte("\n"))
	candidate = bytes.ReplaceAll(candidate, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(pinned, candidate) {
		t.Fatal("Apple content-cache schema changed; review types, validation, and receiver contract before updating the fixture")
	}
}
