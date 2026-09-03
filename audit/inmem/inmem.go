package inmem

import (
	"context"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/audit"
)

// Store is an in-memory audit trail. It is unsealed and unbounded by design:
// tests and a throwaway run want the same behaviour as SQL without a
// database, and a deployment that needs the records to survive a restart
// configures one.
type Store struct {
	mu      sync.RWMutex
	records []audit.Record
	next    int64
}

var _ audit.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store { return &Store{next: 1} }

// Append implements audit.Store.
func (s *Store) Append(_ context.Context, rec audit.Record) (audit.Record, error) {
	if rec.Type == "" {
		return audit.Record{}, audit.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == 0 {
		s.next = 1
	}
	rec.ID = s.next
	s.next++
	rec.Fields = maps.Clone(rec.Fields)
	s.records = append(s.records, rec)
	return clone(rec), nil
}

// List implements audit.Store, newest first.
func (s *Store) List(_ context.Context, q audit.Query, p audit.Page) (audit.Result[audit.Record], error) {
	before := int64(0)
	if p.Cursor != "" {
		v, err := strconv.ParseInt(p.Cursor, 10, 64)
		if err != nil {
			return audit.Result[audit.Record]{}, audit.ErrInvalid
		}
		before = v
	}
	limit := p.Size()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := audit.Result[audit.Record]{}
	// Walk backwards: the newest record has the highest id, and the cursor
	// is the last id already returned.
	for i := len(s.records) - 1; i >= 0; i-- {
		rec := s.records[i]
		if before != 0 && rec.ID >= before {
			continue
		}
		if !q.Matches(rec) {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].ID, 10)
			break
		}
		out.Items = append(out.Items, clone(rec))
	}
	return out, nil
}

// Get implements audit.Store.
func (s *Store) Get(_ context.Context, id int64) (audit.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rec := range s.records {
		if rec.ID == id {
			return clone(rec), nil
		}
	}
	return audit.Record{}, audit.ErrNotFound
}

// Prune implements audit.Store.
func (s *Store) Prune(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.records[:0]
	removed := 0
	for _, rec := range s.records {
		if rec.At.Before(before) {
			removed++
			continue
		}
		kept = append(kept, rec)
	}
	s.records = kept
	return removed, nil
}

// clone copies a record out so a caller cannot mutate stored state.
func clone(rec audit.Record) audit.Record {
	rec.Fields = maps.Clone(rec.Fields)
	return rec
}
