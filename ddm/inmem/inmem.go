package inmem

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// Store implements ddm.Store in memory.
type Store struct {
	mu sync.Mutex
	st *state
}

var _ ddm.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{st: newState()}
}

// versionKey identifies one revision of a declaration.
type versionKey struct {
	identifier string
	token      string
}

// statusKey identifies one declaration status row of an enrollment.
type statusKey struct {
	kind       schemaddm.Kind
	identifier string
}

// state is every table of the store. Enrollments are keyed by their id
// string, so a user channel ("<device>:<user>") is a different key from
// its device. Byte slices held in the state are never mutated in place:
// writes store a copy and reads return a copy, so a cloned state may
// share them safely.
type state struct {
	decls    map[string]ddm.Declaration
	versions map[versionKey]ddm.DeclarationVersion
	sets     map[string]ddm.Set
	// members is set name -> member identifiers.
	members map[string]map[string]struct{}
	// ids remembers the full identity of every enrollment key seen.
	ids map[string]mdm.EnrollmentID
	// enrollSets is enrollment key -> assigned set names.
	enrollSets map[string]map[string]struct{}
	// enrollDecls is enrollment key -> directly assigned identifiers.
	enrollDecls   map[string]map[string]struct{}
	snapshots     map[string]ddm.Snapshot
	statusDecls   map[string]map[statusKey]ddm.DeclarationStatus
	statusValues  map[string]map[string]ddm.StatusValue
	statusErrors  map[string][]ddm.StatusError
	statusReports map[string][]ddm.StatusReportRecord
	changes       map[int64]ddm.Change
	// seq is the single monotonic counter behind report, error, and
	// change sequence numbers.
	seq int64
}

func newState() *state {
	return &state{
		decls:         map[string]ddm.Declaration{},
		versions:      map[versionKey]ddm.DeclarationVersion{},
		sets:          map[string]ddm.Set{},
		members:       map[string]map[string]struct{}{},
		ids:           map[string]mdm.EnrollmentID{},
		enrollSets:    map[string]map[string]struct{}{},
		enrollDecls:   map[string]map[string]struct{}{},
		snapshots:     map[string]ddm.Snapshot{},
		statusDecls:   map[string]map[statusKey]ddm.DeclarationStatus{},
		statusValues:  map[string]map[string]ddm.StatusValue{},
		statusErrors:  map[string][]ddm.StatusError{},
		statusReports: map[string][]ddm.StatusReportRecord{},
		changes:       map[int64]ddm.Change{},
	}
}

// clone returns a copy of the state that shares no map or slice with the
// original.
func (st *state) clone() *state {
	return &state{
		decls:         maps.Clone(st.decls),
		versions:      maps.Clone(st.versions),
		sets:          maps.Clone(st.sets),
		members:       cloneNested(st.members),
		ids:           maps.Clone(st.ids),
		enrollSets:    cloneNested(st.enrollSets),
		enrollDecls:   cloneNested(st.enrollDecls),
		snapshots:     maps.Clone(st.snapshots),
		statusDecls:   cloneNested(st.statusDecls),
		statusValues:  cloneNested(st.statusValues),
		statusErrors:  cloneSlices(st.statusErrors),
		statusReports: cloneSlices(st.statusReports),
		changes:       maps.Clone(st.changes),
		seq:           st.seq,
	}
}

func cloneNested[K, IK comparable, V any](m map[K]map[IK]V) map[K]map[IK]V {
	out := make(map[K]map[IK]V, len(m))
	for k, inner := range m {
		out[k] = maps.Clone(inner)
	}
	return out
}

func cloneSlices[K comparable, V any](m map[K][]V) map[K][]V {
	out := make(map[K][]V, len(m))
	for k, s := range m {
		out[k] = slices.Clone(s)
	}
	return out
}

func (st *state) nextSeq() int64 {
	st.seq++
	return st.seq
}

// tx is the view every method runs against: the live state while the
// store lock is held, or a private copy inside Update. It also satisfies
// ddm.Store so a callback can discover that nesting Update is invalid.
type tx struct {
	st *state
}

var _ ddm.Store = (*tx)(nil)

// Update implements ddm.Store on the transaction view: nested transactions
// are not supported.
func (t *tx) Update(context.Context, func(ddm.Tx) error) error {
	return fmt.Errorf("%w: nested Update", ddm.ErrInvalid)
}

// Update implements ddm.Store. fn runs against a deep copy of the state
// under the store lock; the copy replaces the live state only when fn
// returns nil. An error or a panic in fn leaves the store untouched. fn
// must use the Tx it is given: calling the Store's own methods from inside
// fn would deadlock on the lock.
func (s *Store) Update(_ context.Context, fn func(ddm.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil Update callback", ddm.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.st.clone()
	if err := fn(&tx{st: cp}); err != nil {
		return err
	}
	s.st = cp
	return nil
}

// view locks the store and returns the live view with the unlock function.
func (s *Store) view() (*tx, func()) {
	s.mu.Lock()
	return &tx{st: s.st}, s.mu.Unlock
}

// validID maps an ill-formed enrollment id to ddm.ErrInvalid.
func validID(id mdm.EnrollmentID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ddm.ErrInvalid, err)
	}
	return nil
}

// validName rejects an empty name, identifier, token, or path.
func validName(what, name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty %s", ddm.ErrInvalid, what)
	}
	return nil
}

// notFound builds an ErrNotFound for one named record.
func notFound(what, name string) error {
	return fmt.Errorf("%w: %s %q", ddm.ErrNotFound, what, name)
}

// add inserts key into the inner set of m, creating it as needed, and
// reports whether the key was new.
func add(m map[string]map[string]struct{}, outer, key string) bool {
	inner, ok := m[outer]
	if !ok {
		inner = map[string]struct{}{}
		m[outer] = inner
	}
	if _, exists := inner[key]; exists {
		return false
	}
	inner[key] = struct{}{}
	return true
}

// remove deletes key from the inner set of m, dropping the inner set when
// it becomes empty, and reports whether the key was present.
func remove(m map[string]map[string]struct{}, outer, key string) bool {
	inner, ok := m[outer]
	if !ok {
		return false
	}
	if _, exists := inner[key]; !exists {
		return false
	}
	delete(inner, key)
	if len(inner) == 0 {
		delete(m, outer)
	}
	return true
}

// removeEverywhere deletes key from every inner set of m.
func removeEverywhere(m map[string]map[string]struct{}, key string) {
	for outer := range m {
		remove(m, outer, key)
	}
}

// sortedKeys returns the keys of an inner set in order.
func sortedKeys(inner map[string]struct{}) []string {
	return slices.Sorted(maps.Keys(inner))
}
