package lab

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
)

// TestADEHardwareRejectsIncompleteNativeEvidence keeps actual device evidence
// independent of API success, certificate presence and caller-entered identifiers.
func TestADEHardwareRejectsIncompleteNativeEvidence(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"platform", "device address", "target address", "issuance method", "issuance serial", "certificate", "expired certificate", "disabled", "Authenticate", "TokenUpdate", "missing binding", "foreign binding", "foreign enrollment binding", "identity method", "no profile", "profile state", "effective profile", "permission", "not queued", "command error", "missing serial", "wrong serial", "missing OS", "missing build", "wrong platform", "missing supervision", "false supervision", "identity changed", "DDM unexercised", "DDM unsupported"} {
		t.Run(fault, func(t *testing.T) {
			c := adeAcceptance{}
			issuances := 0
			s, configured := adeFailureSession(t, func(w http.ResponseWriter, r *http.Request) {
				write := func(value any) {
					t.Helper()
					if err := json.NewEncoder(w).Encode(value); err != nil {
						t.Error(err)
					}
				}
				switch {
				case r.URL.Path == "/admin/v1/dep/accounts":
					adeAccountReply(t, w, c)
				case strings.HasSuffix(r.URL.Path, "/enrollment-evidence"):
					issuances++
					e := adeEnrollmentEvidence{ID: c.DeviceID, EnrollmentMethod: string(correlation.AutomatedDeviceEnrollment), Serial: c.Serial, UDID: c.DeviceID, Identity: "scep", Certificate: "pin", Enabled: true, NotAfter: time.Now().Add(time.Hour), AuthenticatedAt: time.Now(), TokenUpdatedAt: time.Now()}
					switch fault {
					case "issuance method":
						e.EnrollmentMethod = string(correlation.ProfileBasedDeviceEnrollment)
					case "issuance serial":
						e.Serial = "OTHER"
					case "certificate":
						e.Certificate = ""
					case "expired certificate":
						e.NotAfter = time.Now().Add(-time.Hour)
					case "disabled":
						e.Enabled = false
					case "Authenticate":
						e.AuthenticatedAt = time.Time{}
					case "TokenUpdate":
						e.TokenUpdatedAt = time.Time{}
					case "missing binding":
						e.UDID = ""
					case "foreign binding":
						e.UDID = "OTHER"
					case "foreign enrollment binding":
						e.EnrollmentID = "OTHER"
					case "identity method":
						e.Identity = "acme"
					case "identity changed":
						if issuances > 1 {
							e.Certificate = "replacement"
						}
					case "permission":
						w.WriteHeader(403)
						_, _ = io.WriteString(w, "PRIVATE SERIAL TOKEN")
						return
					}
					write(e)
				case strings.HasSuffix(r.URL.Path, "/targets"):
					uuid := "P1"
					if fault == "effective profile" {
						uuid = "OTHER"
					}
					write(adeSelection{Serials: map[string]string{c.Serial: uuid}})
				case strings.HasSuffix(r.URL.Path, "/commands"):
					n := 1
					if fault == "not queued" {
						n = 0
					}
					write(map[string]int{"Queued": n})
				case strings.HasSuffix(r.URL.Path, "/push"):
					write(map[string]bool{"Sent": true})
				case strings.HasSuffix(r.URL.Path, "/result"):
					queries := map[string]any{"SerialNumber": c.Serial, "OSVersion": "27.0", "BuildVersion": "27A100", "ProductName": "Mac16,1", "IsSupervised": true}
					switch fault {
					case "missing serial":
						delete(queries, "SerialNumber")
					case "wrong serial":
						queries["SerialNumber"] = "OTHER"
					case "missing OS":
						delete(queries, "OSVersion")
					case "missing build":
						delete(queries, "BuildVersion")
					case "wrong platform":
						queries["ProductName"] = "iPad16,1"
					case "missing supervision":
						delete(queries, "IsSupervised")
					case "false supervision":
						queries["IsSupervised"] = false
					case "DDM unsupported":
						queries["OSVersion"], queries["ProductName"] = "15.0", "AppleTV14,1"
					}
					b, err := plist.Marshal(map[string]any{"QueryResponses": queries})
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					status := "Acknowledged"
					if fault == "command error" {
						status = "Error"
					}
					write(map[string]any{"Status": status, "Response": b})
				default:
					// Command readiness is checked before queueing.
					write(map[string]any{"Enabled": true, "TokenUpdatedAt": time.Now()})
				}
			})
			c = configured
			c.DeviceID = "opaque-enrollment"
			switch fault {
			case "platform":
				c.Platform = "unknown"
			case "device address":
				c.DeviceID = ""
			case "target address":
				s.Info.UDID = "other-enrollment"
			case "DDM unsupported":
				c.Platform = "tvOS"
			}
			adeTestConfig(t, s, c)
			s.Env.DEP.ModifyDevice(c.Serial, func(d *dep.Device) {
				d.ProfileUUID, d.ProfileStatus = "P1", dep.ProfileStatusAssigned
				if fault == "no profile" {
					d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusEmpty
				}
				if fault == "profile state" {
					d.ProfileStatus = "UNKNOWN"
				}
			})
			err := liveADEHardware(t.Context(), s)
			if fault == "DDM unsupported" || fault == "false supervision" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("incomplete hardware proof accepted or leaked", err)
			}
			if fault == "DDM unexercised" && !errors.Is(err, ErrBlocked) {
				t.Fatal("missing DDM became hardware pass", err)
			}
			rows := adeTestEvidence(t, s, "hardware-*/platform-results.json")
			if len(rows) != 1 {
				t.Fatal("platform result evidence missing")
			}
			var report struct {
				Platforms map[string]adeHardwareResult `json:"platforms"`
			}
			if err := json.Unmarshal(rows[0], &report); err != nil {
				t.Fatal(err)
			}
			for platform, result := range report.Platforms {
				if platform != c.Platform && result.Status != "unexercised" {
					t.Fatal("other platform falsely exercised", platform, result)
				}
			}
			if fault == "DDM unsupported" && report.Platforms["tvOS"].DDM != "unsupported" {
				t.Fatal("unsupported DDM conflated with hardware")
			}
			if fault == "false supervision" && (report.Platforms["macOS"].Supervised == nil || *report.Platforms["macOS"].Supervised || report.Platforms["macOS"].DDM != "unsupported") {
				t.Fatal("native false supervision was replaced or unsupported scope claimed to pass")
			}
			if fault == "missing supervision" && (!errors.Is(err, ErrBlocked) || report.Platforms["macOS"].Supervised != nil) {
				t.Fatal("missing supervision became an affirmative capability", err)
			}
		})
	}
}

// TestADEAssignmentOptInPrecedesAllRequests protects ordinary all/theme runs.
func TestADEAssignmentOptInPrecedesAllRequests(t *testing.T) {
	requests := 0
	s, c := adeFailureSession(t, func(http.ResponseWriter, *http.Request) { requests++ })
	for _, flags := range [][2]bool{{false, false}, {false, true}, {true, false}} {
		c.Mutate, c.Quiescent = flags[0], flags[1]
		adeTestConfig(t, s, c)
		if err := liveADEAssignment(t.Context(), s); !errors.Is(err, ErrBlocked) {
			t.Fatal(err)
		}
	}
	if requests != 0 || len(s.Env.DEP.Requests()) != 0 {
		t.Fatal("non-opted-in run made requests")
	}
	if _, err := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("non-mutating run acquired a fence", err)
	}
}
