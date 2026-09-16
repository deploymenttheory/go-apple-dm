package recovery

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

type rejectedRecipient struct{}

func (rejectedRecipient) Wrap([]byte) ([]*age.Stanza, error) {
	return nil, errors.New("recipient unavailable")
}

type failingArchiveWriter struct{ left int }

func (w *failingArchiveWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		return 0, errors.New("archive storage failed")
	}
	w.left -= len(p)
	return len(p), nil
}

func TestArchivePublicationFailuresNeverPublish(t *testing.T) {
	_, stage, identity, manifest := archiveFixture(t)
	for name, change := range map[string]func(*Metadata, *[]age.Recipient){
		"missing recipient":           func(_ *Metadata, r *[]age.Recipient) { *r = nil },
		"unsupported backend":         func(m *Metadata, _ *[]age.Recipient) { m.Backend = "other" },
		"invalid creation time":       func(m *Metadata, _ *[]age.Recipient) { m.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"failed encryption recipient": func(_ *Metadata, r *[]age.Recipient) { *r = []age.Recipient{rejectedRecipient{}} },
	} {
		t.Run(name, func(t *testing.T) {
			metadata := manifest.Metadata
			recipients := []age.Recipient{identity.Recipient()}
			change(&metadata, &recipients)
			dir := t.TempDir()
			if _, err := Create(t.Context(), filepath.Join(dir, "checkpoint"), stage, metadata, recipients, Limits{}); err == nil {
				t.Fatal("published failed archive")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("failed publication left files")
			}
		})
	}
	if _, err := Create(t.Context(), filepath.Join(t.TempDir(), "missing", "archive"), stage, manifest.Metadata, []age.Recipient{identity.Recipient()}, Limits{}); err == nil {
		t.Fatal("ignored missing destination parent")
	}
	if _, err := inventory(t.Context(), "missing", Limits{}.defaults()); err == nil {
		t.Fatal("accepted missing stage")
	}
	if _, err := inventory(t.Context(), t.TempDir(), Limits{}.defaults()); err == nil {
		t.Fatal("accepted empty stage")
	}
}

func TestAuthenticatedArchiveRequiresOneLeadingManifest(t *testing.T) {
	_, _, identity, _ := archiveFixture(t)
	for _, tc := range []struct {
		name string
		kind byte
		body string
	}{
		{"", 0, ""}, {"database.json", tar.TypeReg, "{}"}, {"manifest.json", tar.TypeDir, ""}, {"manifest.json", tar.TypeReg, "{} {}"},
	} {
		var raw bytes.Buffer
		encrypted, err := age.Encrypt(&raw, identity.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		archive := tar.NewWriter(encrypted)
		if tc.name != "" {
			if err := archive.WriteHeader(&tar.Header{Name: tc.name, Typeflag: tc.kind, Size: int64(len(tc.body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(archive, tc.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		if err := encrypted.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "malformed.age")
		if err := writePrivate(path, raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if v, err := Verify(t.Context(), path, t.TempDir(), []age.Identity{identity}, Limits{}); err == nil {
			_ = v.Close()
			t.Fatal("accepted missing or ambiguous manifest")
		}
	}
}

func TestArchiveAuthenticatesFinalChunkAfterCompleteTar(t *testing.T) {
	_, _, key, m := archiveFixture(t)
	payload := bytes.Repeat([]byte("x"), 62976)
	sum := sha256.Sum256(payload)
	m.Entries = []Entry{{Name: "state", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > 512 {
		t.Fatal("fixture no longer fills one encrypted chunk", err)
	}
	path := craftedArchive(t, key, m, []*tar.Header{{Name: "state", Mode: 0o600, Size: int64(len(payload))}}, [][]byte{payload}, nil)
	v, err := Verify(t.Context(), path, t.TempDir(), []age.Identity{key}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	// #nosec G703 -- The test controls this fixture path within its private workspace.
	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := Verify(t.Context(), path, t.TempDir(), []age.Identity{key}, Limits{}); err == nil {
		_ = v.Close()
		t.Fatal("accepted damaged final authentication tag")
	}
}

func TestArchiveDetectsChangedAndUnreadableSource(t *testing.T) {
	for _, change := range []string{"removed", "size", "content", "symlink", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "key")
			if err := writePrivate(file, []byte("original")); err != nil {
				t.Fatal(err)
			}
			entries, err := inventory(t.Context(), dir, Limits{}.defaults())
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "removed":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "size":
				if err := os.WriteFile(file, []byte("short"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "content":
				if err := os.WriteFile(file, []byte("modified"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("elsewhere", file); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				makeUnreadable(t, file)
				if _, err := inventory(t.Context(), dir, Limits{}.defaults()); err == nil {
					t.Fatal("inventoried unreadable file")
				}
			}
			if err := writeEntries(t.Context(), tar.NewWriter(io.Discard), dir, entries); err == nil {
				t.Fatal("missed changed source")
			}
		})
	}
	_, stage, _, manifest := archiveFixture(t)
	for _, bytesAvailable := range []int{0, 512} {
		if err := writeEntries(t.Context(), tar.NewWriter(&failingArchiveWriter{left: bytesAvailable}), stage, manifest.Entries); err == nil {
			t.Fatal("ignored archive write failure")
		}
	}
	if err := writeEntries(t.Context(), tar.NewWriter(io.Discard), "missing", manifest.Entries); err == nil {
		t.Fatal("accepted missing stage")
	}
}

func TestVerifyAndRestoreFilesystemFailures(t *testing.T) {
	archive, _, key, _ := archiveFixture(t)
	if _, err := Verify(t.Context(), archive, t.TempDir(), nil, Limits{}); err == nil {
		t.Fatal("accepted missing recovery identity")
	}
	if _, err := Verify(t.Context(), "missing", t.TempDir(), []age.Identity{key}, Limits{}); err == nil {
		t.Fatal("accepted missing archive")
	}
	if _, err := Verify(t.Context(), archive, filepath.Join(t.TempDir(), "missing"), []age.Identity{key}, Limits{}); err == nil {
		t.Fatal("accepted missing verification parent")
	}
	v, err := Verify(t.Context(), archive, t.TempDir(), []age.Identity{key}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(v.Close)
	if err := v.RestoreFiles(filepath.Join(t.TempDir(), "missing", "target")); err == nil {
		t.Fatal("restored through missing parent")
	}
	if err := v.RestoreFiles(strings.Repeat("x", 300)); err == nil {
		t.Fatal("restored with invalid filesystem name")
	}
	if err := copyVerified("missing", filepath.Join(t.TempDir(), "copy"), v.Manifest.Entries[0]); err == nil {
		t.Fatal("copied missing file")
	}
	if err := copyVerified(filepath.Join(v.Directory(), v.Manifest.Entries[0].Name), t.TempDir(), v.Manifest.Entries[0]); err == nil {
		t.Fatal("overwrote directory")
	}
	if err := syncDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("synced missing directory")
	}
}

func TestExtractionFailsBeforePublishingBadFiles(t *testing.T) {
	data := []byte("protected key")
	hash := sha256.Sum256(data)
	entry := Entry{Name: "keys/storage", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
	var raw bytes.Buffer
	w := tar.NewWriter(&raw)
	if err := w.WriteHeader(&tar.Header{Name: entry.Name, Size: entry.Size, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []string{"parent", "file", "cancelled", "truncated"} {
		t.Run(conflict, func(t *testing.T) {
			v := &Verified{directory: t.TempDir(), Manifest: Manifest{Entries: []Entry{entry}}}
			ctx := t.Context()
			input := raw.Bytes()
			switch conflict {
			case "parent":
				if err := writePrivate(filepath.Join(v.directory, "keys"), nil); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.Mkdir(filepath.Join(v.directory, "keys"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := writePrivate(filepath.Join(v.directory, entry.Name), nil); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "truncated":
				input = input[:515]
			}
			if err := extract(ctx, tar.NewReader(bytes.NewReader(input)), v); err == nil {
				t.Fatal("accepted failed extraction")
			}
		})
	}
}

func TestBoundedPrivateFileHelpers(t *testing.T) {
	dir := t.TempDir()
	if _, err := readPrivate(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("read missing file")
	}
	if _, err := readPrivate(filepath.Join(dir, "missing", "key")); err == nil {
		t.Fatal("read missing parent")
	}
	if _, err := readPrivate(dir); err == nil {
		t.Fatal("read directory as key")
	}
	if err := writePrivate(filepath.Join(dir, "missing", "key"), nil); err == nil {
		t.Fatal("created missing parent")
	}
	big := filepath.Join(dir, "oversize")
	if err := os.WriteFile(big, make([]byte, maxConfigFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(big); err == nil {
		t.Fatal("accepted oversized key file")
	}
	if err := readJSONFile(big, new(any)); err == nil {
		t.Fatal("accepted oversized configuration")
	}
	if err := readJSONFile(dir, new(any)); err == nil {
		t.Fatal("accepted directory configuration")
	}
	if err := writeJSONFile(filepath.Join(dir, "bad-json"), make(chan int)); err == nil {
		t.Fatal("accepted non-JSON metadata")
	}
	if err := writeJSONFile(filepath.Join(dir, "missing", "file"), nil); err == nil {
		t.Fatal("wrote through missing parent")
	}
	if err := writePrivate(filepath.Join(dir, "trailing-json"), []byte(`{} {}`)); err != nil {
		t.Fatal(err)
	}
	if err := readJSONFile(filepath.Join(dir, "trailing-json"), new(any)); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
