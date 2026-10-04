package applications_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"howett.net/plist"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/filesystem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// failedReader models a source or output stream that fails before completion.
type failedReader struct{}

// Read supplies a deterministic stream failure for cancellation and cleanup tests.
func (failedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// Write models a disconnected export consumer.
func (failedReader) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestMetadataValidationAndUnsupportedPreferences exercises field boundaries and
// confirms every stored installer preference is either honored or explicitly rejected.
func TestMetadataValidationAndUnsupportedPreferences(t *testing.T) {
	cases := []struct {
		name   string
		change func(*applications.Metadata)
	}{
		{"empty name", func(m *applications.Metadata) { m.PackageName = " " }},
		{"long name", func(m *applications.Metadata) { m.PackageName = strings.Repeat("a", 256) }},
		{"bad utf8", func(m *applications.Metadata) { m.PackageName = string([]byte{255}) }},
		{"filename", func(m *applications.Metadata) { m.FileName = "../escape.pkg" }},
		{"bundle", func(m *applications.Metadata) { m.BundleID = "has space" }},
		{"version", func(m *applications.Metadata) { m.Version = "line\nfeed" }},
		{"category control", func(m *applications.Metadata) { m.CategoryID = ptr("a\x7fb") }},
		{"notes bound", func(m *applications.Metadata) { m.Notes = ptr(strings.Repeat("a", 16385)) }},
		{"negative priority", func(m *applications.Metadata) { m.Priority = ptr(-1) }},
		{"parent", func(m *applications.Metadata) { m.ParentPackageID = ptr("a/b") }},
		{"path traversal", func(m *applications.Metadata) { m.BasePath = ptr("../elsewhere") }},
		{"format", func(m *applications.Metadata) { m.Format = ptr("dmg") }},
		{"language", func(m *applications.Metadata) { m.InstallLanguage = ptr("bad language") }},
		{"os wildcards", func(m *applications.Metadata) { m.OSRequirements = ptr("x.15") }},
		{"os empty part", func(m *applications.Metadata) { m.OSRequirements = ptr("15..2") }},
		{"os overflow", func(m *applications.Metadata) { m.OSRequirements = ptr("99999999999999999999999") }},
		{"os too many parts", func(m *applications.Metadata) { m.OSRequirements = ptr("15.1.2.3") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := metadata()
			tc.change(&m)
			if err := applications.ValidateMetadata(m); !errors.Is(err, applications.ErrInvalid) {
				t.Fatal(err)
			}
			if err := m.CheckNativeDelivery("15.8"); err == nil {
				t.Fatal("invalid metadata reached delivery")
			}
		})
	}
	m := metadata()
	v := reflect.ValueOf(&m).Elem()
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		if typ.Field(i).Type == reflect.TypeFor[*bool]() {
			field := v.Field(i)
			field.Set(reflect.ValueOf(ptr(true)))
			options := m.UnsupportedOptions()
			if len(options) != 1 {
				t.Errorf("%s did not request exactly one unsupported behavior: %v", typ.Field(i).Name, options)
			}
			field.Set(reflect.Zero(field.Type()))
		}
	}
	for _, field := range []string{"SerialNumber", "BasePath", "InstallLanguage", "SelfHealingAction"} {
		m = metadata()
		reflect.ValueOf(&m).Elem().FieldByName(field).Set(reflect.ValueOf(ptr("requested")))
		if len(m.UnsupportedOptions()) != 1 {
			t.Errorf("%s lost behavior", field)
		}
	}
	m = metadata()
	m.BundleID = ""
	if err := m.CheckNativeDelivery("15.8"); err == nil {
		t.Fatal("missing delivery identity")
	}
	m = metadata()
	if _, err := m.SupportsOS("fifteen"); err == nil {
		t.Fatal("invalid actual OS")
	}
	m.OSRequirements = ptr("15,,26")
	if _, err := m.SupportsOS("15"); err == nil {
		t.Fatal("invalid stored requirement")
	}
	m.OSRequirements = nil
	if ok, err := m.SupportsOS("15"); err != nil || !ok {
		t.Fatal("unrestricted version", err)
	}
	m = metadata()
	m.Notes = ptr("tab\tline\r\n")
	if err := applications.ValidateMetadata(m); err != nil {
		t.Fatal(err)
	}
	if _, err := applications.DecodeMetadata(failedReader{}); err == nil {
		t.Fatal("reader error discarded")
	}
	if _, err := applications.DecodeMetadata(strings.NewReader(strings.Repeat(" ", 128<<10+1))); !errors.Is(err, applications.ErrTooLarge) {
		t.Fatal(err)
	}
}

