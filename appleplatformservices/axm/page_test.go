package axm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestPagination(t *testing.T) {
	t.Parallel()
	seed := func(t *testing.T, n int) *fixture {
		t.Helper()
		f := newFixture(t)
		for i := range n {
			f.srv.AddOrgDevice(fmt.Sprintf("SER%03d", i), nil)
		}
		return f
	}
	t.Run("FollowsLinksNext", func(t *testing.T) {
		t.Parallel()
		f := seed(t, 5)
		c := f.client(t, nil)
		first, err := c.ListOrgDevices(context.Background(), ListOptions{Limit: 2})
		if err != nil || len(first.Items) != 2 || !first.HasNext() {
			t.Fatalf("first page: %+v %v", first.Links, err)
		}
		all, err := All(context.Background(), c, first)
		if err != nil || len(all) != 5 || all[4].ID != "SER004" {
			t.Fatalf("All: %d items, %v", len(all), err)
		}
		if n := len(apiRequests(f.srv)); n != 3 {
			t.Fatalf("API requests %d, want 3 pages", n)
		}
		var seen []string
		err = Each(context.Background(), c, first, func(d OrgDevice) error {
			seen = append(seen, d.ID)
			return nil
		})
		if err != nil || len(seen) != 5 {
			t.Fatalf("Each: %v %v", seen, err)
		}
		stop := errors.New("stop")
		if err := Each(context.Background(), c, first, func(OrgDevice) error { return stop }); !errors.Is(err, stop) {
			t.Fatalf("Each must return the callback's error: %v", err)
		}
		pages := 0
		for range Pages(context.Background(), c, first) {
			pages++
			if pages == 2 {
				break
			}
		}
		if pages != 2 {
			t.Fatalf("break must stop Pages: %d", pages)
		}
		if _, err := NextPage(context.Background(), c, Page[OrgDevice]{}); !errors.Is(err, ErrNextLink) {
			t.Fatalf("no next: %v", err)
		}
	})
	t.Run("MergesQueryFromNext", func(t *testing.T) {
		t.Parallel()
		hits := 0
		var srv = stub(t, func(w http.ResponseWriter, r *http.Request) {
			hits++
			q := r.URL.Query()
			if hits == 1 {
				// Apple's next link without the fields selection.
				_, _ = fmt.Fprintf(w, `{"data":[{"type":"orgDevices","id":"A"}],"links":{"self":%q,"next":"/v1/orgDevices?cursor=1"}}`, "http://"+r.Host+r.URL.RequestURI())
				return
			}
			if hits == 2 && (q.Get("fields[orgDevices]") != "serialNumber" || q.Get("cursor") != "1" || q.Get("limit") != "1") {
				t.Errorf("second request query %v", q)
			}
			_, _ = w.Write([]byte(`{"data":[{"type":"orgDevices","id":"B"}],"links":{"self":"x"}}`))
		})
		c := stubClient(t, srv, nil)
		first, err := c.ListOrgDevices(context.Background(), ListOptions{Fields: []string{"serialNumber"}, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		all, err := All(context.Background(), c, first)
		if err != nil || len(all) != 2 || all[1].ID != "B" {
			t.Fatalf("%v %v", all, err)
		}
		// An absolute next link on another host is refused.
		first.Links.Next = "https://evil.example/v1/orgDevices?cursor=1"
		if _, err := NextPage(context.Background(), c, first); !errors.Is(err, ErrNextLink) {
			t.Fatalf("foreign host: %v", err)
		}
		first.Links.Next = "http://[::1]:x/"
		if _, err := NextPage(context.Background(), c, first); !errors.Is(err, ErrNextLink) {
			t.Fatalf("unparsable: %v", err)
		}
		first.Links.Next, first.Links.Self = "/v1/orgDevices?cursor=1", "http://[::1]:x/"
		if _, err := NextPage(context.Background(), c, first); err != nil {
			t.Fatalf("unparsable self is ignored: %v", err)
		}
	})
	t.Run("StopsWithoutNext", func(t *testing.T) {
		t.Parallel()
		f := seed(t, 2)
		c := f.client(t, nil)
		first, err := c.ListOrgDevices(context.Background(), ListOptions{Limit: 10})
		if err != nil || first.HasNext() {
			t.Fatalf("%+v %v", first.Links, err)
		}
		all, err := All(context.Background(), c, first)
		if err != nil || len(all) != 2 || len(apiRequests(f.srv)) != 1 {
			t.Fatalf("%d %v", len(all), err)
		}
	})
	t.Run("PageCap", func(t *testing.T) {
		t.Parallel()
		f := seed(t, 5)
		c := f.client(t, func(cfg *Config) { cfg.PageCap = 2 })
		first, err := c.ListOrgDevices(context.Background(), ListOptions{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		all, err := All(context.Background(), c, first)
		if !errors.Is(err, ErrPageCap) || len(all) != 4 {
			t.Fatalf("%d items, %v", len(all), err)
		}
		if err := Each(context.Background(), c, first, func(OrgDevice) error { return nil }); !errors.Is(err, ErrPageCap) {
			t.Fatalf("Each: %v", err)
		}
	})
	t.Run("RespectsContext", func(t *testing.T) {
		t.Parallel()
		f := seed(t, 5)
		c := f.client(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		first, err := c.ListOrgDevices(ctx, ListOptions{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		all, err := All(ctx, c, first)
		if !errors.Is(err, context.Canceled) || len(all) != 2 {
			t.Fatalf("%d items, %v", len(all), err)
		}
		// A failing page propagates too.
		f.srv.ServerError(10)
		c = f.client(t, func(cfg *Config) { cfg.Retry.Max = 0 })
		if _, err := All(context.Background(), c, first); !hasStatus(err, http.StatusServiceUnavailable) {
			t.Fatalf("page error: %v", err)
		}
	})
	t.Run("MetaPaging", func(t *testing.T) {
		t.Parallel()
		f := seed(t, 5)
		c := f.client(t, nil)
		first, err := c.ListOrgDevices(context.Background(), ListOptions{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if first.Meta.Paging.Total != 5 || first.Meta.Paging.Limit != 2 || first.Meta.Paging.NextCursor != "2" {
			t.Fatalf("meta %+v", first.Meta)
		}
		if first.Links.Self == "" || first.Links.Next == "" {
			t.Fatalf("links %+v", first.Links)
		}
		second, err := c.ListOrgDevices(context.Background(), ListOptions{Limit: 2, Cursor: first.Meta.Paging.NextCursor})
		if err != nil || second.Items[0].ID != "SER002" {
			t.Fatalf("cursor: %+v %v", second.Items, err)
		}
		last, err := NextPage(context.Background(), c, second)
		if err != nil || len(last.Items) != 1 || last.HasNext() || last.Meta.Paging.NextCursor != "" {
			t.Fatalf("last: %+v %v", last.Meta, err)
		}
	})
	t.Run("LimitValidated", func(t *testing.T) {
		t.Parallel()
		for _, n := range []int{-1, 1001, 5000} {
			if _, err := (ListOptions{Limit: n}).query("x"); !errors.Is(err, ErrLimit) {
				t.Errorf("limit %d: %v", n, err)
			}
		}
		for _, n := range []int{0, 1, 1000} {
			q, err := (ListOptions{Limit: n}).query("x")
			if err != nil {
				t.Errorf("limit %d: %v", n, err)
			}
			if n == 0 && q.Has("limit") {
				t.Error("limit 0 must be omitted")
			}
			if n > 0 && q.Get("limit") != strconv.Itoa(n) {
				t.Errorf("limit %d encoded as %q", n, q.Get("limit"))
			}
		}
	})
}

func TestDecode(t *testing.T) {
	t.Parallel()
	t.Run("ArraysForIMEIMEID", func(t *testing.T) {
		t.Parallel()
		body := `{"type":"orgDevices","id":"X","attributes":{"serialNumber":"X","imei":["123456789012345","123456789012346"],"meid":["12345678901237"],"ethernetMacAddress":["aa:bb"],"wifiMacAddress":"cc:dd","status":"UNASSIGNED","addedToOrgDateTime":"2025-04-30T22:05:14.192Z","color":"","futureField":{"a":1}}}`
		var d OrgDevice
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		a := d.Attributes
		if len(a.IMEI) != 2 || a.IMEI[1] != "123456789012346" || len(a.MEID) != 1 || len(a.EthernetMACAddress) != 1 || a.WiFiMACAddress != "cc:dd" {
			t.Fatalf("%+v", a)
		}
		if a.AddedToOrgDateTime.Year() != 2025 || a.AddedToOrgDateTime.Nanosecond() != 192_000_000 {
			t.Fatalf("time %v", a.AddedToOrgDateTime)
		}
		if string(a.Extra["futureField"]) != `{"a":1}` || len(a.Extra) != 1 {
			t.Fatalf("extra %v", a.Extra)
		}
		// A single string still decodes.
		var det MDMDeviceDetail
		if err := json.Unmarshal([]byte(`{"type":"mdmDeviceDetails","id":"Y","attributes":{"ethernetMacAddress":"aa:bb","imei":null}}`), &det); err != nil {
			t.Fatal(err)
		}
		if len(det.Attributes.EthernetMACAddress) != 1 || det.Attributes.IMEI != nil {
			t.Fatalf("%+v", det.Attributes)
		}
		var sl StringList
		if err := json.Unmarshal([]byte(`7`), &sl); !errors.Is(err, ErrDecode) {
			t.Fatalf("number: %v", err)
		}
		if err := sl.UnmarshalJSON([]byte(`"unterminated`)); !errors.Is(err, ErrDecode) {
			t.Fatalf("bad string: %v", err)
		}
		if err := json.Unmarshal([]byte(`{"attributes":5}`), &d); !errors.Is(err, ErrDecode) {
			t.Fatalf("bad attributes: %v", err)
		}
		if _, err := decodeWithExtra([]byte(`[1]`), &struct{}{}); !errors.Is(err, ErrDecode) {
			t.Fatalf("array: %v", err)
		}
	})
	t.Run("StatusEnumsPreserveUnknown", func(t *testing.T) {
		t.Parallel()
		var d OrgDevice
		if err := json.Unmarshal([]byte(`{"type":"orgDevices","id":"X","attributes":{"status":"ASSIGNED","purchaseSourceType":"APPLE","mdmMigrationStatus":"REQUESTED"}}`), &d); err != nil {
			t.Fatal(err)
		}
		if d.Attributes.Status != OrgDeviceStatusAssigned || d.Attributes.PurchaseSourceType != PurchaseSourceApple || d.Attributes.MDMMigrationStatus != MDMMigrationRequested {
			t.Fatalf("%+v", d.Attributes)
		}
		if err := json.Unmarshal([]byte(`{"type":"orgDevices","id":"X","attributes":{"status":"QUARANTINED"}}`), &d); err != nil {
			t.Fatal(err)
		}
		if d.Attributes.Status != "QUARANTINED" {
			t.Fatalf("unknown status lost: %q", d.Attributes.Status)
		}
		var act OrgDeviceActivity
		if err := json.Unmarshal([]byte(`{"type":"orgDeviceActivities","id":"a","attributes":{"status":"COMPLETED","subStatus":"COMPLETED_WITH_SUCCESS","newField":1}}`), &act); err != nil {
			t.Fatal(err)
		}
		if !act.Terminal() || !act.Succeeded() || len(act.Attributes.Extra) != 1 {
			t.Fatalf("%+v", act.Attributes)
		}
		act.Attributes.Status, act.Attributes.SubStatus = ActivityInProgress, ActivityProcessing
		if act.Terminal() || act.Succeeded() {
			t.Fatal("in progress")
		}
		act.Attributes.Status = "PAUSED"
		if act.Terminal() {
			t.Fatal("unknown status must not be terminal")
		}
		for _, s := range []ActivityStatus{ActivityStopped, ActivityFailed} {
			act.Attributes.Status = s
			if !act.Terminal() || act.Succeeded() {
				t.Fatalf("%s", s)
			}
		}
		// Every attributes type keeps unknown members.
		extraBody := `{"known":1,"unknownMember":"x"}`
		for name, v := range map[string]interface{ UnmarshalJSON([]byte) error }{
			"coverage": &AppleCareCoverageAttributes{}, "details": &MDMDeviceDetailAttributes{}, "server": &MDMServerAttributes{},
			"user": &UserAttributes{}, "group": &UserGroupAttributes{}, "unit": &OrganizationalUnitAttributes{},
			"app": &AppAttributes{}, "package": &PackageAttributes{}, "configuration": &ConfigurationAttributes{}, "blueprint": &BlueprintAttributes{},
		} {
			if err := v.UnmarshalJSON([]byte(extraBody)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			b, _ := json.Marshal(v)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if _, leaked := m["Extra"]; leaked {
				t.Errorf("%s: Extra must not marshal", name)
			}
		}
		var cov AppleCareCoverageAttributes
		_ = json.Unmarshal([]byte(extraBody), &cov)
		if string(cov.Extra["unknownMember"]) != `"x"` || string(cov.Extra["known"]) != "1" {
			t.Fatalf("%v", cov.Extra)
		}
	})
	t.Run("MDMServerID", func(t *testing.T) {
		t.Parallel()
		body := `{"data":{"type":"mdmServers","id":"1F97349736CF4614A94F624E705841AD","attributes":{"serverName":"Production MDM","serverType":"MDM","enableMdmDisownFlag":false,"defaultProductFamilies":["IPAD","IPHONE","MAC"],"status":"ACTIVE","deviceCount":128,"lastConnectedDateTime":"2026-06-01T08:14:23Z","lastConnectedIp":"203.0.113.5","createdDateTime":"2025-05-01T03:21:44Z","updatedDateTime":"2026-05-12T11:02:18Z"},"relationships":{"devices":{"links":{"self":"https://api-business.apple.com/v1/mdmServers/1F97349736CF4614A94F624E705841AD/relationships/devices"}}},"links":{"self":"https://api-business.apple.com/v1/mdmServers/1F97349736CF4614A94F624E705841AD"}},"links":{"self":"https://api-business.apple.com/v1/mdmServers/1F97349736CF4614A94F624E705841AD"}}`
		var doc single[MDMServer]
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatal(err)
		}
		s := doc.Data
		if len(s.ID) != 32 || s.ID != "1F97349736CF4614A94F624E705841AD" {
			t.Fatalf("id %q", s.ID)
		}
		if s.Attributes.ServerType != MDMServerTypeMDM || s.Attributes.Status != MDMServerActive || s.Attributes.DeviceCount != 128 || len(s.Attributes.DefaultProductFamilies) != 3 || s.Attributes.LastConnectedIP != "203.0.113.5" {
			t.Fatalf("%+v", s.Attributes)
		}
		if s.Relationships["devices"].Links.Self == "" || s.Links.Self == "" {
			t.Fatalf("links lost: %+v", s)
		}
		// The assigned-server linkage: a full id, an empty id, and null.
		for body, want := range map[string]string{
			`{"data":{"type":"mdmServers","id":"1F97349736CF4614A94F624E705841AD"},"links":{"self":"s","related":"r"}}`: "1F97349736CF4614A94F624E705841AD",
			`{"data":{"type":"mdmServers","id":""},"links":{"self":"s"}}`:                                               "",
			`{"data":null,"links":{"self":"s"}}`:                                                                        "",
		} {
			var link single[*Linkage]
			if err := json.Unmarshal([]byte(body), &link); err != nil {
				t.Fatal(err)
			}
			if link.Data != nil && link.Data.ID != want {
				t.Fatalf("%s: %q", body, link.Data.ID)
			}
		}
		// Audit event data decodes by its property key.
		var ev AuditEvent
		if err := json.Unmarshal([]byte(`{"type":"auditEvents","id":"e","attributes":{"eventDateTime":"2026-02-14T12:00:00Z","type":"DEVICE_ASSIGNED_TO_SERVER","eventDataPropertyKey":"eventDataDeviceAssignedToServer","eventDataDeviceAssignedToServer":{"serialNumber":"C02X","targetServerName":"Prod"}}}`), &ev); err != nil {
			t.Fatal(err)
		}
		var data AuditEventDeviceAssignedToServer
		if err := ev.Attributes.Data(&data); err != nil || data.SerialNumber != "C02X" || data.TargetServerName != "Prod" {
			t.Fatalf("%+v %v", data, err)
		}
		if err := ev.Attributes.Data(new(int)); !errors.Is(err, ErrDecode) {
			t.Fatalf("mismatched target: %v", err)
		}
		ev.Attributes.EventDataPropertyKey = ""
		if err := ev.Attributes.Data(&data); !errors.Is(err, ErrNoEventData) {
			t.Fatalf("no key: %v", err)
		}
		if ev.Attributes.EventDateTime != time.Date(2026, 2, 14, 12, 0, 0, 0, time.UTC) {
			t.Fatal(ev.Attributes.EventDateTime)
		}
	})
}
