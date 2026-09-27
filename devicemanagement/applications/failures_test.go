package applications_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var errPersistence = errors.New("injected persistence failure")

// faultState injects a failure at each persistence boundary in turn, including
// reads and writes inside a transaction, while preserving the real atomic store.
type faultState struct {
	state.Store
	at, calls int
	failed    bool
}

// step fails exactly one requested persistence operation.
func (s *faultState) step() error {
	s.calls++
	if s.calls == s.at {
		s.failed = true
		return errPersistence
	}
	return nil
}

// Get injects a read failure before calling the real store.
func (s *faultState) Get(ctx context.Context, key string) (state.Record, error) {
	if err := s.step(); err != nil {
		return state.Record{}, err
	}
	return s.Store.Get(ctx, key)
}

// List injects an enumeration failure before calling the real store.
func (s *faultState) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if err := s.step(); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, prefix, after, limit)
}

// Update keeps the real transaction while wrapping each operation inside it.
func (s *faultState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if err := s.step(); err != nil {
		return err
	}
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(faultTx{Tx: tx, parent: s}) })
}

type faultTx struct {
	state.Tx
	parent *faultState
}

// Get injects a transactional read failure.
func (tx faultTx) Get(ctx context.Context, key string) (state.Record, error) {
	if err := tx.parent.step(); err != nil {
		return state.Record{}, err
	}
	return tx.Tx.Get(ctx, key)
}

// List injects a transactional enumeration failure.
func (tx faultTx) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if err := tx.parent.step(); err != nil {
		return nil, err
	}
	return tx.Tx.List(ctx, prefix, after, limit)
}

// Put injects a write failure to exercise transaction rollback.
func (tx faultTx) Put(ctx context.Context, r state.Record) error {
	if err := tx.parent.step(); err != nil {
		return err
	}
	return tx.Tx.Put(ctx, r)
}

// Delete injects a delete failure to exercise retryable cleanup.
func (tx faultTx) Delete(ctx context.Context, key string) error {
	if err := tx.parent.step(); err != nil {
		return err
	}
	return tx.Tx.Delete(ctx, key)
}

// memoryBlobs is a deterministic private storage fixture with independent bytes.
type memoryBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
}

// Put copies source bytes and rejects object replacement.
func (s *memoryBlobs) Put(_ context.Context, key string, r io.ReadSeeker, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; ok {
		return applications.ErrConflict
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return applications.ErrIntegrity
	}
	s.objects[key] = data
	return nil
}

// Open returns independent bytes so readers cannot mutate stored content.
func (s *memoryBlobs) Open(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, applications.ErrNotFound
	}
	end := int64(len(data))
	if length >= 0 {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data[offset:end]))), nil
}