// TestDigestAlgorithms checks every supported algorithm and refuses ambiguity,
// malformed encodings and mismatched expectations without relying on provider hashes.
func TestDigestAlgorithms(t *testing.T) {
	actual := applications.Digests{MD5: strings.Repeat("a", 32), SHA256: strings.Repeat("b", 64), SHA512: strings.Repeat("c", 128), SHA3512: strings.Repeat("d", 128)}
	for _, tc := range []struct{ kind, hash string }{{"MD5", actual.MD5}, {"SHA256", actual.SHA256}, {"SHA512", actual.SHA512}, {"SHA3-512", actual.SHA3512}} {
		expected := applications.Digests{HashType: tc.kind, HashValue: strings.ToUpper(tc.hash)}
		if err := expected.Match(actual); err != nil {
			t.Fatal(tc.kind, err)
		}
	}
	for _, d := range []applications.Digests{
		{MD5: "x"},
		{SHA256: "x"},
		{SHA3512: "x"},
		{SHA512: "x"},
		{HashType: "SHA3"},
		{HashType: "SHA256", HashValue: "short"},
		{SHA256: actual.SHA256, HashType: "SHA256", HashValue: strings.Repeat("c", 64)},
	} {
		if err := d.Match(actual); !errors.Is(err, applications.ErrInvalid) {
			t.Fatalf("accepted invalid digest %+v: %v", d, err)
		}
	}
	if err := (applications.Digests{SHA3512: actual.SHA512}).Match(actual); !errors.Is(err, applications.ErrIntegrity) {
		t.Fatal("SHA512 confused with SHA3-512", err)
	}
}

