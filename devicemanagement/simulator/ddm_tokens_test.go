package simulator

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddmproto"
)

// TestCommandTokenControlsSynchronizationRequests checks command token controls synchronization
// requests.
func TestCommandTokenControlsSynchronizationRequests(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       []string
	}{
		{"same", `{"SyncTokens":{"DeclarationsToken":"A"}}`, nil},
		{"different", `{"SyncTokens":{"DeclarationsToken":"B"}}`, []string{"declaration-items"}},
		{"absent", "", []string{"tokens", "declaration-items"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				var checkin map[string]any
				if err := plist.Unmarshal(body, &checkin); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				endpoint, _ := checkin["Endpoint"].(string)
				requests = append(requests, endpoint)
				switch endpoint {
				case "tokens":
					_, _ = w.Write([]byte(`{"SyncTokens":{"DeclarationsToken":"B"}}`))
				case "declaration-items":
					_, _ = w.Write([]byte(`{"DeclarationsToken":"B","Declarations":{}}`))
				default:
					t.Errorf("unexpected endpoint: %s", endpoint)
					w.WriteHeader(400)
				}
			}))
			defer srv.Close()
			d := New("token-test", WithURLs(srv.URL, srv.URL), WithDDM(nil), WithDDMFaults(DDMFaults{DropStatus: true}))
			ch := d.ddmChannel()
			ch.state.DeclarationsToken = "A"
			ch.state.Items = &ddmproto.DeclarationItemsResponse{DeclarationsToken: "A"}
			cmd, err := mdm.NewCommand(&commands.DeclarativeManagement{Data: []byte(tc.data)})
			if err != nil {
				t.Fatal(err)
			}
			if reply := ch.handleCommand(t.Context(), cmd, Reply{Status: mdm.StatusAcknowledged}); reply.Status != mdm.StatusAcknowledged {
				t.Fatal(reply)
			}
			if !slices.Equal(requests, tc.want) {
				t.Fatalf("requests=%v, want %v", requests, tc.want)
			}
		})
	}
}
