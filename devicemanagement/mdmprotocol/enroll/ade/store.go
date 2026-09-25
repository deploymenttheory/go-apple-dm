package ade

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// ErrStore is returned for store failures.
var ErrStore = fault.NewOperator(fault.Unavailable, "the automated device enrollment store failed")

// Record is what the handler persists per serial: the MachineInfo as
// parsed, the DEP record it was joined to, and when it arrived.
type Record struct {
	Parsed *Parsed
	// DEP is what DEPLookup returned for the serial; nil when none.
	DEP        any
	ReceivedAt time.Time
}

// MachineInfoStore keeps the last MachineInfo per serial. Put replaces.
type MachineInfoStore interface {
	// Put stores the latest verified machine information for its serial number.
	Put(ctx context.Context, rec *Record) error
	// Get returns stored machine information and a found flag for the serial number.
	Get(ctx context.Context, serial string) (*Record, bool, error)
}

// DEPLookup finds the device record a DEP sync produced for a serial, so
// the MachineInfo can be joined to it. The dep package satisfies it; any
// record type is accepted and handed to the ProfileHook unchanged.
type DEPLookup interface {
	// DeviceBySerial looks up the device's enrollment-service record and distinguishes
	// absence from lookup failure.
	DeviceBySerial(ctx context.Context, serial string) (record any, found bool, err error)
}

// DEPLookupFunc adapts a function to DEPLookup.
type DEPLookupFunc func(ctx context.Context, serial string) (any, bool, error)

// DeviceBySerial implements DEPLookup.
func (f DEPLookupFunc) DeviceBySerial(ctx context.Context, serial string) (any, bool, error) {
	return f(ctx, serial)
}

// MemStore is an in-memory MachineInfoStore, safe for concurrent use.
type MemStore struct {
	mu   sync.RWMutex
	recs map[string]*Record
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{recs: map[string]*Record{}} }

// Put implements MachineInfoStore.
func (s *MemStore) Put(_ context.Context, rec *Record) error {
	if rec == nil || rec.Parsed == nil || rec.Parsed.SERIAL == "" {
		return fmt.Errorf("%w: record without serial", ErrStore)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.Parsed.SERIAL] = rec
	return nil
}

// Get implements MachineInfoStore.
func (s *MemStore) Get(_ context.Context, serial string) (*Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.recs[serial]
	return rec, ok, nil
}

// Len reports how many serials are stored.
func (s *MemStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.recs)
}