// Delete supports retries after an object was already removed.
func (s *memoryBlobs) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// TestPersistenceFailures exercises every state boundary of each public catalogue
// operation. Failed atomic mutations roll back; interrupted deletion remains retryable.
func TestPersistenceFailures(t *testing.T) {
	ctx := t.Context()
	payload, policy := fixture(t)
	base := state.NewMemory()
	blobs := &memoryBlobs{objects: map[string][]byte{}}
	cfg := applications.Config{State: base, Backends: map[string]applications.BlobStore{"test": blobs}, ScratchDir: t.TempDir(), Verification: policy}
	initial, err := applications.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := initial.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	r, err = initial.Upload(ctx, r.ID, r.Revision, "test", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := applications.BuildManifest(*r.Content, "https://example.test/package")
	if err != nil {
		t.Fatal(err)
	}
	r, err = initial.AssignManifest(ctx, r.ID, r.Revision, "package.plist", manifest)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := base.List(ctx, "applications/", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(*applications.Manager) error{
		"create": func(m *applications.Manager) error { _, e := m.Create(ctx, metadata()); return e },
		"get":    func(m *applications.Manager) error { _, e := m.Get(ctx, r.ID); return e },
		"list":   func(m *applications.Manager) error { _, e := m.List(ctx, paging.Page{}); return e },
		"update": func(m *applications.Manager) error {
			meta := metadata()
			meta.Notes = ptr("changed")
			_, e := m.Update(ctx, r.ID, r.Revision, meta)
			return e
		},
		"revision": func(m *applications.Manager) error { _, e := m.Revision(ctx, r.ID, r.Content.Revision); return e },
		"delete":   func(m *applications.Manager) error { return m.Delete(ctx, r.ID, r.Revision) },
		"upload": func(m *applications.Manager) error {
			_, e := m.Upload(ctx, r.ID, r.Revision, "test", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
			return e
		},
		"manifest": func(m *applications.Manager) error {
			_, e := m.AssignManifest(ctx, r.ID, r.Revision, "new.plist", manifest)
			return e
		},
		"delete manifest": func(m *applications.Manager) error { _, e := m.DeleteManifest(ctx, r.ID, r.Revision); return e },
		"history":         func(m *applications.Manager) error { _, e := m.History(ctx, r.ID, paging.Page{}); return e },
		"note": func(m *applications.Manager) error {
			_, e := m.AddHistoryNote(ctx, r.ID, r.Revision, "audit note")
			return e
		},
		"open": func(m *applications.Manager) error {
			body, _, e := m.Open(ctx, r.ID, r.Content.Revision, 0, -1)
			if e != nil {
				return e
			}
			return body.Close()
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			for failAt := 1; failAt < 100; failAt++ {
				persistent := state.NewMemory()
				if err = persistent.Update(ctx, []string{"seed"}, func(tx state.Tx) error {
					for _, row := range seed {
						if e := tx.Put(ctx, row); e != nil {
							return e
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				backend := &memoryBlobs{objects: map[string][]byte{r.Content.Key: bytes.Clone(payload)}}
				injected := &faultState{Store: persistent, at: failAt}
				candidateCfg := cfg
				candidateCfg.State = injected
				candidateCfg.Backends = map[string]applications.BlobStore{"test": backend}
				m, e := applications.New(candidateCfg)
				if e != nil {
					t.Fatal(e)
				}
				e = operation(m)
				if !injected.failed {
					if e != nil {
						t.Fatal(e)
					}
					break
				}
				if !errors.Is(e, errPersistence) {
					t.Fatalf("boundary %d hid persistence failure: %v", failAt, e)
				}
				injected.at = 0
				if name == "delete" {
					if e = m.Delete(ctx, r.ID, r.Revision); e != nil {
						t.Fatalf("cannot retry deletion after boundary %d: %v", failAt, e)
					}
				} else {
					current, e := m.Get(ctx, r.ID)
					if e != nil {
						t.Fatal(e)
					}
					if current.Revision != r.Revision || current.HistorySequence != r.HistorySequence {
						t.Fatalf("failed %s committed at boundary %d", name, failAt)
					}
					if len(backend.objects) != 1 {
						t.Fatalf("failed %s left an incomplete upload", name)
					}
				}
			}
		})
	}
}

// failingBlobs injects transfer failures around a real immutable storage fixture.
type failingBlobs struct {
	applications.BlobStore
	operation string
}

// Put simulates backend rejection before an object is available.
func (s *failingBlobs) Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error {
	if s.operation == "put" {
		return io.ErrUnexpectedEOF
	}
	return s.BlobStore.Put(ctx, key, r, size)
}

// Open models download, streaming and response-close failures during readback.
func (s *failingBlobs) Open(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if s.operation == "open" || s.operation == "cleanup" {
		return nil, io.ErrUnexpectedEOF
	}
	body, err := s.BlobStore.Open(ctx, key, offset, length)
	if err != nil {
		return nil, err
	}
	return faultReadback{ReadCloser: body, operation: s.operation}, nil
}

// Delete preserves a failure for explicit retry and cleanup-error reporting.
func (s *failingBlobs) Delete(ctx context.Context, key string) error {
	if s.operation == "delete" || s.operation == "cleanup" {
		return io.ErrClosedPipe
	}
	return s.BlobStore.Delete(ctx, key)
}

type faultReadback struct {
	io.ReadCloser
	operation string
}

// Read injects a interrupted transfer after the upload itself succeeded.
func (r faultReadback) Read(p []byte) (int, error) {
	if r.operation == "read" {
		return 0, io.ErrUnexpectedEOF
	}
	return r.ReadCloser.Read(p)
}

// Close releases the real body even when the provider reports completion failure.
func (r faultReadback) Close() error {
	err := r.ReadCloser.Close()
	if r.operation == "close" {
		return errors.Join(err, io.ErrClosedPipe)
	}
	return err
}

// TestStorageTransferFailures tests every transfer completion boundary and proves
// a failed backend operation cannot produce a ready package revision.
func TestStorageTransferFailures(t *testing.T) {
	payload, policy := fixture(t)
	ctx := t.Context()
	for _, operation := range []string{"put", "open", "read", "close", "cleanup"} {
		t.Run(operation, func(t *testing.T) {
			backend := &failingBlobs{BlobStore: &memoryBlobs{objects: map[string][]byte{}}, operation: operation}
			m, err := applications.New(applications.Config{State: state.NewMemory(), Backends: map[string]applications.BlobStore{"test": backend}, ScratchDir: t.TempDir(), Verification: policy})
			if err != nil {
				t.Fatal(err)
			}
			r, err := m.Create(ctx, metadata())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Upload(ctx, r.ID, r.Revision, "test", applications.Source{Kind: "upload"}, bytes.NewReader(payload), ""); err == nil {
				t.Fatal("transfer failure ignored")
			}
			if operation == "cleanup" && !strings.Contains(err.Error(), "remove incomplete upload") {
				t.Fatal("cleanup error lost", err)
			}
			current, err := m.Get(ctx, r.ID)
			if err != nil || current.Content != nil || current.CloudTransferStatus != "MISSING" {
				t.Fatal("failed transfer became ready", err)
			}
		})
	}
	// Deletion disables a package first and can be retried after storage recovers.
	backing := state.NewMemory()
	backend := &failingBlobs{BlobStore: &memoryBlobs{objects: map[string][]byte{}}}
	cfg := applications.Config{State: backing, Backends: map[string]applications.BlobStore{"test": backend}, ScratchDir: t.TempDir(), Verification: policy}
	m, err := applications.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Create(ctx, metadata())
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.Upload(ctx, r.ID, r.Revision, "test", applications.Source{Kind: "upload"}, bytes.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.Open(ctx, r.ID, r.Content.Revision, -1, 1); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal(err)
	}
	other := cfg
	other.Backends = map[string]applications.BlobStore{"other": backend}
	unavailable, err := applications.New(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = unavailable.Open(ctx, r.ID, r.Content.Revision, 0, -1); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal("missing backend read", err)
	}
	if err = unavailable.Delete(ctx, r.ID, r.Revision); !errors.Is(err, applications.ErrInvalid) {
		t.Fatal("missing backend deletion", err)
	}
	backend.operation = "delete"
	if err = m.Delete(ctx, r.ID, r.Revision); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if _, err = m.Revision(ctx, r.ID, r.Content.Revision); !errors.Is(err, applications.ErrConflict) {
		t.Fatal("deleting package readable", err)
	}
	child := metadata()
	child.ParentPackageID = &r.ID
	if _, err = m.Create(ctx, child); !errors.Is(err, applications.ErrConflict) {
		t.Fatal("deleting parent accepted", err)
	}
	backend.operation = ""
	if err = m.Delete(ctx, r.ID, r.Revision); err != nil {
		t.Fatal("delete retry", err)
	}
}

// TestCorruptStateIsRejected proves persistent JSON corruption cannot silently
// disappear from catalogue, history, parent checks or deletion.
func TestCorruptStateIsRejected(t *testing.T) {
	ctx := t.Context()
	for _, kind := range []string{"record", "history", "content", "child"} {
		t.Run(kind, func(t *testing.T) {
			persistent := state.NewMemory()
			backend := &memoryBlobs{objects: map[string][]byte{}}
			m, err := applications.New(applications.Config{State: persistent, Backends: map[string]applications.BlobStore{"test": backend}})
			if err != nil {
				t.Fatal(err)
			}
			r, err := m.Create(ctx, metadata())
			if err != nil {
				t.Fatal(err)
			}
			key := "applications/records/" + r.ID
			switch kind {
			case "history":
				key = "applications/history/" + r.ID + "/00000000000000000001"
			case "content":
				key = "applications/content/" + r.ID + "/revision"
			case "child":
				key = "applications/records/zzz-child"
			}
			if err = persistent.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: []byte("invalid json")}) }); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "record":
				_, err = m.List(ctx, paging.Page{})
			case "history":
				_, err = m.History(ctx, r.ID, paging.Page{})
			default:
				err = m.Delete(ctx, r.ID, r.Revision)
			}
			if err == nil {
				t.Fatal("corrupt state ignored")
			}
		})
	}
}
