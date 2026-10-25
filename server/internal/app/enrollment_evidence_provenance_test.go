package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// TestEnrollmentEvidenceReportsActualIssuanceMethod keeps profile-based,
// account-driven, User Enrollment and historical records distinct from ADE.
func TestEnrollmentEvidenceReportsActualIssuanceMethod(t *testing.T) {
	a, id := replacementSecurityApp(t)
	for _, method := range []correlation.Method{correlation.AutomatedDeviceEnrollment, correlation.ProfileBasedDeviceEnrollment, correlation.AccountDrivenDeviceEnrollment, correlation.UserEnrollment, ""} {
		t.Run(string(method), func(t *testing.T) {
			evidence := identityEvidence{EnrollmentMethod: method, UDID: id.ID, EnrollmentID: "opaque-binding", Serial: "ISSUED-SERIAL", Method: IdentitySCEP, NotAfter: time.Now().Add(time.Hour)}
			if method == correlation.UserEnrollment {
				evidence.UDID, evidence.Serial, evidence.EnrollmentID = "", "", id.ID
			}
			if method == "" {
				evidence.UDID, evidence.EnrollmentID, evidence.Serial = "", "", ""
			}
			b, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			key := "issued-identity:original-pin"
			if err = a.protocol.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: b}) }); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(t.Context(), "GET", "https://mdm.example/evidence", nil)
			r.SetPathValue("channel", "device")
			r.SetPathValue("id", id.ID)
			w := httptest.NewRecorder()
			a.enrollmentEvidence(w, r)
			var response struct {
				ID               string `json:"ID"`
				EnrollmentMethod string `json:"EnrollmentMethod"`
				Serial           string `json:"Serial"`
				UDID             string `json:"UDID"`
				EnrollmentID     string `json:"EnrollmentID"`
				Identity         string `json:"Identity"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			if response.ID != id.ID || response.Identity != IdentitySCEP || response.EnrollmentMethod != string(method) || response.Serial != evidence.Serial || response.UDID != evidence.UDID || response.EnrollmentID != evidence.EnrollmentID {
				t.Fatal("issuance facts were inferred or replaced", response)
			}
		})
	}
}

// TestEnrollmentEvidenceUsesExistingReadGrant checks the mounted HTTP route,
// scoped authorization and ordinary enrollment reads without exporting secrets.
func TestEnrollmentEvidenceUsesExistingReadGrant(t *testing.T) {
	a, id := replacementSecurityApp(t)
	if _, err := a.admin.PutRole(t.Context(), adminauth.Root, adminauth.Role{Name: "evidence-reader"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.admin.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "evidence-reader", Source: `permit(principal in MDM::Role::"evidence-reader", action == MDM::Action::"readEnrollment", resource == MDM::Enrollment::"device/device/");`}); err != nil {
		t.Fatal(err)
	}
	_, token, err := a.admin.CreatePrincipal(t.Context(), adminauth.Root, adminauth.Principal{Name: "reader", Roles: []string{"evidence-reader"}}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	_, denied, err := a.admin.CreatePrincipal(t.Context(), adminauth.Root, adminauth.Principal{Name: "no-grants"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, Serial: "ADE-SERIAL", UDID: id.ID, Method: IdentitySCEP, NotAfter: time.Now().Add(time.Hour)}
	b, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.protocol.Update(t.Context(), []string{"issued-identity:original-pin"}, func(tx state.Tx) error {
		return tx.Put(t.Context(), state.Record{Key: "issued-identity:original-pin", Value: b})
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, token string
		status            int
	}{
		{"evidence", "/admin/v1/enrollments/device/device/enrollment-evidence", string(token), 200},
		{"ordinary read", "/admin/v1/enrollments/device/device", string(token), 200},
		{"unauthenticated", "/admin/v1/enrollments/device/device/enrollment-evidence", "", 401},
		{"no implicit grant", "/admin/v1/enrollments/device/device/enrollment-evidence", string(denied), 403},
		{"wrong channel", "/admin/v1/enrollments/user/device/enrollment-evidence", string(token), 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "GET", "https://mdm.example"+test.path, nil)
			if test.token != "" {
				r.Header.Set("Authorization", "Bearer "+test.token)
			}
			w := httptest.NewRecorder()
			a.Handler.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("identity evidence may be cached")
			}
			for _, secret := range []string{string(token), string(denied), "issued-identity:", "consumer_secret", "access_secret", "PrivateKey"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("private credential exported")
				}
			}
			if test.name == "evidence" && !strings.Contains(w.Body.String(), `"EnrollmentMethod":"`+string(correlation.AutomatedDeviceEnrollment)+`"`) {
				t.Fatal("stored ADE provenance absent", w.Body.String())
			}
			if test.name == "ordinary read" && strings.Contains(w.Body.String(), "EnrollmentMethod") {
				t.Fatal("ordinary read gained protocol evidence")
			}
		})
	}
}
