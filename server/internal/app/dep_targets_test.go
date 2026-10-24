package app_test

import (
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestDEPProfileChoicesAdmin exercises explicit default selection, independent
// definitions and serial override removal through authenticated admin routes.
func TestDEPProfileChoicesAdmin(t *testing.T) {
	fake := deptest.NewServer(deptest.Options{})
	t.Cleanup(fake.Close)
	store := depinmem.New()
	a := build(t, app.Config{Storage: "inmem", BootstrapToken: "t", DEP: app.DEPConfig{Store: store, BaseURL: fake.URL(), HTTPClient: fake.Client(), ProfileURL: "https://mdm.example/enroll"}})
	if _, err := a.DEP.StoreTokens(t.Context(), "account", fake.Tokens()); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, a)
	base := "/admin/v1/dep/accounts/account"
	var unselected, first, second dep.ProfileResponse
	decode(t, do(t, srv, "PUT", base+"/profile", "t", []byte(`{"profile_name":"unselected","org_magic":"unselected"}`)), http.StatusOK, &unselected)
	withoutDefault, err := store.GetAccount(t.Context(), "account")
	if err != nil || withoutDefault.ProfileUUID != "" {
		t.Fatal("definition selected an implicit default", err)
	}
	decode(t, do(t, srv, "PUT", base+"/profile?default=true", "t", []byte(`{"profile_name":"first","org_magic":"first"}`)), http.StatusOK, &first)
	decode(t, do(t, srv, "PUT", base+"/profile", "t", []byte(`{"profile_name":"second","org_magic":"second"}`)), http.StatusOK, &second)
	account, err := store.GetAccount(t.Context(), "account")
	if err != nil || account.ProfileUUID != first.ProfileUUID {
		t.Fatal("definition replaced default", err)
	}
	var profiles struct{ Items []dep.Profile }
	decode(t, do(t, srv, "GET", base+"/profiles", "t", nil), http.StatusOK, &profiles)
	if len(profiles.Items) != 3 {
		t.Fatal("definitions lost")
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"PUT", "/targets/SERIAL", `{"profile_uuid":"` + second.ProfileUUID + `"}`, http.StatusNoContent},
		{"PUT", "/targets/OTHER", `{"profile_uuid":"missing"}`, http.StatusNotFound},
		{"PUT", "/targets/OTHER", `{}`, http.StatusBadRequest},
		{"PUT", "/targets/OTHER", `invalid`, http.StatusBadRequest},
		{"PUT", "/default-profile", `{"profile_uuid":"missing"}`, http.StatusNotFound},
		{"PUT", "/profile?default=invalid", `{}`, http.StatusBadRequest},
		{"PUT", "/targets/%20SERIAL", `{"profile_uuid":"` + second.ProfileUUID + `"}`, http.StatusBadRequest},
	} {
		response := do(t, srv, tc.method, base+tc.path, "t", []byte(tc.body))
		_ = response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s %s = %d", tc.method, tc.path, response.StatusCode)
		}
	}
	var targets struct {
		Default  string
		Serials  map[string]string
		Revision int64
	}
	decode(t, do(t, srv, "GET", base+"/targets", "t", nil), http.StatusOK, &targets)
	if targets.Default != first.ProfileUUID || targets.Serials["SERIAL"] != second.ProfileUUID {
		t.Fatal("target inspection lost choices")
	}
	response := do(t, srv, "DELETE", base+"/targets/SERIAL", "t", nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	account, err = store.GetAccount(t.Context(), "account")
	if err != nil || account.DesiredProfile("SERIAL") != first.ProfileUUID {
		t.Fatal("default fallback", err)
	}
	response = do(t, srv, "DELETE", base+"/default-profile", "t", nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	account, err = store.GetAccount(t.Context(), "account")
	if err != nil || account.DesiredProfile("SERIAL") != "" {
		t.Fatal("default removal", err)
	}
	response = do(t, srv, "PUT", base+"/default-profile", "t", []byte(`{"profile_uuid":"`+second.ProfileUUID+`"}`))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	if fake.Count(http.MethodDelete, dep.PathProfileDevs) != 0 || fake.Count(http.MethodPost, dep.PathDisown) != 0 {
		t.Fatal("local choices mutated Apple state")
	}
}
