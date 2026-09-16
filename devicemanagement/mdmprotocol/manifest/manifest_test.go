package manifest_test

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/manifest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/other"
)

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestManifestHashes(t *testing.T) {
	md := manifest.Metadata{
		BundleIdentifier: "com.example.app",
		BundleVersion:    "1",
		Title:            "Example",
	}
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	const def = "cb8379ac2098aa165029e3938a51da0bcecfc008fd6795f401178647f96c5b34"
	const g = "cd0aa9856147b6c5b4ff2b7dfee5da20aa38253099ef1b4a64aced233c9afe29"
	for _, tc := range []struct {
		data   string
		chunk  int64
		hashes []string
	}{{"abc", 0, []string{abc}}, {"abcdef", 3, []string{abc, def}}, {"abcdefg", 3, []string{abc, def, g}}, {"abc", 10, []string{abc}}} {
		m, n, err := manifest.Build(
			"https://example.com/app.pkg",
			md,
			strings.NewReader(tc.data),
			tc.chunk,
		)
		if err != nil || n != int64(len(tc.data)) {
			t.Fatal(n, err)
		}
		a := m.Items[0].Assets[0]
		if tc.chunk == 0 {
			if a.Sha256 == nil || *a.Sha256 != abc || a.Sha256Size != nil || len(a.Sha256s) != 0 {
				t.Fatal(a)
			}
		} else if a.Sha256 != nil || a.Sha256Size == nil || *a.Sha256Size != tc.chunk || !reflect.DeepEqual(a.Sha256s, tc.hashes) {
			t.Fatal(a)
		}
		wire, err := plist.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"md5", "sizeInBytes", "needs-shine"} {
			if strings.Contains(string(wire), forbidden) {
				t.Fatal(forbidden)
			}
		}
		var decoded other.ManifestURL
		if err := plist.Unmarshal(wire, &decoded); err != nil || !reflect.DeepEqual(*m, decoded) {
			t.Fatal("roundtrip", err)
		}
	}
	for _, url := range []string{"http://example.com/a", "https://u:p@example.com/a", "https:///a", "https://example.com/a#fragment"} {
		if _, _, err := manifest.Build(
			url,
			md,
			strings.NewReader("a"),
			0,
		); !errors.Is(
			err,
			manifest.ErrInput,
		) {
			t.Fatal(url, err)
		}
	}
	for _, chunk := range []int64{0, 3} {
		if m, n, err := manifest.Build(
			"https://example.com/a",
			md,
			brokenReader{},
			chunk,
		); m != nil || n != 0 ||
			!errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(m, n, err)
		}
	}
	if _, _, err := manifest.Build(
		"https://example.com/a",
		md,
		strings.NewReader(""),
		3,
	); !errors.Is(
		err,
		manifest.ErrInput,
	) {
		t.Fatal(err)
	}
}
