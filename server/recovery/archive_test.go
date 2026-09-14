package recovery

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

func archiveFixture(t *testing.T) (string, string, *age.X25519Identity, Manifest) {
	t.Helper()
	dir := t.TempDir()
	stage := filepath.Join(dir, "source")
	if err := os.MkdirAll(filepath.Join(stage, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(stage, "secrets", "storage"),
		bytes.Repeat([]byte("private-key-material"), 8000),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(stage, "database.jsonl"),
		[]byte("encrypted database rows\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "checkpoint.age")
	manifest, err := Create(
		t.Context(),
		destination,
		stage,
		Metadata{
			Backend:   "sqlite",
			PublicURL: "https://mdm.example.test",
			Revision:  "reviewed-commit",
		},
		[]age.Recipient{id.Recipient()},
		Limits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return destination, stage, id, manifest
}

func TestArchiveRoundTripAndOccupiedTarget(t *testing.T) {
	source, stage, identity, manifest := archiveFixture(t)
	parent := t.TempDir()
	v, err := Verify(t.Context(), source, parent, []age.Identity{identity}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.Manifest.Metadata != manifest.Metadata {
		t.Fatal("metadata changed")
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err := v.RestoreFiles(target); err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Entries {
		want, _ := os.ReadFile(filepath.Join(stage, entry.Name))
		got, err := os.ReadFile(filepath.Join(target, entry.Name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("restored bytes changed", entry.Name, err)
		}
		if err := privatefile.Check(filepath.Join(target, entry.Name)); err != nil {
			t.Fatal("private file permissions", err)
		}
	}
	if err := v.RestoreFiles(target); !errors.Is(err, ErrOccupied) {
		t.Fatal(err)
	}
	if _, err := Create(
		t.Context(),
		source,
		stage,
		manifest.Metadata,
		[]age.Recipient{identity.Recipient()},
		Limits{},
	); !errors.Is(
		err,
		ErrOccupied,
	) {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.RestoreFiles(filepath.Join(parent, "closed")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("verification left private staging")
	}
}

func TestArchiveRejectsDamagedCiphertextAndWrongIdentity(t *testing.T) {
	source, _, identity, _ := archiveFixture(t)
	original, _ := os.ReadFile(source)
	wrong, _ := age.GenerateX25519Identity()
	for _, name := range []string{"wrong identity", "header", "last chunk", "truncate", "appended data"} {
		t.Run(name, func(t *testing.T) {
			data := bytes.Clone(original)
			key := identity
			switch name {
			case "wrong identity":
				key = wrong
			case "header":
				data[0] ^= 1
			case "last chunk":
				data[len(data)-1] ^= 1
			case "truncate":
				data = data[:len(data)-16]
			case "appended data":
				data = append(data, 1)
			}
			dir := t.TempDir()
			file := filepath.Join(dir, "damaged.age")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Verify(t.Context(), file, dir, []age.Identity{key}, Limits{}); err == nil {
				t.Fatal("damaged checkpoint verified")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatal("failed verification retained plaintext")
			}
		})
	}
}

func TestArchiveRejectsUnsafeSourcesAndLimits(t *testing.T) {
	source, stage, identity, manifest := archiveFixture(t)
	recipients := []age.Recipient{identity.Recipient()}
	for _, limit := range []Limits{{Bytes: 10}, {Files: 1}} {
		if _, err := Create(
			t.Context(),
			source+"new",
			stage,
			manifest.Metadata,
			recipients,
			limit,
		); !errors.Is(
			err,
			ErrLimit,
		) {
			t.Fatal(err)
		}
		if _, err := Verify(
			t.Context(),
			source,
			t.TempDir(),
			[]age.Identity{identity},
			limit,
		); !errors.Is(
			err,
			ErrLimit,
		) {
			t.Fatal(err)
		}
	}
	if _, err := Create(
		t.Context(),
		source+"new",
		t.TempDir(),
		manifest.Metadata,
		recipients,
		Limits{},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal(err)
	}
	for _, name := range []string{"symlink", "manifest.json", "bad name"} {
		dir := t.TempDir()
		if name == "symlink" {
			_ = os.Symlink(source, filepath.Join(dir, name))
		} else {
			_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
		}
		if _, err := Create(
			t.Context(),
			source+"new",
			dir,
			manifest.Metadata,
			recipients,
			Limits{},
		); !errors.Is(
			err,
			ErrInvalid,
		) {
			t.Fatal(name, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Create(
		ctx,
		source+"new",
		stage,
		manifest.Metadata,
		recipients,
		Limits{},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
	if _, err := Verify(
		ctx,
		source,
		t.TempDir(),
		[]age.Identity{identity},
		Limits{},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
	if _, err := Create(
		t.Context(),
		source+"new",
		stage,
		manifest.Metadata,
		nil,
		Limits{},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal(err)
	}
	if _, err := Verify(
		t.Context(),
		source,
		t.TempDir(),
		nil,
		Limits{},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal(err)
	}
}

func craftedArchive(
	t *testing.T,
	identity *age.X25519Identity,
	manifest any,
	headers []*tar.Header,
	contents [][]byte,
	trailing []byte,
) string {
	t.Helper()
	var ciphertext bytes.Buffer
	encrypted, err := age.Encrypt(&ciphertext, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(encrypted)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.WriteHeader(
		&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(raw))},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(raw); err != nil {
		t.Fatal(err)
	}
	for i, h := range headers {
		if err := archive.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(contents[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := encrypted.Write(trailing); err != nil {
		t.Fatal(err)
	}
	if err := encrypted.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "crafted.age")
	if err := os.WriteFile(file, ciphertext.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestAuthenticatedArchiveStillRequiresSafeCompleteManifest(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	data := []byte("x")
	hash := sha256.Sum256(data)
	entry := Entry{Name: "data", Size: 1, SHA256: hex.EncodeToString(hash[:])}
	metadata := Metadata{
		Backend:   "sqlite",
		PublicURL: "https://mdm.example.test",
		Revision:  "a",
		CreatedAt: time.Now().UTC(),
	}
	for _, name := range []string{"version", "backend", "metadata", "duplicate", "traversal", "absolute", "bad hash", "bad size", "missing entry", "unexpected entry", "link", "content mismatch", "trailing plaintext", "unknown field"} {
		t.Run(name, func(t *testing.T) {
			m := Manifest{Version: 1, Metadata: metadata, Entries: []Entry{entry}}
			headers := []*tar.Header{{Name: "data", Mode: 0o600, Size: 1}}
			contents := [][]byte{data}
			var trailing []byte
			switch name {
			case "version":
				m.Version = 2
			case "backend":
				m.Metadata.Backend = "unsupported"
			case "metadata":
				m.Metadata.PublicURL = ""
			case "duplicate":
				m.Entries = append(m.Entries, entry)
			case "traversal":
				m.Entries[0].Name = "../escape"
			case "absolute":
				m.Entries[0].Name = "/escape"
			case "bad hash":
				m.Entries[0].SHA256 = "bad"
			case "bad size":
				m.Entries[0].Size = -1
			case "missing entry":
				headers, contents = nil, nil
			case "unexpected entry":
				headers = append(headers, &tar.Header{Name: "extra", Size: 1})
				contents = append(contents, data)
			case "link":
				headers[0] = &tar.Header{
					Name:     "data",
					Typeflag: tar.TypeSymlink,
					Linkname: "/outside",
				}
				contents[0] = nil
			case "content mismatch":
				contents[0] = []byte("y")
			case "trailing plaintext":
				trailing = []byte("ignored content")
			}
			var manifest any = m
			if name == "unknown field" {
				manifest = map[string]any{"unexpected": true}
			}
			file := craftedArchive(t, id, manifest, headers, contents, trailing)
			if _, err := Verify(
				t.Context(),
				file,
				t.TempDir(),
				[]age.Identity{id},
				Limits{},
			); err == nil {
				t.Fatal("unsafe authenticated archive accepted")
			}
		})
	}
}

func TestRestoreRechecksIsolatedFiles(t *testing.T) {
	source, _, identity, _ := archiveFixture(t)
	v, err := Verify(t.Context(), source, t.TempDir(), []age.Identity{identity}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := os.WriteFile(
		filepath.Join(v.Directory(), "database.jsonl"),
		[]byte("changed"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := v.RestoreFiles(filepath.Join(t.TempDir(), "restored")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func FuzzArchiveNames(f *testing.F) {
	for _, name := range []string{"secrets/key", "../secret", "/absolute", "a\\b", "a/./b", "a//b", "a\x00b"} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if validName(name) &&
			(filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.Contains(name, "\x00")) {
			t.Fatal("unsafe archive path accepted")
		}
	})
}
