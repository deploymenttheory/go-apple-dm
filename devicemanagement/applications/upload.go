package applications

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Upload stages and verifies source bytes, uploads them through the selected backend,
// then reads them back and compares size and SHA-256 before committing the association.
// Failed verification, cancellation and conflicting metadata leave no deployable revision.
// expectedSHA256 may pin an independently obtained source digest; empty computes it only.
func (m *Manager) Upload(ctx context.Context, id, expected, backend string, source Source, input io.Reader, expectedSHA256 string) (record Record, retErr error) {
	record, err := m.Get(ctx, id)
	if err != nil {
		return record, err
	}
	if expected == "" || record.Revision != expected || record.Deleting {
		return record, ErrConflict
	}
	store := m.cfg.Backends[backend]
	if store == nil || input == nil {
		return record, ErrInvalid
	}
	switch source.Kind {
	case "upload", "file":
	case "https":
		u, e := url.Parse(source.Location)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return record, ErrInvalid
		}
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		source.Location = u.String()
	default:
		return record, ErrInvalid
	}
	if len(source.Location) > 4096 {
		return record, ErrInvalid
	}
	if err = (Digests{SHA256: expectedSHA256}).Validate(); err != nil {
		return record, err
	}
	file, err := os.CreateTemp(m.cfg.ScratchDir, "application-package-*.pkg")
	if err != nil {
		return record, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	h := newDigestWriter()
	size, err := io.Copy(io.MultiWriter(file, h), io.LimitReader(contextReader{ctx, input}, m.cfg.MaxBytes+1))
	if err != nil {
		return record, err
	}
	if size > m.cfg.MaxBytes {
		return record, ErrTooLarge
	}
	if size == 0 {
		return record, ErrInvalid
	}
	digests := h.sum()
	if err = record.Metadata.Digests.Match(digests); err != nil {
		return record, err
	}
	if err = (Digests{SHA256: expectedSHA256}).Match(digests); err != nil {
		return record, err
	}
	verification, err := VerifyPackage(ctx, file, size, m.cfg.Verification)
	if err != nil {
		return record, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return record, err
	}
	revision := rand.Text()
	key := id + "/" + revision + ".pkg"
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if e := store.Delete(cleanup, key); e != nil {
				retErr = fmt.Errorf("%w; remove incomplete upload: %v", retErr, e)
			}
		}
	}()
	if err = store.Put(ctx, key, file, size); err != nil {
		return record, err
	}
	readback, err := store.Open(ctx, key, 0, -1)
	if err != nil {
		return record, err
	}
	h = newDigestWriter()
	actual, err := io.Copy(h, io.LimitReader(contextReader{ctx, readback}, size+1))
	closeErr := readback.Close()
	if err != nil {
		return record, err
	}
	if closeErr != nil {
		return record, closeErr
	}
	if actual != size || h.sum() != digests {
		return record, ErrIntegrity
	}
	err = m.cfg.State.Update(ctx, []string{catalogueLock, recordPrefix + id}, func(tx state.Tx) error {
		current, e := read[Record](ctx, tx, recordPrefix+id)
		if e != nil {
			return e
		}
		if current.Revision != expected || current.Deleting {
			return ErrConflict
		}
		content := Content{Revision: revision, Backend: backend, Key: key, Digests: digests, Format: verification.Format, Size: size, Metadata: current.Metadata, Source: source, CreatedAt: tx.Now(), Verification: verification}
		if e = put(ctx, tx, contentPrefix+id+"/"+revision, content); e != nil {
			return e
		}
		current.Content = &content
		current.Manifest = nil
		current.CloudTransferStatus = "READY"
		current.Revision = rand.Text()
		current.UpdatedAt = tx.Now()
		if e = appendHistory(ctx, tx, &current, "uploaded", ""); e != nil {
			return e
		}
		if e = put(ctx, tx, recordPrefix+id, current); e != nil {
			return e
		}
		record = current
		return nil
	})
	committed = err == nil
	return record, err
}

// Open reads bytes from a verified immutable revision. The installation manifest carries
// its SHA-256, so changes made outside the manager cannot silently change installed bytes.
func (m *Manager) Open(ctx context.Context, id, revision string, offset, length int64) (io.ReadCloser, Content, error) {
	c, err := m.Revision(ctx, id, revision)
	if err != nil {
		return nil, c, err
	}
	if offset < 0 || offset > c.Size || length < -1 || length > c.Size-offset {
		return nil, c, ErrInvalid
	}
	b := m.cfg.Backends[c.Backend]
	if b == nil {
		return nil, c, ErrInvalid
	}
	r, err := b.Open(ctx, c.Key, offset, length)
	return r, c, err
}
