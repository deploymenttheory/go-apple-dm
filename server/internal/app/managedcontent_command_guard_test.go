package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// TestManagedContentGenericCommandOwnershipHTTP proves that ordinary command
// administration respects SDK-owned command identities and asset scopes while
// retaining access to independent assets and device queries.
func TestManagedContentGenericCommandOwnershipHTTP(t *testing.T) {
	a, token, f, id, _, plan := contentBoundaryBench(t)
	dispatch := contentBoundaryStep(t, a, token, "guard-owned", plan)
	requests, mutations := f.requests.Load(), f.posts.Load()
	request := func(command *mdm.Command, expected int) {
		t.Helper()
		before, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/v1/enrollments/device/"+id.ID+"/commands", bytes.NewReader(command.Raw))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatal(command.RequestType, w.Code, expected, w.Body.String())
		}
		after, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		want := len(before.Items)
		if expected < 300 {
			want++
		}
		if len(after.Items) != want {
			t.Fatal("command guard returned without the required queue outcome", len(after.Items), want)
		}
	}
	// A non-install command still cannot reuse the workflow's immutable UUID.
	reused, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}}, mdm.WithUUID(dispatch.ID))
	if err != nil {
		t.Fatal(err)
	}
	request(reused, http.StatusConflict)
	for _, tc := range []struct {
		name    string
		payload commands.Command
		status  int
	}{
		{"owned-app", &commands.InstallApplication{ITunesStoreID: new(int64(1))}, http.StatusConflict},
		{"cross-kind-owned-asset", &commands.InstallMedia{ITunesStoreID: new(int64(1)), MediaType: "Book"}, http.StatusConflict},
		{"independent-app", &commands.InstallApplication{ITunesStoreID: new(int64(2))}, http.StatusOK},
		{"independent-book", &commands.InstallMedia{ITunesStoreID: new(int64(3)), MediaType: "Book"}, http.StatusOK},
		{"native-query", &commands.DeviceInformation{Queries: []string{"OSVersion"}}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, err := mdm.NewCommand(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			request(command, tc.status)
		})
	}
	if f.requests.Load() != requests || f.posts.Load() != mutations {
		t.Fatal("generic native administration contacted Apple services")
	}
	_, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "guard-owned")
	if err != nil || evidence.Dispatch == nil || evidence.Dispatch.ID != dispatch.ID || evidence.Dispatch.Digest != dispatch.Digest {
		t.Fatal("generic commands rewrote immutable SDK dispatch", err)
	}
}
