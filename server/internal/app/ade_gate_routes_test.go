package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestADEConfiguredMinimumRoutes verifies that the production endpoint composes
// literal targets into the shared library gate before returning an enrollment profile.
func TestADEConfiguredMinimumRoutes(t *testing.T) {
	f := newEnrollFixture(t, "", func(c *app.Config) {
		c.Enroll.ADEMinimumOS = app.ADEMinimumOS{IOS: "27.1", IPadOS: "27.2", MacOS: "27.3", VisionOS: "27.4"}
	})
	for _, tc := range []struct {
		product, version, target string
		capable                  bool
		status                   int
	}{
		{"iPhone15,2", "17.0", "27.1", true, 403},
		{"iPad13,1", "17.0", "27.2", true, 403},
		{"Mac14,7", "14.0", "27.3", true, 403},
		{"RealityDevice14,1", "26.0", "27.4", true, 403},
		{"iPhone15,2", "27.1", "", false, 200},
		{"iPad13,1", "27.2", "", false, 200},
		{"Mac14,7", "27.3", "", false, 200},
		{"RealityDevice14,1", "27.4", "", false, 200},
		{"iPhone15,2", "16.0", "", true, 500},
		{"Mac14,7", "26.0", "", false, 500},
		{"future1,1", "99.0", "", true, 500},
		{"AppleTV14,1", "26.0", "", true, 200},
	} {
		t.Run(tc.product+"/"+tc.version, func(t *testing.T) {
			d := f.device(t, "GATE-"+tc.product+"-"+tc.version, tc.product)
			d.OSVersion = tc.version
			signed, err := d.SignedMachineInfo(simulator.ADEOptions{CanRequestSoftwareUpdate: tc.capable})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.publicURL+app.PathADE, bytes.NewReader(signed))
			if err != nil {
				t.Fatal(err)
			}
			res, err := f.client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			closeErr := res.Body.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if res.StatusCode != tc.status {
				t.Fatalf("%s/%s: %d %s", tc.product, tc.version, res.StatusCode, body)
			}
			if tc.target != "" {
				var reply struct {
					Code    string `json:"code"`
					Details struct {
						OSVersion string `json:"OSVersion"`
					} `json:"details"`
				}
				if err = json.Unmarshal(body, &reply); err != nil {
					t.Fatal(err)
				}
				if reply.Code != simulator.ErrorCodeSoftwareUpdateRequired || reply.Details.OSVersion != tc.target {
					t.Fatalf("literal target changed: %s", body)
				}
			}
		})
	}
	t.Run("disabled", func(t *testing.T) {
		f := newEnrollFixture(t, "", nil)
		d := f.device(t, "GATE-DISABLED", "Mac14,7")
		d.OSVersion = "13.0"
		if err := d.ADEEnroll(t.Context(), f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatal("unconfigured gate blocked enrollment", err)
		}
	})
}
