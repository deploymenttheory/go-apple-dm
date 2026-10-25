package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

// TestADEPendingIntentHasOneOwner exercises the filesystem exclusivity that
// protects the original assignment when two invocations pass preflight together.
func TestADEPendingIntentHasOneOwner(t *testing.T) {
	t.Parallel()
	s := &Session{Env: &Environment{Workspace: &Workspace{Directory: t.TempDir()}}}
	if err := os.Mkdir(s.Env.Workspace.path("ade"), 0o700); err != nil {
		t.Fatal(err)
	}
	intents := []adeAssignmentIntent{
		{RequestID: "first", OriginalProfile: "original-first", EvidenceDirectory: filepath.Join(t.TempDir(), "first")},
		{RequestID: "second", OriginalProfile: "original-second", EvidenceDirectory: filepath.Join(t.TempDir(), "second")},
	}
	type result struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, len(intents))
	for i, intent := range intents {
		go func() {
			<-start
			results <- result{index: i, err: adeAcquireFence(s, intent)}
		}()
	}
	close(start)
	winner, loser := -1, -1
	for range intents {
		r := <-results
		if r.err == nil {
			if winner != -1 {
				t.Fatal("two invocations acquired the same pending intent")
			}
			winner = r.index
		} else if errors.Is(r.err, ErrBlocked) {
			loser = r.index
		} else {
			t.Fatal(r.err)
		}
	}
	if winner == -1 || loser == -1 {
		t.Fatalf("missing exclusive owner: winner=%d loser=%d", winner, loser)
	}
	path := s.Env.Workspace.path("ade", "pending-assignment.json")
	// #nosec G304 -- This fixed fixture path is beneath the test's private temporary workspace.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var retained adeAssignmentIntent
	if err := json.Unmarshal(before, &retained); err != nil {
		t.Fatal(err)
	}
	want := intents[winner]
	if retained.RequestID != want.RequestID || retained.OriginalProfile != want.OriginalProfile || retained.EvidenceDirectory != want.EvidenceDirectory {
		t.Fatal("the winning original assignment or evidence locator was lost", retained)
	}
	if err := adeReleaseFence(s, intents[loser].RequestID); !errors.Is(err, errOperation) {
		t.Fatal("a competing invocation could release the winner's intent", err)
	}
	// #nosec G304 -- This fixed fixture path is beneath the test's private temporary workspace.
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected release changed the original recovery bytes", err)
	}
	if err := adeReleaseFence(s, want.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the verified owner did not release its completed intent", err)
	}
}

// TestADEPendingIntentReadFailuresPreserveEvidence verifies that missing,
// malformed and oversized recovery state cannot be treated as completed intent.
func TestADEPendingIntentReadFailuresPreserveEvidence(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing", "malformed", "oversized"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			s := &Session{Env: &Environment{Workspace: &Workspace{Directory: t.TempDir()}}}
			if err := os.Mkdir(s.Env.Workspace.path("ade"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := s.Env.Workspace.path("ade", "pending-assignment.json")
			var before []byte
			if fault != "missing" {
				before = []byte(`{"request_id":"private-original"`)
				if fault == "oversized" {
					before = bytes.Repeat([]byte("x"), (1<<20)+1)
				}
				if err := os.WriteFile(path, before, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := adeReleaseFence(s, "private-original")
			want := errOperation
			if fault == "missing" {
				want = ErrBlocked
			}
			if !errors.Is(err, want) || strings.Contains(err.Error(), "private-original") || strings.Contains(err.Error(), s.Env.Workspace.Directory) {
				t.Fatal("unreadable recovery state was accepted or exposed", err)
			}
			// #nosec G304 -- This fixed fixture path is beneath the test's private temporary workspace.
			after, readErr := os.ReadFile(path)
			if fault == "missing" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("release manufactured missing recovery evidence", readErr)
				}
			} else if readErr != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid recovery evidence was removed or changed", readErr)
			}
		})
	}
}

// TestADEPendingIntentParentFailure covers a real directory replacement between
// preflight and acquisition without relying on platform-specific permission bits.
func TestADEPendingIntentParentFailure(t *testing.T) {
	t.Parallel()
	s := &Session{Env: &Environment{Workspace: &Workspace{Directory: t.TempDir()}}}
	path := s.Env.Workspace.path("ade")
	before := []byte("operator-owned private state")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := adeAcquireFence(s, adeAssignmentIntent{RequestID: "private-intent"}); !errors.Is(err, errOperation) || strings.Contains(err.Error(), "private-intent") || strings.Contains(err.Error(), path) {
		t.Fatal("pending-state persistence failure was ignored or exposed", err)
	}
	// #nosec G304 -- This fixed fixture path is beneath the test's private temporary workspace.
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed acquisition overwrote existing private state", err)
	}
}

// TestADEStoredDeviceRejectsConflictingEvidence refuses a deleted or different
// persisted profile and repeated cursors instead of reporting assignment success.
func TestADEStoredDeviceRejectsConflictingEvidence(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"deleted", "different profile", "repeated cursor"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			calls := 0
			s, c := adeFailureSession(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/devices") {
					t.Error("readback performed a non-device read", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				d := dep.StoredDevice{Device: dep.Device{SerialNumber: "DESIGNATED", ProfileUUID: "expected"}}
				cursor := ""
				switch fault {
				case "deleted":
					d.Deleted = true
				case "different profile":
					d.ProfileUUID = "PRIVATE-FOREIGN-PROFILE"
				case "repeated cursor":
					d.SerialNumber = "OTHER"
					cursor = "same-page"
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"Items": []dep.StoredDevice{d}, "NextCursor": cursor}); err != nil {
					t.Error(err)
				}
			})
			err := (&adeAccess{config: c}).storedDevice(t.Context(), s, "expected")
			if !errors.Is(err, errOperation) || strings.Contains(err.Error(), "PRIVATE-FOREIGN-PROFILE") {
				t.Fatal("conflicting persisted evidence was accepted or exposed", err)
			}
			wantCalls := 1
			if fault == "repeated cursor" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("readback requests=%d; want %d", calls, wantCalls)
			}
			if _, err := os.Stat(filepath.Join(s.Evidence, "stored-device.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("conflicting state manufactured successful persisted-device proof", err)
			}
		})
	}
}
