package ddmtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
)

// Factory returns a fresh, empty store for one test.
type Factory func(t *testing.T) ddm.Store

// RunAll runs every suite.
func RunAll(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("Declarations", func(t *testing.T) { RunDeclarationSuite(t, newStore) })
	t.Run("Sets", func(t *testing.T) { RunSetSuite(t, newStore) })
	t.Run("Assignments", func(t *testing.T) { RunAssignmentSuite(t, newStore) })
	t.Run("Snapshots", func(t *testing.T) { RunSnapshotSuite(t, newStore) })
	t.Run("Status", func(t *testing.T) { RunStatusSuite(t, newStore) })
	t.Run("Changes", func(t *testing.T) { RunChangeSuite(t, newStore) })
	t.Run("Clear", func(t *testing.T) { RunClearSuite(t, newStore) })
	t.Run("Update", func(t *testing.T) { RunUpdateSuite(t, newStore) })
	t.Run("Concurrency", func(t *testing.T) { RunConcurrencySuite(t, newStore) })
}

// put stores d and fails the test unless it was a change.
func put(t *testing.T, s ddm.Tx, d *ddm.Declaration) {
	t.Helper()
	changed, err := s.PutDeclaration(context.Background(), d)
	if err != nil {
		t.Fatalf("PutDeclaration %s: %v", d.Identifier, err)
	}
	if !changed {
		t.Fatalf("PutDeclaration %s: not changed", d.Identifier)
	}
}

// putSet creates the set.
func putSet(t *testing.T, s ddm.Tx, name string) {
	t.Helper()
	if _, err := s.PutSet(context.Background(), name, t0); err != nil {
		t.Fatalf("PutSet %s: %v", name, err)
	}
}

// addMember adds identifier to set.
func addMember(t *testing.T, s ddm.Tx, set, identifier string) {
	t.Helper()
	if _, err := s.AddSetDeclaration(context.Background(), set, identifier, t0); err != nil {
		t.Fatalf("AddSetDeclaration %s %s: %v", set, identifier, err)
	}
}

// assignSet assigns set to id.
func assignSet(t *testing.T, s ddm.Tx, id mdm.EnrollmentID, set string) {
	t.Helper()
	if _, err := s.AssignSet(context.Background(), id, set, t0); err != nil {
		t.Fatalf("AssignSet %s %s: %v", id.ID, set, err)
	}
}

// assignDecl assigns identifier directly to id.
func assignDecl(t *testing.T, s ddm.Tx, id mdm.EnrollmentID, identifier string) {
	t.Helper()
	if _, err := s.AssignDeclaration(context.Background(), id, identifier, t0); err != nil {
		t.Fatalf("AssignDeclaration %s %s: %v", id.ID, identifier, err)
	}
}

// wantErr asserts err wraps want.
func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

// wantStrings asserts got equals want.
func wantStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: got %q, want %q", what, got, want)
	}
}

// wantTime asserts got equals want by Equal.
func wantTime(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s: got %s, want %s", what, got, want)
	}
}

// identifiers projects declarations to their identifiers.
func identifiers(ds []ddm.Declaration) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Identifier
	}
	return out
}

// ids projects enrollment identities to their id strings.
func ids(es []mdm.EnrollmentID) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}
