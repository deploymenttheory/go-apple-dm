package lab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
)

// TestLiveBlueprintProfileConvergence covers native profile installation, replacement,
// removal, and a profile that never reaches the declared state.
func TestLiveBlueprintProfileConvergence(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		observed   []string
		fail       bool
	}{
		{"install", "v1", []string{"", "v1"}, false},
		{"replace", "v2", []string{"v1", "v2"}, false},
		{"remove", "", []string{"v2", ""}, false},
		{"stale replacement", "v2", []string{"v1"}, true},
		{"never installed", "v1", []string{""}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reads := 0
			e := &Environment{Instance: Instance{URL: "https://lab.invalid"}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"Queued":1}`
				switch {
				case strings.HasSuffix(r.URL.Path, "/push"):
					body = `{"Sent":true}`
				case strings.HasSuffix(r.URL.Path, "/result"):
					name := tc.observed[min(reads, len(tc.observed)-1)]
					reads++
					profiles := []map[string]string{}
					if name != "" {
						profiles = append(profiles, map[string]string{"PayloadIdentifier": "profile", "PayloadUUID": "uuid", "PayloadDisplayName": name, "Source": "Declarative Device Management"})
					}
					raw, err := plist.Marshal(map[string]any{"ProfileList": profiles})
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(map[string]any{"Status": "Acknowledged", "Response": raw})
					if err != nil {
						t.Fatal(err)
					}
					body = string(encoded)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			dir := t.TempDir()
			check := &liveBlueprintTest{e: e, path: "/enrollments/device/mac", dir: dir, phase: "observe", profileID: "profile", profileUUID: "uuid"}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			err := check.checkProfile(ctx, tc.want)
			if (err != nil) != tc.fail {
				t.Fatalf("convergence result: %v", err)
			}
			if !tc.fail {
				files, err := filepath.Glob(filepath.Join(dir, "*-response.plist"))
				if err != nil || reads != 2 || len(files) != 2 {
					t.Fatalf("fresh command evidence: reads=%d files=%d err=%v", reads, len(files), err)
				}
			}
		})
	}
}
