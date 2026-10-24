package axm

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestOpaqueDeviceResourceAddressing uses independent paged responses with distinct IDs and serials.
func TestOpaqueDeviceResourceAddressing(t *testing.T) {
	t.Parallel()
	var posted bool
	srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/orgDevices":
			if r.URL.Query().Get("cursor") == "next" {
				_, _ = w.Write([]byte(`{"data":[{"type":"orgDevices","id":"opaque-B","attributes":{"serialNumber":"SERIAL-B"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"type":"orgDevices","id":"opaque-A","attributes":{"serialNumber":"SERIAL-A"}}],"links":{"next":"/v1/orgDevices?cursor=next"}}`))
		case "/v1/orgDeviceActivities":
			var req OrgDeviceActivityCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			ids := req.Data.Relationships.Devices.Data
			if len(ids) != 2 || ids[0].ID != "opaque-B" || ids[1].ID != "opaque-A" {
				t.Errorf("posted physical serials: %+v", ids)
			}
			posted = true
			_, _ = w.Write([]byte(`{"data":{"id":"activity","type":"orgDeviceActivities"}}`))
		case "/v1/orgDevices/opaque-A/relationships/assignedServer":
			_, _ = w.Write([]byte(`{"data":{"id":"server","type":"mdmServers"}}`))
		case "/v1/orgDevices/opaque-A":
			_, _ = w.Write([]byte(`{"data":{"id":"opaque-A","type":"orgDevices","attributes":{"serialNumber":"SERIAL-A"}}}`))
		default:
			t.Errorf("incorrect resource path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := stubClient(t, srv, nil)
	resources, err := c.ResolveOrgDevicesBySerial(t.Context(), []string{"SERIAL-B", "SERIAL-A"})
	if err != nil || resources[0].ID != "opaque-B" {
		t.Fatalf("%+v %v", resources, err)
	}
	if _, err := c.AssignDevices(t.Context(), "server", []string{"SERIAL-B", "SERIAL-A"}); err != nil || !posted {
		t.Fatalf("assign: %v", err)
	}
	if err := c.WaitForAssignedServer(t.Context(), "SERIAL-A", "server", time.Second); err != nil {
		t.Fatal(err)
	}
	if d, err := c.GetOrgDevice(t.Context(), resources[1].ID, GetOptions{}); err != nil || d.Attributes.SerialNumber != "SERIAL-A" {
		t.Fatalf("%+v %v", d, err)
	}
}

// TestSerialResolutionFailsClosed rejects partial, missing and ambiguous source evidence before mutation.
func TestSerialResolutionFailsClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body string
		status     int
		cap        int
		want       error
	}{
		{name: "absent", body: `{"data":[]}`, want: ErrDeviceSerial},
		{name: "no inferred serial", body: `{"data":[{"id":"SERIAL","attributes":{}}]}`, want: ErrDeviceSerial},
		{name: "no id", body: `{"data":[{"attributes":{"serialNumber":"SERIAL"}}]}`, want: ErrDeviceSerial},
		{name: "ambiguous", body: `{"data":[{"id":"one","attributes":{"serialNumber":"SERIAL"}},{"id":"two","attributes":{"serialNumber":"SERIAL"}}]}`, want: ErrDeviceSerial},
		{name: "later failure", body: `{"data":[{"id":"one","attributes":{"serialNumber":"SERIAL"}}],"links":{"next":"/v1/orgDevices?cursor=fail"}}`, status: http.StatusForbidden},
		{name: "page cap", body: `{"data":[{"id":"one","attributes":{"serialNumber":"SERIAL"}}],"links":{"next":"/v1/orgDevices?cursor=fail"}}`, cap: 1, want: ErrPageCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("mutation after failed resolution")
				}
				if r.URL.Query().Get("cursor") != "" {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			})
			c := stubClient(t, srv, func(cfg *Config) { cfg.PageCap = tc.cap })
			got, err := c.ResolveOrgDevicesBySerial(t.Context(), []string{"SERIAL"})
			if got != nil || err == nil {
				t.Fatalf("%+v %v", got, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if tc.status != 0 && !hasStatus(err, tc.status) {
				t.Fatal(err)
			}
			if _, err := c.AssignDevices(t.Context(), "server", []string{"SERIAL"}); err == nil {
				t.Fatal("mutation succeeded")
			}
		})
	}
	f := newFixture(t)
	c := f.client(t, nil)
	for _, serials := range [][]string{nil, {""}, {" "}, {"S", "S"}} {
		if _, err := c.ResolveOrgDevicesBySerial(t.Context(), serials); !errors.Is(err, ErrArgument) {
			t.Fatalf("%v: %v", serials, err)
		}
	}
	if len(f.srv.Requests()) != 0 {
		t.Fatal("invalid input sent to API")
	}
	if err := c.WaitForAssignedServer(t.Context(), "missing", "server", time.Second); err == nil || !strings.Contains(err.Error(), ErrDeviceSerial.Error()) {
		t.Fatal(err)
	}
}

// TestOpaqueIDPathEscaping ensures reserved ID characters stay in one path segment.
func TestOpaqueIDPathEscaping(t *testing.T) {
	t.Parallel()
	srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/orgDevices/opaque%2FA%25%23%3F" || r.URL.RawQuery != "" {
			t.Errorf("incorrect resource addressing: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"opaque/A%#?","attributes":{"serialNumber":"SERIAL"}}}`))
	})
	c := stubClient(t, srv, nil)
	if device, err := c.GetOrgDevice(t.Context(), "opaque/A%#?", GetOptions{}); err != nil || device.ID != "opaque/A%#?" {
		t.Fatalf("%+v %v", device, err)
	}
	if _, err := c.url(request{path: "%zz"}); !errors.Is(err, ErrArgument) {
		t.Fatal(err)
	}
}
