//go:build acceptance || e2e

package acceptance

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/lab"
)

// Run the same HTTP contract against embedded and installed server binaries.
// No policy is sent to a physical device and no binary execution is attempted.
func TestBinaryValidationDelivery(t *testing.T) {
	for _, topology := range []string{"device-management"} {
		t.Run(topology, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			if err := lab.Init(dir, "simulated", "sqlite", "127.0.0.1:0", lab.AdapterProcess, nil); err != nil {
				t.Fatal(err)
			}
			w, err := lab.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			binary := os.Getenv("LAB_DMSERVER")
			e, err := lab.Start(ctx, w, binary, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			api := func(method, path string, body []byte, want int) []byte {
				t.Helper()
				base := e.URL
				out, code, err := lab.HTTP(ctx, e.Client, base, e.Token, method, path, bytes.NewReader(body))
				if code != want || (want < 400 && err != nil) {
					t.Fatalf("%s %s: HTTP %d, want %d: %v: %s", method, path, code, want, err, out)
				}
				return out
			}
			d := simulator.New("BINARY-VALIDATION-"+topology, simulator.WithClient(e.Client), simulator.WithDDM(map[string]any{}))
			d.OSVersion, d.SerialNumber = "27.0", "BENCH-APPROVED-SYNTHETIC"
			enrollment := []byte(fmt.Sprintf(`{"DeviceID":%q,"Serial":%q}`, d.UDID, d.SerialNumber))
			p := api("POST", "/enrollment-profiles", enrollment, http.StatusOK)
			if err := d.ApplyProfile(ctx, p, profile.ParseOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := d.Enroll(ctx); err != nil {
				t.Fatal(err)
			}
			devicePath := "/enrollments/device/" + d.UDID
			cmd, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion", "IsSupervised"}})
			if err != nil {
				t.Fatal(err)
			}
			d.Responder = func(c *mdm.Command) simulator.Reply {
				reply := simulator.AcknowledgeAll(c)
				if c.RequestType == "DeviceInformation" {
					reply.Payload = &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{OSVersion: new("27.0"), IsSupervised: new(true)}}
				}
				return reply
			}
			api("POST", devicePath+"/commands", cmd.Raw, http.StatusOK)
			got, err := d.Connect(ctx)
			if err != nil || len(got) != 1 || got[0].UUID != cmd.UUID {
				t.Fatal("tracked macOS27 inventory not delivered", got, err)
			}
			d.Responder = nil
			const identifier = "com.example.binary-validation"
			const payload = `{"Allowed":{"DeniedBinaries":[{"SigningID":"com.example.fixture","PathPrefix":"/Applications/Fixture.app"}]}}`
			declaration := func(p string) []byte {
				return []byte(fmt.Sprintf(`{"Type":"com.apple.configuration.app.settings","Identifier":%q,"Payload":%s}`, identifier, p))
			}
			api("PUT", "/declarations", declaration(payload), http.StatusOK)
			before := api("GET", "/declarations/"+identifier, nil, http.StatusOK)
			activation := []byte(fmt.Sprintf(`{"Type":"com.apple.activation.simple","Identifier":"com.example.binary-activation","Payload":{"StandardConfigurations":[%q]}}`, identifier))
			api("PUT", "/declarations", activation, http.StatusOK)
			for _, id := range []string{identifier, "com.example.binary-activation"} {
				api("PUT", "/sets/binary/declarations/"+id, nil, http.StatusOK)
			}
			api("PUT", devicePath+"/sets/binary", nil, http.StatusOK)
			if _, err := d.SyncDDM(ctx); err != nil {
				t.Fatal(err)
			}
			state := d.DDM()
			delivered := state.Declarations["configuration/"+identifier]
			var expected map[string]any
			if err := json.Unmarshal([]byte(payload), &expected); err != nil {
				t.Fatal(err)
			}
			if delivered == nil || !reflect.DeepEqual(expected, delivered.Payload) {
				t.Fatal("valid rule or qualifier lost in actual device delivery", delivered)
			}
			for _, invalid := range []string{
				`{"Allowed":{"AllowedBinaries":[{"SigningID":"com.example.fixture"}]}}`,
				`{"Allowed":{"AllowedBinaries":[{"PathPrefix":"/Applications/Fixture.app"}]}}`,
				`{"Allowed":{"DeniedBinaries":[{"SigningID":""}]}}`,
			} {
				api("PUT", "/declarations", declaration(invalid), http.StatusBadRequest)
			}
			after := api("GET", "/declarations/"+identifier, nil, http.StatusOK)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid replacement changed persisted declaration")
			}
			if _, err := d.SyncDDM(ctx); err != nil {
				t.Fatal(err)
			}
			if d.DDM().DeclarationsToken != state.DeclarationsToken {
				t.Fatal("invalid replacement changed the device manifest")
			}
			// Restart the actual runtime over the same disposable database.
			e.Close()
			restarted, err := lab.Start(ctx, w, binary, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			e = restarted
			if persisted := api("GET", "/declarations/"+identifier, nil, http.StatusOK); !bytes.Equal(before, persisted) {
				t.Fatal("restart did not preserve the accepted declaration")
			}
			api("DELETE", devicePath+"/sets/binary", nil, http.StatusOK)
			for _, id := range []string{identifier, "com.example.binary-activation"} {
				api("DELETE", "/sets/binary/declarations/"+id, nil, http.StatusOK)
				api("DELETE", "/declarations/"+id, nil, http.StatusNoContent)
				api("GET", "/declarations/"+id, nil, http.StatusNotFound)
			}
		})
	}
}
