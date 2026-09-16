package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/other"
)

// ErrInput indicates invalid metadata, URL, chunk size or empty asset data.
var ErrInput = errors.New("manifest: invalid input")

// Metadata describes a macOS package or an enterprise iOS/iPadOS app. For apps,
// BundleVersion is CFBundleVersion, not CFBundleShortVersionString.
type Metadata struct {
	BundleIdentifier string
	BundleVersion    string
	Title            string
	Subtitle         string
}

// Build streams SHA-256 hashes into a generated manifest and returns the asset
// byte count separately. chunkSize=0 selects a whole-file digest; positive values
// select chunks, including a final short chunk. It never emits MD5 or removed
// sizeInBytes/metadata.items fields. URL must use HTTPS without credentials.
// The reader is neither closed nor retained. An error returns no partial manifest.
// https://developer.apple.com/documentation/devicemanagement/installing-packages
func Build(
	assetURL string,
	metadata Metadata,
	reader io.Reader,
	chunkSize int64,
) (*other.ManifestURL, int64, error) {
	u, err := url.Parse(assetURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
		metadata.BundleIdentifier == "" || metadata.BundleVersion == "" || metadata.Title == "" ||
		reader == nil ||
		chunkSize < 0 {
		return nil, 0, ErrInput
	}
	asset := other.ManifestURLItemsAssets{Kind: "software-package", Url: assetURL}
	var size int64
	if chunkSize == 0 {
		h := sha256.New()
		size, err = io.Copy(h, reader)
		digest := hex.EncodeToString(h.Sum(nil))
		asset.Sha256 = &digest
	} else {
		asset.Sha256Size = &chunkSize
		for {
			h := sha256.New()
			var n int64
			n, err = io.CopyN(h, reader, chunkSize)
			size += n
			if n > 0 {
				asset.Sha256s = append(asset.Sha256s, hex.EncodeToString(h.Sum(nil)))
			}
			if errors.Is(err, io.EOF) {
				err = nil
				break
			}
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return nil, 0, fmt.Errorf("manifest: read asset: %w", err)
	}
	if size == 0 {
		return nil, 0, ErrInput
	}
	md := other.ManifestURLItemsMetadata{
		BundleIdentifier: metadata.BundleIdentifier,
		BundleVersion:    &metadata.BundleVersion,
		Title:            metadata.Title,
		Kind:             "software",
	}
	if metadata.Subtitle != "" {
		md.Subtitle = &metadata.Subtitle
	}
	return &other.ManifestURL{
		Items: []other.ManifestURLItems{
			{Assets: []other.ManifestURLItemsAssets{asset}, Metadata: md},
		},
	}, size, nil
}
