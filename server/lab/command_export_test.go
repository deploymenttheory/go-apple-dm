package lab

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
)

// TestCommandRetainsEnrollmentScopedEvidence checks the reusable device primitive.
func TestCommandRetainsEnrollmentScopedEvidence(t *testing.T) {
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		t.Fatal(err)
	}
	e := &Environment{Instance: Instance{URL: "https://lab.invalid"}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"Queued":1}`
		if strings.HasSuffix(r.URL.Path, "/push") {
			body = `{"Sent":true}`
		}
		if strings.HasSuffix(r.URL.Path, "/result") {
			if !strings.HasSuffix(r.URL.Path, "/enrollments/device/mac/commands/"+cmd.UUID+"/result") {
				t.Fatal("polled wrong enrollment/UUID")
			}
			encoded, _ := json.Marshal(map[string]any{"Status": "Acknowledged", "Response": []byte("native-response")})
			body = string(encoded)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	dir := t.TempDir()
	response, err := Command(t.Context(), e, "/enrollments/device/mac", cmd, dir)
	if err != nil || string(response) != "native-response" {
		t.Fatalf("response %q, %v", response, err)
	}
	for _, name := range []string{"request.plist", "push.json", "result.json", "response.plist"} {
		// #nosec G304 -- The path combines t.TempDir with a locally generated command UUID.
		b, err := os.ReadFile(filepath.Join(dir, cmd.UUID+"-"+name))
		if err != nil || len(b) == 0 {
			t.Fatalf("missing evidence %s: %v", name, err)
		}
	}
	if _, err = Command(t.Context(), nil, "/x", cmd, dir); err == nil {
		t.Fatal("nil environment accepted")
	}
	if _, err = Command(t.Context(), e, "/x", nil, dir); err == nil {
		t.Fatal("nil command accepted")
	}
	cmd.UUID = "../outside"
	if _, err = Command(t.Context(), e, "/x", cmd, dir); err == nil {
		t.Fatal("path UUID accepted")
	}
}

// TestCommandEvidenceFailureStopsAcceptance proves missing evidence cannot pass,
// including when the device has already acknowledged the command.
func TestCommandEvidenceFailureStopsAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls int
	}{
		{"directory", 0},
		{"request.plist", 0},
		{"push.json", 2},
		{"push-attempts.json", 2},
		{"result.json", 3},
		{"response.plist", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd, err := mdm.NewCommand(&commands.ProfileList{})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if tc.name == "directory" {
				dir = filepath.Join(dir, "file-instead-of-directory")
				if err := os.WriteFile(dir, []byte("occupied"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(dir, cmd.UUID+"-"+tc.name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			e := &Environment{Instance: Instance{URL: "https://lab.invalid"}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `{"Queued":1}`
				switch {
				case strings.HasSuffix(r.URL.Path, "/push"):
					body = `{"Sent":true,"Outcome":"sent","Status":200}`
				case strings.HasSuffix(r.URL.Path, "/result"):
					body = `{"Status":"Acknowledged","Response":"bmF0aXZlLXJlc3BvbnNl"}`
				case strings.HasSuffix(r.URL.Path, "/commands"):
				default:
					t.Fatalf("unexpected request: %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			response, err := Command(t.Context(), e, "/enrollments/device/mac", cmd, dir)
			var pathError *os.PathError
			if !errors.As(err, &pathError) || response != nil {
				t.Fatalf("unrecorded evidence passed: response=%q err=%v", response, err)
			}
			if calls != tc.calls {
				t.Fatalf("continued after evidence failure: calls=%d want=%d", calls, tc.calls)
			}
		})
	}
}
