package applications

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"time"

	"howett.net/plist"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/other"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Manifest is an explicitly assigned Apple installation manifest tied to one
// immutable content revision. Data is the original XML or binary plist, encoded
// as base64 in JSON. FileName is a display name, never a filesystem path.
type Manifest struct {
	FileName        string    `json:"manifestFileName"`
	Data            []byte    `json:"data"`
	ContentRevision string    `json:"contentRevision"`
	CreatedAt       time.Time `json:"createdAt"`
}

// validateHTTPS rejects insecure URLs, credentials, fragments and oversized locations.
func validateHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || !validText(raw, 8192, false) {
		return invalidField("HTTPS URL")
	}
	return nil
}

// BuildManifest generates the native installation manifest from verified content.
// downloadURL must resolve to exactly this revision. Device URL authorization and
// lifetime belong to the hosting server. SHA-256 and legacy MD5 describe identical
// bytes; neither is substituted for package signer trust verification.
func BuildManifest(c Content, downloadURL string) ([]byte, error) {
	if err := validateHTTPS(downloadURL); err != nil {
		return nil, err
	}
	if !c.Verification.SignatureValid || !c.Verification.Trusted || c.Size <= 0 {
		return nil, ErrIntegrity
	}
	if !validDigest(c.SHA256, 32) || !validDigest(c.MD5, 16) {
		return nil, ErrIntegrity
	}
	if c.Metadata.BundleID == "" || c.Metadata.Version == "" {
		return nil, invalidField("application identity")
	}
	manifest := other.ManifestURL{Items: []other.ManifestURLItems{{
		Assets:   []other.ManifestURLItemsAssets{{Kind: "software-package", Url: downloadURL, Sha256: &c.SHA256, Md5: &c.MD5}},
		Metadata: other.ManifestURLItemsMetadata{BundleIdentifier: c.Metadata.BundleID, BundleVersion: &c.Metadata.Version, Kind: "software", Title: c.Metadata.PackageName},
	}}}
	return plist.Marshal(manifest, plist.XMLFormat)
}

// ValidateManifest accepts one package asset, checks its HTTPS URL and requires
// a whole-file SHA-256 matching the content revision. Identity must also match.
// Chunk hashes, additional assets and unknown keys are rejected rather than
// retained without verification. Optional MD5 is checked when supplied.
func ValidateManifest(data []byte, c Content) error {
	if len(data) == 0 {
		return invalidField("manifest")
	}
	if len(data) > 1<<20 {
		return ErrTooLarge
	}
	var raw map[string]any
	decoder := plist.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("%w: manifest plist: %w", ErrInvalid, err)
	}
	if !onlyKeys(raw, "items") {
		return invalidField("manifest keys")
	}
	items, ok := raw["items"].([]any)
	if !ok || len(items) != 1 {
		return invalidField("manifest items")
	}
	item, ok := items[0].(map[string]any)
	if !ok || !onlyKeys(item, "assets", "metadata") {
		return invalidField("manifest item")
	}
	assets, ok := item["assets"].([]any)
	if !ok || len(assets) != 1 {
		return invalidField("manifest assets")
	}
	asset, ok := assets[0].(map[string]any)
	if !ok || !onlyKeys(asset, "kind", "url", "sha256", "md5") {
		return invalidField("manifest asset")
	}
	if asset["kind"] != "software-package" {
		return invalidField("manifest asset kind")
	}
	rawURL, ok := asset["url"].(string)
	if !ok {
		return invalidField("manifest URL")
	}
	if err := validateHTTPS(rawURL); err != nil {
		return err
	}
	sha, ok := asset["sha256"].(string)
	if !ok || sha == "" {
		return invalidField("manifest sha256")
	}
	expected := Digests{SHA256: sha}
	if v, exists := asset["md5"]; exists {
		expected.MD5, ok = v.(string)
		if !ok || expected.MD5 == "" {
			return invalidField("manifest md5")
		}
	}
	if err := expected.Match(c.Digests); err != nil {
		return err
	}
	meta, ok := item["metadata"].(map[string]any)
	if !ok || !onlyKeys(meta, "bundle-identifier", "bundle-version", "kind", "title", "subtitle") {
		return invalidField("manifest metadata")
	}
	if meta["kind"] != "software" || meta["bundle-identifier"] != c.Metadata.BundleID || meta["bundle-version"] != c.Metadata.Version || meta["title"] != c.Metadata.PackageName {
		return fmt.Errorf("%w: manifest identity", ErrIntegrity)
	}
	if v, exists := meta["subtitle"]; exists {
		subtitle, ok := v.(string)
		if !ok || !validText(subtitle, 255, false) {
			return invalidField("manifest subtitle")
		}
	}
	return nil
}

// onlyKeys rejects manifest properties outside the explicitly validated schema.
func onlyKeys(m map[string]any, keys ...string) bool {
	for k := range m {
		found := false
		for _, allowed := range keys {
			if k == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// AssignManifest validates a supplied manifest and attaches it atomically to the
// current content revision. Concurrent metadata or content changes cause conflict.
func (m *Manager) AssignManifest(ctx context.Context, id, expected, fileName string, data []byte) (Record, error) {
	if !ValidID(id) || expected == "" || !validFileName(fileName, ".plist") {
		return Record{}, ErrInvalid
	}
	var out Record
	err := m.cfg.State.Update(ctx, []string{catalogueLock, recordPrefix + id}, func(tx state.Tx) error {
		r, err := read[Record](ctx, tx, recordPrefix+id)
		if err != nil {
			return err
		}
		if r.Revision != expected || r.Deleting {
			return ErrConflict
		}
		if r.Content == nil {
			return fmt.Errorf("%w: verified content required", ErrInvalid)
		}
		if err = ValidateManifest(data, *r.Content); err != nil {
			return err
		}
		r.Manifest = &Manifest{FileName: fileName, Data: bytes.Clone(data), ContentRevision: r.Content.Revision, CreatedAt: tx.Now()}
		r.Revision = rand.Text()
		r.UpdatedAt = tx.Now()
		if err = appendHistory(ctx, tx, &r, "manifest-assigned", ""); err != nil {
			return err
		}
		if err = put(ctx, tx, recordPrefix+id, r); err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}

// DeleteManifest removes only the assigned manifest, retaining verified content.
func (m *Manager) DeleteManifest(ctx context.Context, id, expected string) (Record, error) {
	if !ValidID(id) || expected == "" {
		return Record{}, ErrInvalid
	}
	var out Record
	err := m.cfg.State.Update(ctx, []string{catalogueLock, recordPrefix + id}, func(tx state.Tx) error {
		r, err := read[Record](ctx, tx, recordPrefix+id)
		if err != nil {
			return err
		}
		if r.Revision != expected || r.Deleting {
			return ErrConflict
		}
		r.Manifest = nil
		r.Revision = rand.Text()
		r.UpdatedAt = tx.Now()
		if err = appendHistory(ctx, tx, &r, "manifest-deleted", ""); err != nil {
			return err
		}
		if err = put(ctx, tx, recordPrefix+id, r); err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}
