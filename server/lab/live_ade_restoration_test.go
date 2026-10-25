package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
)

// TestADERestorationFailureBoundaries exercises incomplete assignment, changed
// ownership and failed restoration through the real SQL-backed reference worker.
func TestADERestorationFailureBoundaries(t *testing.T) {
	for _, fault := range []string{"partial", "pending", "concurrent selection", "restoration rejected"} {
		t.Run(fault, func(t *testing.T) {
			s, c := adeRuntime(t)
			var other dep.ProfileResponse
			if fault == "concurrent selection" {
				if err := s.Env.api(t.Context(), "PUT", adeBase(c)+"/profile", map[string]string{"profile_name": "concurrent", "url": s.Env.URL + "/enroll/ade", "org_magic": "other"}, &other); err != nil {
					t.Fatal(err)
				}
			}
			base := s.Env.Client.Transport
			altered := false
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if err != nil || altered || r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/sync") {
					return response, err
				}
				altered = true
				switch fault {
				case "partial", "pending":
					var outcome adeSyncOutcome
					decodeErr := json.NewDecoder(response.Body).Decode(&outcome)
					closeErr := response.Body.Close()
					if decodeErr != nil || closeErr != nil {
						return nil, errors.Join(decodeErr, closeErr)
					}
					outcome.Assign.Assigned = 0
					if fault == "pending" {
						outcome.Assign.Pending = 1
					} else {
						outcome.Assign.Failed = 1
					}
					b, err := json.Marshal(outcome)
					if err != nil {
						return nil, err
					}
					response.Body = io.NopCloser(bytes.NewReader(b))
				case "concurrent selection":
					// Another operator changes the local choice after the actual worker ran.
					body, _ := json.Marshal(map[string]string{"profile_uuid": other.ProfileUUID})
					change, err := http.NewRequestWithContext(r.Context(), "PUT", s.Env.URL+"/admin/v1"+adeBase(c)+"/targets/"+c.Serial, bytes.NewReader(body))
					if err != nil {
						return nil, err
					}
					change.Header.Set("Authorization", "Bearer "+s.Env.Token)
					changed, err := base.RoundTrip(change)
					if err != nil {
						return nil, err
					}
					closeErr := changed.Body.Close()
					if changed.StatusCode != 204 || closeErr != nil {
						return nil, errors.New("fixture concurrent selection failed")
					}
				case "restoration rejected":
					s.Env.DEP.Script(dep.PathProfileDevs, deptest.Scripted{Status: 403, Body: "PRIVATE token serial"})
				}
				return response, nil
			})
			err := liveADEAssignment(t.Context(), s)
			if err == nil || !altered || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("failure passed or leaked", err)
			}
			_, markerErr := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json"))
			d, ok := s.Env.DEP.Device(c.Serial)
			if !ok {
				t.Fatal("fixture device lost")
			}
			cleanup := adeTestEvidence(t, s, "assignment-*/cleanup.json")
			if len(cleanup) != 1 {
				t.Fatal("cleanup proof absent")
			}
			if fault == "partial" {
				if !errors.Is(markerErr, os.ErrNotExist) || d.ProfileUUID != "" || !bytes.Contains(cleanup[0], []byte(`"status":"passed"`)) {
					t.Fatal("settled partial assignment was not restored", markerErr, d)
				}
			} else {
				if markerErr != nil || d.ProfileUUID == "" || !bytes.Contains(cleanup[0], []byte(`"status":"unresolved"`)) {
					t.Fatal("unresolved mutation lost its fence or proof", markerErr, d)
				}
			}
			if fault == "concurrent selection" {
				s.Env.Client.Transport = base
				var selection adeSelection
				if err := s.Env.api(t.Context(), "GET", adeBase(c)+"/targets", nil, &selection); err != nil {
					t.Fatal(err)
				}
				if selection.Serials[c.Serial] != other.ProfileUUID || s.Env.DEP.Count("DELETE", dep.PathProfileDevs) != 0 {
					t.Fatal("cleanup overwrote another operator's choice")
				}
			}
		})
	}
}