// TestCataloguePagingHistoryAndExports exercises page cursors, explicit notes,
// measured export fields, spreadsheet escaping and failing output streams.
func TestCataloguePagingHistoryAndExports(t *testing.T) {
	m, _ := manager(t, applications.VerificationPolicy{})
	ctx := t.Context()
	var records []applications.Record
	for i := 0; i < 3; i++ {
		meta := metadata()
		meta.PackageName = fmt.Sprintf("Package %d", i)
		r, err := m.Create(ctx, meta)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	var ids []string
	cursor := ""
	for {
		page, err := m.List(ctx, paging.Page{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Items {
			ids = append(ids, r.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(ids) != 3 || ids[0] >= ids[1] || ids[1] >= ids[2] {
		t.Fatal(ids)
	}
	if _, err := m.List(ctx, paging.Page{Cursor: "../escape"}); err == nil {
		t.Fatal("invalid cursor")
	}
	r := records[0]
	noted, err := m.AddHistoryNote(ctx, r.ID, r.Revision, "=formula")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AddHistoryNote(ctx, r.ID, r.Revision, "stale"); !errors.Is(err, applications.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = m.AddHistoryNote(ctx, r.ID, noted.Revision, " "); err == nil {
		t.Fatal("blank note")
	}
	history, err := m.History(ctx, r.ID, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"zero", "0"} {
		if _, err = m.History(ctx, r.ID, paging.Page{Cursor: cursor}); err == nil {
			t.Fatal("invalid history cursor")
		}
	}
	for _, format := range []func(io.Writer, []applications.HistoryEntry) error{applications.ExportHistoryJSON, applications.ExportHistoryCSV} {
		var b bytes.Buffer
		if err = format(&b, history.Items); err != nil || b.Len() == 0 {
			t.Fatal(err)
		}
		if err = format(failedReader{}, history.Items); err == nil {
			t.Fatal("history export hid disconnected client")
		}
	}
	meta := metadata()
	meta.PackageName = "=formula"
	meta.FillUserTemplate = ptr(false)
	export := applications.Record{ID: "export", Metadata: meta, CloudTransferStatus: "READY", Content: &applications.Content{Size: 19, Format: "flat-pkg", Digests: applications.Digests{SHA256: strings.Repeat("a", 64)}}, Manifest: &applications.Manifest{FileName: "source.plist", Data: []byte("plist")}}
	view := export.Resource()
	if view.Size == nil || *view.Size != 19 || view.Format != "flat-pkg" || view.ManifestFileName != "source.plist" {
		t.Fatal(view)
	}
	var b bytes.Buffer
	if err = applications.ExportCSV(&b, []applications.Record{export}, nil); err != nil || !strings.Contains(b.String(), "'=formula") {
		t.Fatal(b.String(), err)
	}
	for _, fields := range [][]string{{"unknown"}, {"id", "id"}} {
		if err = applications.ExportCSV(io.Discard, nil, fields); err == nil {
			t.Fatal("invalid export fields")
		}
	}
	if err = applications.ExportJSON(failedReader{}, records); err == nil {
		t.Fatal("JSON output error ignored")
	}
	if err = applications.ExportCSV(failedReader{}, records, nil); err == nil {
		t.Fatal("CSV output error ignored")
	}
	records = append(records, records[0])
	records[0].Metadata.Priority = ptr(30)
	records[1].Metadata.Priority = ptr(0)
	ordered := applications.OrderForDelivery(records)
	if *ordered[0].Metadata.Priority != 0 || *ordered[len(ordered)-1].Metadata.Priority != 30 || *records[0].Metadata.Priority != 30 {
		t.Fatal("priority ordering mutated input")
	}
	if _, err = m.DeleteMany(ctx, nil); err == nil {
		t.Fatal("empty deletion batch")
	}
	if _, err = m.DeleteMany(ctx, []applications.DeleteRequest{{ID: r.ID, Revision: r.Revision}, {ID: r.ID, Revision: r.Revision}}); err == nil {
		t.Fatal("duplicate deletion batch")
	}
}

// TestManifestValidation checks hostile and inconsistent manifests before storage,
// and confirms removal preserves the verified package revision.
func TestManifestValidation(t *testing.T) {
	payload, policy := fixture(t)
	m, _ := manager(t, policy)
	ctx := t.Context()
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AssignManifest(ctx, r.ID, r.Revision, "file.plist", []byte("plist")); err == nil {
		t.Fatal("manifest without content")
	}
	r, err = m.Upload(ctx, r.ID, r.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	c := *r.Content
	manifest, err := applications.BuildManifest(c, "https://example.test/package")
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if _, err = plist.Unmarshal(manifest, &original); err != nil {
		t.Fatal(err)
	}
	mutate := func(change func(map[string]any)) []byte {
		var object map[string]any
		encoded, e := json.Marshal(original)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(encoded, &object); e != nil {
			t.Fatal(e)
		}
		change(object)
		b, e := plist.Marshal(object, plist.XMLFormat)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	cases := [][]byte{nil, []byte("invalid"), bytes.Repeat([]byte("x"), 1<<20+1)}
	cases = append(cases,
		mutate(func(m map[string]any) { m["unknown"] = true }),
		mutate(func(m map[string]any) { m["items"] = []any{} }),
		mutate(func(m map[string]any) { m["items"] = []any{"bad item"} }),
		mutate(func(m map[string]any) {
			m["items"] = []any{map[string]any{"assets": []any{}, "metadata": map[string]any{}}}
		}),
	)
	for _, b := range cases {
		if err = applications.ValidateManifest(b, c); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	for _, replacement := range []struct{ from, to string }{{"software-package", "unknown-package"}, {"https://example.test/package", "http://example.test/package"}, {c.Metadata.BundleID, "com.example.wrong"}, {"<key>sha256s</key>", "<key>sha256</key>"}} {
		b := bytes.ReplaceAll(manifest, []byte(replacement.from), []byte(replacement.to))
		if err = applications.ValidateManifest(b, c); err == nil {
			t.Fatal("accepted", replacement)
		}
	}
	for _, change := range []func(*applications.Content){func(c *applications.Content) { c.Size = 0 }, func(c *applications.Content) { c.SHA256 = "" }, func(c *applications.Content) { c.Metadata.BundleID = "" }} {
		bad := c
		change(&bad)
		if _, err = applications.BuildManifest(bad, "https://example.test/package"); err == nil {
			t.Fatal("invalid manifest source")
		}
	}
	if _, err = applications.BuildManifest(c, "http://example.test/package"); err == nil {
		t.Fatal("insecure URL")
	}
	assigned, err := m.AssignManifest(ctx, r.ID, r.Revision, "manifest.plist", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.DeleteManifest(ctx, r.ID, r.Revision); !errors.Is(err, applications.ErrConflict) {
		t.Fatal(err)
	}
	cleared, err := m.DeleteManifest(ctx, r.ID, assigned.Revision)
	if err != nil || cleared.Manifest != nil || cleared.Content == nil {
		t.Fatal(err)
	}
}

// TestFilesystemConfinementAndCancellation checks real filesystem behavior,
// including traversal, symlink escape, cancellation, wrong sizes and closed roots.
func TestFilesystemConfinementAndCancellation(t *testing.T) {
	root := t.TempDir()
	store, err := filesystem.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	data := []byte("package source")
	if err = store.Put(ctx, "source.pkg", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "source.pkg", bytes.NewReader(data), int64(len(data))); !errors.Is(err, applications.ErrConflict) {
		t.Fatal(err)
	}
	for _, key := range []string{"", "..", "a/../b", "/absolute", "back\\slash", "has space"} {
		if err = store.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatal("unsafe key", key)
		}
		if _, err = store.Open(ctx, key, 0, -1); err == nil {
			t.Fatal("unsafe open", key)
		}
		if err = store.Delete(ctx, key); err == nil {
			t.Fatal("unsafe delete", key)
		}
	}
	for _, span := range [][2]int64{{-1, 1}, {0, -2}, {math.MaxInt64, 2}, {99, 1}, {1, 99}} {
		if _, err = store.Open(ctx, "source.pkg", span[0], span[1]); err == nil {
			t.Fatal("invalid range", span)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = store.Open(canceled, "source.pkg", 0, -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = store.Put(canceled, "new.pkg", bytes.NewReader(data), int64(len(data))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = store.Delete(canceled, "source.pkg"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(ctx, "directory", 0, -1); err == nil {
		t.Fatal("directory accepted")
	}
	if runtime.GOOS != "windows" {
		if err = os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
			t.Fatal(err)
		}
		if err = store.Put(ctx, "escape/outside.pkg", bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatal("symlink escape")
		}
	}
	if err = store.Put(ctx, "bad-size.pkg", bytes.NewReader(data), 1); !errors.Is(err, applications.ErrIntegrity) {
		t.Fatal(err)
	}
	if err = store.Delete(ctx, "missing.pkg"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(ctx, "source.pkg", 0, -1); err == nil {
		t.Fatal("closed root read")
	}
	if err = store.Put(ctx, "after-close.pkg", bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("closed root write")
	}
	if _, err = filesystem.New(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing root")
	}
	// Import uses a separate source root and passes through the complete verifier.
	payload, policy := fixture(t)
	m, source := manager(t, policy)
	if err = source.Put(ctx, "source.pkg", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Import(ctx, m, r.ID, r.Revision, "local", "source.pkg"); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Import(ctx, m, r.ID, r.Revision, "local", "../source.pkg"); err == nil {
		t.Fatal("unsafe import")
	}
	if _, err = source.Import(ctx, m, r.ID, r.Revision, "local", "missing.pkg"); err == nil {
		t.Fatal("missing import")
	}
}

// TestManagerConfiguration rejects invalid policies and bounds before I/O starts.
func TestManagerConfiguration(t *testing.T) {
	_, store := manager(t, applications.VerificationPolicy{})
	for _, cfg := range []applications.Config{
		{},
		{State: state.NewMemory()},
		{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"../bad": store}},
		{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"nil": nil}},
		{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"ok": store}, MaxBytes: math.MaxInt64},
		{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"ok": store}, Verification: applications.VerificationPolicy{AllowPrivateSigner: true}},
	} {
		if _, err := applications.New(cfg); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 65), "path/id"} {
		if applications.ValidID(id) {
			t.Fatal("invalid ID", id)
		}
	}
}

// TestInvalidCatalogueRequests prevents malformed IDs, stale metadata and absent
// records from creating accidental state under alternate keys.
func TestInvalidCatalogueRequests(t *testing.T) {
	m, _ := manager(t, applications.VerificationPolicy{})
	ctx := t.Context()
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	requests := []func() error{
		func() error { _, e := m.Create(ctx, applications.Metadata{}); return e },
		func() error { _, e := m.Get(ctx, "../bad"); return e },
		func() error { _, e := m.Get(ctx, "missing"); return e },
		func() error { _, e := m.Update(ctx, r.ID, "", metadata()); return e },
		func() error { _, e := m.Update(ctx, r.ID, r.Revision, applications.Metadata{}); return e },
		func() error { _, e := m.Revision(ctx, r.ID, "../bad"); return e },
		func() error { return m.Delete(ctx, r.ID, "") },
		func() error { _, e := m.History(ctx, "../bad", paging.Page{}); return e },
		func() error { _, e := m.AssignManifest(ctx, r.ID, "", "x.plist", nil); return e },
		func() error { _, e := m.DeleteManifest(ctx, r.ID, ""); return e },
		func() error {
			meta := metadata()
			meta.ParentPackageID = ptr("missing")
			_, e := m.Create(ctx, meta)
			return e
		},
	}
	for i, run := range requests {
		if e := run(); e == nil {
			t.Errorf("invalid request %d accepted", i)
		}
	}
	meta := metadata()
	if err = meta.CheckNativeDelivery("bad OS"); err == nil {
		t.Fatal("invalid device OS accepted")
	}
	ordered := applications.OrderForDelivery([]applications.Record{{ID: "b"}, {ID: "a"}, {ID: "a"}})
	if ordered[0].ID != "a" || ordered[2].ID != "b" {
		t.Fatal("unstable tie ordering")
	}
}

// TestUploadInputFailures checks limits, sources, cancellation and integrity pins
// before content can reach a remote storage backend.
func TestUploadInputFailures(t *testing.T) {
	ctx := t.Context()
	m, _ := manager(t, applications.VerificationPolicy{})
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		backend string
		source  applications.Source
		reader  io.Reader
		digest  string
	}{
		{"unknown", applications.Source{Kind: "upload"}, strings.NewReader("x"), ""},
		{"local", applications.Source{Kind: "upload"}, nil, ""},
		{"local", applications.Source{Kind: "ftp"}, strings.NewReader("x"), ""},
		{"local", applications.Source{Kind: "https", Location: "http://example.test/pkg"}, strings.NewReader("x"), ""},
		{"local", applications.Source{Kind: "https", Location: "https://user:pass@example.test/pkg"}, strings.NewReader("x"), ""}, // #nosec G101 -- Deliberately rejected URL credentials in an authored validation fixture.
		{"local", applications.Source{Kind: "file", Location: strings.Repeat("x", 4097)}, strings.NewReader("x"), ""},
		{"local", applications.Source{Kind: "upload"}, strings.NewReader("x"), "invalid"},
		{"local", applications.Source{Kind: "upload"}, failedReader{}, ""},
		{"local", applications.Source{Kind: "upload"}, strings.NewReader(""), ""},
	} {
		if _, e := m.Upload(ctx, r.ID, r.Revision, tc.backend, tc.source, tc.reader, tc.digest); e == nil {
			t.Fatal("invalid upload accepted")
		}
	}
	_, disk := manager(t, applications.VerificationPolicy{})
	for _, cfg := range []applications.Config{
		{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"local": disk}, ScratchDir: filepath.Join(t.TempDir(), "missing")},
		{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"local": disk}, ScratchDir: t.TempDir(), MaxBytes: 1},
	} {
		candidate, e := applications.New(cfg)
		if e != nil {
			t.Fatal(e)
		}
		entry, e := candidate.Create(ctx, metadata())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = candidate.Upload(ctx, entry.ID, entry.Revision, "local", applications.Source{Kind: "upload"}, strings.NewReader("oversized"), ""); e == nil {
			t.Fatal("upload ignored staging failure or byte limit")
		}
	}
	payload, policy := fixture(t)
	candidate, _ := manager(t, policy)
	meta := metadata()
	meta.SHA256 = strings.Repeat("0", 64)
	entry, e := candidate.Create(ctx, meta)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = candidate.Upload(ctx, entry.ID, entry.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), ""); !errors.Is(e, applications.ErrIntegrity) {
		t.Fatal(e)
	}
}

// TestManifestValueTypes rejects syntactically valid plists carrying wrong value
// types, unsupported metadata or inconsistent whole-file digests.
func TestManifestValueTypes(t *testing.T) {
	c := applications.Content{Metadata: metadata(), Digests: applications.Digests{SHA256: strings.Repeat("a", 64), MD5: strings.Repeat("b", 32)}}
	makeManifest := func(assetExtra, metaExtra map[string]any) []byte {
		asset := map[string]any{"kind": "software-package", "url": "https://example.test/pkg", "sha256": c.SHA256}
		meta := map[string]any{"bundle-identifier": c.Metadata.BundleID, "bundle-version": c.Metadata.Version, "kind": "software", "title": c.Metadata.PackageName}
		for k, v := range assetExtra {
			asset[k] = v
		}
		for k, v := range metaExtra {
			meta[k] = v
		}
		b, err := plist.Marshal(map[string]any{"items": []any{map[string]any{"assets": []any{asset}, "metadata": meta}}}, plist.XMLFormat)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, extra := range []map[string]any{{"url": true}, {"sha256": false}, {"sha256": ""}, {"md5": true}, {"md5": ""}} {
		if err := applications.ValidateManifest(makeManifest(extra, nil), c); err == nil {
			t.Fatal("invalid asset accepted", extra)
		}
	}
	for _, extra := range []map[string]any{{"unknown": true}, {"subtitle": true}, {"subtitle": strings.Repeat("a", 256)}} {
		if err := applications.ValidateManifest(makeManifest(nil, extra), c); err == nil {
			t.Fatal("invalid metadata accepted", extra)
		}
	}
	if err := applications.ValidateManifest(makeManifest(nil, map[string]any{"subtitle": "Publisher"}), c); err != nil {
		t.Fatal(err)
	}
}

// TestVerificationBounds checks explicit private trust and decompression limits.
func TestVerificationBounds(t *testing.T) {
	payload, policy := fixture(t)
	invalid := applications.VerificationPolicy{AllowPrivateSigner: true}
	if _, err := applications.VerifyPackage(t.Context(), bytes.NewReader(payload), int64(len(payload)), invalid); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal(err)
	}
	policy.MaxExpandedBytes = 1
	if _, err := applications.VerifyPackage(t.Context(), bytes.NewReader(payload), int64(len(payload)), policy); !errors.Is(err, applications.ErrTooLarge) {
		t.Fatal(err)
	}
	// An unsigned, otherwise valid installer must still fail signature verification.
	var unsigned bytes.Buffer
	if _, err := flatpkg.BuildComponent(flatpkg.ComponentOptions{Identifier: "com.example.unsigned", Version: "1", NoPayload: true, TempDir: t.TempDir()}, &unsigned); err != nil {
		t.Fatal(err)
	}
	if _, err := applications.VerifyPackage(t.Context(), bytes.NewReader(unsigned.Bytes()), int64(unsigned.Len()), applications.VerificationPolicy{}); !errors.Is(err, applications.ErrIntegrity) {
		t.Fatal(err)
	}
}

// cancelInput cancels an upload after its first successful read.
type cancelInput struct {
	io.Reader
	cancel context.CancelFunc
}

// Read cancels between chunks without turning cancellation into a successful EOF.
func (r cancelInput) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.cancel()
	return n, err
}

// TestPayloadTamperingAndStreamingCancellation checks that a signed table of
// contents cannot conceal modified archive data or an interrupted source stream.
func TestPayloadTamperingAndStreamingCancellation(t *testing.T) {
	payload, policy := fixture(t)
	modified := bytes.Clone(payload)
	modified[len(modified)-1] ^= 0xff
	if _, err := applications.VerifyPackage(t.Context(), bytes.NewReader(modified), int64(len(modified)), policy); err == nil {
		t.Fatal("signed archive accepted modified content")
	}
	m, _ := manager(t, policy)
	ctx := t.Context()
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err = m.Upload(canceled, r.ID, r.Revision, "local", applications.Source{Kind: "upload"}, cancelInput{Reader: bytes.NewReader(payload), cancel: cancel}, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	current, err := m.Get(ctx, r.ID)
	if err != nil || current.Content != nil {
		t.Fatal("canceled upload published", err)
	}
	uploaded, err := m.Upload(ctx, r.ID, r.Revision, "local", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := applications.BuildManifest(*uploaded.Content, "https://example.test/package")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AssignManifest(ctx, r.ID, r.Revision, "manifest.plist", manifest); !errors.Is(err, applications.ErrConflict) {
		t.Fatal("stale manifest assignment", err)
	}
}

// TestLargeCatalogueParentChecks verifies relationships across page boundaries
// and rejects pathological ancestry rather than traversing it without a bound.
func TestLargeCatalogueParentChecks(t *testing.T) {
	persistent := state.NewMemory()
	backend := &memoryBlobs{objects: map[string][]byte{}}
	ctx := t.Context()
	m, err := applications.New(applications.Config{State: persistent, Backends: map[string]applications.BlobStore{"test": backend}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	if err = persistent.Update(ctx, []string{"seed"}, func(tx state.Tx) error {
		for i := 0; i < 1001; i++ {
			id := fmt.Sprintf("entry-%04d", i)
			meta := metadata()
			row := applications.Record{ID: id, Revision: "revision", Metadata: meta}
			b, e := json.Marshal(row)
			if e != nil {
				return e
			}
			if e = tx.Put(ctx, state.Record{Key: "applications/records/" + id, Value: b}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = m.Delete(ctx, root.ID, root.Revision); err != nil {
		t.Fatal("paged child scan", err)
	}
	if err = persistent.Update(ctx, []string{"seed"}, func(tx state.Tx) error {
		for i := 0; i < 1001; i++ {
			id := fmt.Sprintf("entry-%04d", i)
			meta := metadata()
			if i < 1000 {
				meta.ParentPackageID = ptr(fmt.Sprintf("entry-%04d", i+1))
			}
			row := applications.Record{ID: id, Revision: "revision", Metadata: meta}
			b, e := json.Marshal(row)
			if e != nil {
				return e
			}
			if e = tx.Put(ctx, state.Record{Key: "applications/records/" + id, Value: b}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	meta := metadata()
	meta.ParentPackageID = ptr("entry-0000")
	if _, err = m.Create(ctx, meta); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal("unbounded parent chain", err)
	}
}
