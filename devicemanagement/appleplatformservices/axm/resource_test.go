package axm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestInventoryResourceRawAndExtra checks raw resources and unknown attributes survive decoding.
func TestInventoryResourceRawAndExtra(t *testing.T) {
	raw := []byte(`{"id":"opaque","type":"orgDevices","futureResource":null,"attributes":{"serialNumber":"S","imei":["one","two"],"isMdmMigrationCapable":false,"future":null,"large":9007199254740993}}`)
	var d OrgDevice
	if e := json.Unmarshal(raw, &d); e != nil {
		t.Fatal(e)
	}
	if string(d.Raw) != string(raw) || len(d.Attributes.IMEI) != 2 || string(d.Attributes.Extra["large"]) != "9007199254740993" {
		t.Fatal("lost Apple fields")
	}
	b, e := json.Marshal(d.Attributes)
	if e != nil || !strings.Contains(string(b), `"future":null`) {
		t.Fatal(string(b), e)
	}
}

// TestNextPageCursorWithoutLink checks cursor-only pagination retains the originating endpoint.
func TestNextPageCursorWithoutLink(t *testing.T) {
	f := newFixture(t)
	f.srv.AddOrgDevice("A", nil)
	f.srv.AddOrgDevice("B", nil)
	c, e := New(t.Context(), f.cfg)
	if e != nil {
		t.Fatal(e)
	}
	page, e := c.ListOrgDevices(t.Context(), ListOptions{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	page.Links.Next = ""
	if !page.HasNext() {
		t.Fatal("cursor ignored")
	}
	next, e := NextPage(t.Context(), c, page)
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal(next, e)
	}
}

// TestInventoryResourcesRejectMalformedJSON ensures failed decoding preserves the previous resource.
func TestInventoryResourcesRejectMalformedJSON(t *testing.T) {
	for _, v := range []interface{ UnmarshalJSON([]byte) error }{&OrgDevice{}, &AppleCareCoverage{}, &MDMDevice{}, &MDMDeviceDetail{}, &MDMServer{}} {
		if err := v.UnmarshalJSON([]byte(`{"id":"before"}`)); err != nil {
			t.Fatal(err)
		}
		before, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.UnmarshalJSON([]byte(`{"id":true}`)); err == nil {
			t.Fatal("invalid resource accepted")
		}
		after, err := json.Marshal(v)
		if err != nil || string(after) != string(before) {
			t.Fatal("failed decode mutated resource", string(after), err)
		}
	}
	if _, err := marshalExtra(make(chan int), nil); err == nil {
		t.Fatal("unsupported value encoded")
	}
	if _, err := marshalExtra(42, nil); err == nil {
		t.Fatal("scalar accepted as attributes")
	}
	if _, err := json.Marshal(OrgDeviceAttributes{Extra: Extra{"future": json.RawMessage(`{`)}}); err == nil {
		t.Fatal("malformed unknown attribute accepted")
	}
}
