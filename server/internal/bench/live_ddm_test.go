package bench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
)

func TestLiveDDMRequiresReportsAndCleansUp(t *testing.T) {
	for _, failure := range []string{"", "unchanged values", "mismatched values", "stale declarations", "missing automatic activation", "invalid configuration", "upload", "cleanup", "inventory", "set", "assignment", "notify", "status", "values", "cleanup notify", "cleanup status"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			declarations := []string{}
			assigned, deletes := false, 0
			answer, err := plist.Marshal(map[string]any{"QueryResponses": map[string]string{"OSVersion": "26.6.2", "BuildVersion": "25G83"}})
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if (failure == "inventory" && strings.HasSuffix(r.URL.Path, "/commands")) ||
					(failure == "set" && r.Method == "PUT" && strings.Contains(r.URL.Path, "/sets/") && !strings.Contains(r.URL.Path, "/enrollments/")) ||
					(failure == "assignment" && r.Method == "PUT" && strings.Contains(r.URL.Path, "/enrollments/")) ||
					(failure == "notify" && assigned && strings.HasSuffix(r.URL.Path, "/notify")) ||
					(failure == "status" && assigned && strings.HasSuffix(r.URL.Path, "/status")) ||
					(failure == "values" && strings.HasSuffix(r.URL.Path, "/status/values")) ||
					(failure == "cleanup notify" && deletes > 0 && strings.HasSuffix(r.URL.Path, "/notify")) ||
					(failure == "cleanup status" && deletes > 0 && strings.HasSuffix(r.URL.Path, "/status")) {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				switch {
				case r.Method == "DELETE":
					deletes++
					assigned = false
					if failure == "cleanup" && strings.Contains(r.URL.Path, "/declarations/") {
						w.WriteHeader(503)
						return
					}
					w.WriteHeader(204)
				case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/declarations"):
					var d struct{ Identifier string }
					if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
						t.Error(err)
					}
					if len(d.Identifier) > ddm.MaxIdentifierBytes {
						t.Error("live declaration identifier exceeds the server limit")
					}
					if failure == "upload" {
						w.WriteHeader(400)
						return
					}
					declarations = append(declarations, d.Identifier)
				case r.Method == "PUT" && strings.Contains(r.URL.Path, "/enrollments/"):
					assigned = true
				case strings.HasSuffix(r.URL.Path, "/commands"):
					json.NewEncoder(w).Encode(map[string]int{"Queued": 1})
				case strings.HasSuffix(r.URL.Path, "/push"):
					json.NewEncoder(w).Encode(map[string]bool{"Sent": true})
				case strings.HasSuffix(r.URL.Path, "/result"):
					json.NewEncoder(w).Encode(map[string]any{"Status": "Acknowledged", "Response": answer})
				case strings.HasSuffix(r.URL.Path, "/status"):
					rows := []ddm.DeclarationStatus{}
					if assigned {
						for _, id := range append(append([]string{}, declarations...), ddm.SubscriptionIdentifier, ddm.SubscriptionActivationIdentifier) {
							valid := "valid"
							if failure == "missing automatic activation" && id == ddm.SubscriptionActivationIdentifier {
								cancel()
								continue
							}
							if failure == "invalid configuration" && strings.HasSuffix(id, ".c") {
								valid = "invalid"
								cancel()
							}
							seen := time.Now().UTC()
							if failure == "stale declarations" {
								seen = seen.Add(-time.Hour)
								cancel()
							}
							rows = append(rows, ddm.DeclarationStatus{Identifier: id, Active: true, Valid: valid, LastSeen: seen})
						}
					}
					json.NewEncoder(w).Encode(rows)
				case strings.HasSuffix(r.URL.Path, "/status/values"):
					at := time.Now().UTC()
					if failure == "unchanged values" {
						at = at.Add(-time.Hour)
					}
					version := []byte(`"26.6.2"`)
					if failure == "mismatched values" {
						version = []byte(`"26.6.1"`)
						cancel()
					}
					json.NewEncoder(w).Encode(map[string]any{"Items": []ddm.StatusValue{
						{Path: "device.operating-system.version", Value: version, LastSeen: at},
						{Path: "device.operating-system.build-version", Value: []byte(`"25G83"`), LastSeen: at},
					}})
				case r.Method == "GET":
					json.NewEncoder(w).Encode(map[string]any{"Enabled": true, "TokenUpdatedAt": time.Now()})
				}
			}))
			defer srv.Close()
			e := &Environment{Instance: Instance{Mode: "live", URL: srv.URL}, Client: srv.Client()}
			err = liveDDM(ctx, e, "test-mac")
			if (failure == "" || failure == "unchanged values") != (err == nil) {
				t.Fatalf("live result: %v", err)
			}
			wantDeletes := 3
			if failure == "inventory" {
				wantDeletes = 0
			}
			if assigned || deletes != wantDeletes {
				t.Fatalf("cleanup incomplete: assigned=%v, deletes=%d", assigned, deletes)
			}
		})
	}
}

func TestDDMCleanupWaitsForDeviceToRemoveDeclarations(t *testing.T) {
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reads++
		rows := []ddm.DeclarationStatus{}
		if reads == 1 {
			rows = append(rows, ddm.DeclarationStatus{Identifier: "temporary"})
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	e := &Environment{Instance: Instance{URL: srv.URL}, Client: srv.Client()}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := cleanupLiveDDM(ctx, e, "/enrollments/device/test", "set", "temporary", "activation"); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatal("cleanup finished before device confirmation", reads)
	}
}
