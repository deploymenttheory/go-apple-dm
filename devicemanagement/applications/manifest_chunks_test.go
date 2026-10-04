package applications_test

import (
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
)

// TestManifestChunkIntegrity covers native DDM digest requirements while keeping
// previously assigned whole-file manifests readable. Every provided digest must
// describe the immutable bytes, even when a client ignores one representation.
func TestManifestChunkIntegrity(t *testing.T) {
	c := applications.Content{Metadata: metadata(), Size: 4097, Digests: applications.Digests{SHA256: strings.Repeat("a", 64), MD5: strings.Repeat("b", 32)}}
	for _, tt := range []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{"whole", map[string]any{"sha256": c.SHA256}, true},
		{"chunk", map[string]any{"sha256-size": c.Size, "sha256s": []string{c.SHA256}}, true},
		{"both", map[string]any{"sha256": c.SHA256, "sha256-size": c.Size, "sha256s": []string{c.SHA256}}, true},
		{"missing", nil, false},
		{"whole mismatch", map[string]any{"sha256": strings.Repeat("f", 64)}, false},
		{"no size", map[string]any{"sha256s": []string{c.SHA256}}, false},
		{"no hashes", map[string]any{"sha256-size": c.Size}, false},
		{"wrong size type", map[string]any{"sha256-size": "4097", "sha256s": []string{c.SHA256}}, false},
		{"negative size", map[string]any{"sha256-size": int64(-1), "sha256s": []string{c.SHA256}}, false},
		{"zero size", map[string]any{"sha256-size": 0, "sha256s": []string{c.SHA256}}, false},
		{"short size", map[string]any{"sha256-size": c.Size - 1, "sha256s": []string{c.SHA256}}, false},
		{"large size", map[string]any{"sha256-size": c.Size + 1, "sha256s": []string{c.SHA256}}, false},
		{"hash string", map[string]any{"sha256-size": c.Size, "sha256s": c.SHA256}, false},
		{"no chunks", map[string]any{"sha256-size": c.Size, "sha256s": []string{}}, false},
		{"multiple chunks", map[string]any{"sha256-size": c.Size, "sha256s": []string{c.SHA256, c.SHA256}}, false},
		{"wrong hash type", map[string]any{"sha256-size": c.Size, "sha256s": []bool{true}}, false},
		{"empty hash", map[string]any{"sha256-size": c.Size, "sha256s": []string{""}}, false},
		{"mismatched chunk", map[string]any{"sha256-size": c.Size, "sha256s": []string{strings.Repeat("f", 64)}}, false},
		{"malformed chunk", map[string]any{"sha256-size": c.Size, "sha256s": []string{"invalid"}}, false},
		{"whole cannot mask invalid chunks", map[string]any{"sha256": c.SHA256, "sha256-size": c.Size, "sha256s": []string{strings.Repeat("f", 64)}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			asset := map[string]any{"kind": "software-package", "url": "https://example.test/pkg"}
			for k, v := range tt.fields {
				asset[k] = v
			}
			meta := map[string]any{"bundle-identifier": c.Metadata.BundleID, "bundle-version": c.Metadata.Version, "kind": "software", "title": c.Metadata.PackageName}
			raw, err := plist.Marshal(map[string]any{"items": []any{map[string]any{"assets": []any{asset}, "metadata": meta}}}, plist.XMLFormat)
			if err != nil {
				t.Fatal(err)
			}
			err = applications.ValidateManifest(raw, c)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%t: %v", tt.valid, err)
			}
		})
	}
}
