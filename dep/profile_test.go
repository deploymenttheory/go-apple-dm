package dep_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/deptest"
)

func TestProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("RoundTripByteStable", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("S1"))
		p := deptest.SampleProfile("")
		p.SkipSetupItems = []string{"Siri", "Zoom", "AppleID"}
		resp, err := f.client.DefineProfile(ctx, acct, p)
		if err != nil {
			t.Fatal(err)
		}
		if resp.ProfileUUID == "" || p.ProfileUUID != resp.ProfileUUID || resp.Devices["S1"] != dep.StatusSuccess {
			t.Fatalf("define: %+v", resp)
		}
		fetched, err := f.client.FetchProfile(ctx, acct, resp.ProfileUUID)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := dep.Marshal(p)
		got, _ := dep.Marshal(fetched)
		if !bytes.Equal(got, want) {
			t.Fatalf("fetched profile differs\n got %s\nwant %s", got, want)
		}
		snapshot := fetched.Clone() // DefineProfile below stamps the new UUID on fetched
		// Defining the fetched profile again sends exactly the bytes the
		// first definition sent (plus the UUID Apple assigned).
		f.srv.ResetRequests()
		if _, err := f.client.DefineProfile(ctx, acct, fetched); err != nil {
			t.Fatal(err)
		}
		var body []byte
		for _, r := range f.srv.Requests() {
			if r.Path == dep.PathProfile {
				body = r.Body
			}
		}
		if !bytes.Equal(body, want) {
			t.Fatalf("second define body\n got %s\nwant %s", body, want)
		}
		// Key order is declaration order with Extra sorted last, so the
		// same value always produces the same bytes.
		again, _ := dep.Marshal(snapshot.Clone())
		if !bytes.Equal(again, got) {
			t.Fatalf("Marshal is not stable across Clone\n got %s\nagain %s", got, again)
		}
		if !strings.HasPrefix(string(got), `{"profile_uuid":"`+resp.ProfileUUID+`","profile_name":"Corporate","url":`) || !strings.HasSuffix(string(got), `,"unknown_flag":true}`) {
			t.Fatalf("key order: %s", got)
		}
		// Explicit false survives; absent stays absent.
		if fetched.AllowPairing == nil || *fetched.AllowPairing || fetched.IsMDMRemovable == nil || *fetched.IsMDMRemovable {
			t.Fatalf("flags: %+v", fetched)
		}
		minimal := &dep.Profile{ProfileName: "m", URL: "https://x.example", OrgMagic: "m"}
		raw, _ := dep.Marshal(minimal)
		if string(raw) != `{"profile_name":"m","url":"https://x.example","org_magic":"m"}` {
			t.Fatalf("minimal: %s", raw)
		}
	})

	t.Run("UnknownKeysPreserved", func(t *testing.T) {
		t.Parallel()
		src := `{"profile_name":"n","url":"https://mdm.example.com","org_magic":"m","is_supervised":true,"future_bool":true,"future_obj":{"a":[1,2,{"b":null}]},"future_str":"s"}`
		var p dep.Profile
		if err := dep.Unmarshal([]byte(src), &p); err != nil {
			t.Fatal(err)
		}
		if p.Extra["future_str"] != "s" || p.Extra["future_bool"] != true || p.Extra["future_obj"] == nil || len(p.Extra) != 3 {
			t.Fatalf("Extra: %+v", p.Extra)
		}
		out, _ := dep.Marshal(p)
		for _, key := range []string{`"future_bool":true`, `"future_obj":{"a":[1,2,{"b":null}]}`, `"future_str":"s"`} {
			if !strings.Contains(string(out), key) {
				t.Errorf("missing %s in %s", key, out)
			}
		}
		// The fake keeps them across define and fetch.
		f := newFixture(t)
		resp, err := f.client.DefineProfile(ctx, acct, &p)
		if err != nil {
			t.Fatal(err)
		}
		back, err := f.client.FetchProfile(ctx, acct, resp.ProfileUUID)
		if err != nil {
			t.Fatal(err)
		}
		if back.Extra["future_str"] != "s" || len(back.Extra) != 3 {
			t.Fatalf("fetched Extra: %+v", back.Extra)
		}
		// Devices and account detail keep unknown keys too.
		var d dep.Device
		if err := dep.Unmarshal([]byte(`{"serial_number":"S","new_key":"v","device_assigned_date":""}`), &d); err != nil {
			t.Fatal(err)
		}
		if d.Extra["new_key"] != "v" || d.DeviceAssignedDate != nil && !d.DeviceAssignedDate.IsZero() {
			t.Fatalf("device: %+v", d)
		}
		var a dep.AccountDetail
		if err := dep.Unmarshal([]byte(`{"org_name":"o","urls":[{"uri":"/x","limit":{"default":1,"maximum":2,"soft":3},"future":1}],"unknown":[]}`), &a); err != nil {
			t.Fatal(err)
		}
		if a.Extra["unknown"] == nil || a.URLs[0].Extra["future"] == nil || a.URLs[0].Limit.Extra["soft"] == nil || a.Limits()["/x"].Maximum != 2 {
			t.Fatalf("account detail: %+v", a)
		}
		if err := dep.Unmarshal([]byte(`{"serial_number":"S","op_date":"yesterday"}`), &d); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("bad timestamp: %v", err)
		}
		if err := dep.Unmarshal([]byte(`{"serial_number":"S","op_date":5}`), &d); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("numeric timestamp: %v", err)
		}
		if err := dep.Unmarshal([]byte(`{"serial_number":"S","op_date":`), &d); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("truncated: %v", err)
		}
		if _, err := dep.Marshal(make(chan int)); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("unmarshalable: %v", err)
		}
	})

	t.Run("FlagsInvalidLocally", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		p := &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m", IsMDMRemovable: dep.Bool(false)}
		_, err := f.client.DefineProfile(ctx, acct, p)
		var pe *dep.ProfileError
		if !errors.As(err, &pe) || pe.Code != dep.CodeFlagsInvalid || !errors.Is(err, dep.ErrProfileInvalid) {
			t.Fatalf("err = %v", err)
		}
		if len(f.srv.Requests()) != 0 {
			t.Fatal("an invalid profile reached the service")
		}
		p.IsSupervised = dep.Bool(false)
		if err := p.Validate(); !errors.As(err, &pe) || pe.Code != dep.CodeFlagsInvalid {
			t.Fatalf("supervised=false: %v", err)
		}
		p.IsSupervised = dep.Bool(true)
		if err := p.Validate(); err != nil {
			t.Fatalf("supervised: %v", err)
		}
		// The fake applies the same rule for a caller that bypasses Validate.
		f.srv.Script(dep.PathProfile, deptest.Scripted{Status: 400, Code: dep.CodeFlagsInvalid})
		_, err = f.client.DefineProfile(ctx, acct, p)
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Code != dep.CodeFlagsInvalid {
			t.Fatalf("service FLAGS_INVALID: %v", err)
		}
	})

	t.Run("SkipKeysValidated", func(t *testing.T) {
		t.Parallel()
		keys := dep.SkipKeys()
		if !slices.IsSorted(keys) || !slices.Contains(keys, "Siri") || !slices.Contains(keys, "iCloudStorage") || len(keys) < 30 {
			t.Fatalf("SkipKeys: %v", keys)
		}
		p := &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com", OrgMagic: "m", SkipSetupItems: keys}
		if err := p.Validate(); err != nil {
			t.Fatalf("every generated key valid: %v", err)
		}
		p.SkipSetupItems = []string{"Siri", "NotAPane"}
		var pe *dep.ProfileError
		if err := p.Validate(); !errors.As(err, &pe) || pe.Code != dep.CodeSkipKeyInvalid || !strings.Contains(pe.Detail, "NotAPane") {
			t.Fatalf("unknown pane: %v", err)
		}
		f := newFixture(t)
		if _, err := f.client.DefineProfile(ctx, acct, p); !errors.Is(err, dep.ErrProfileInvalid) {
			t.Fatalf("define: %v", err)
		}
	})

	t.Run("LengthRules", func(t *testing.T) {
		t.Parallel()
		long := func(n int) string { return strings.Repeat("é", n) }
		base := func() *dep.Profile {
			return &dep.Profile{ProfileName: "n", URL: "https://mdm.example.com/enroll", OrgMagic: "m"}
		}
		cases := []struct {
			name string
			edit func(*dep.Profile)
			code string
		}{
			{"nil", nil, dep.CodeConfigNameRequired},
			{"name empty", func(p *dep.Profile) { p.ProfileName = "" }, dep.CodeConfigNameInvalid},
			{"name long", func(p *dep.Profile) { p.ProfileName = long(126) }, dep.CodeConfigNameInvalid},
			{"url empty", func(p *dep.Profile) { p.URL = "" }, dep.CodeConfigURLInvalid},
			{"url relative", func(p *dep.Profile) { p.URL = "/enroll" }, dep.CodeConfigURLInvalid},
			{"url unparsable", func(p *dep.Profile) { p.URL = "http://[::1" }, dep.CodeConfigURLInvalid},
			{"url long", func(p *dep.Profile) { p.URL = "https://x.example/" + strings.Repeat("a", 2000) }, dep.CodeConfigURLInvalid},
			{"magic empty", func(p *dep.Profile) { p.OrgMagic = "" }, dep.CodeMagicInvalid},
			{"magic long", func(p *dep.Profile) { p.OrgMagic = long(257) }, dep.CodeMagicInvalid},
			{"department long", func(p *dep.Profile) { p.Department = long(126) }, dep.CodeDepartmentInvalid},
			{"email long", func(p *dep.Profile) { p.SupportEmailAddress = long(251) }, dep.CodeSupportEmailInvalid},
			{"phone long", func(p *dep.Profile) { p.SupportPhoneNumber = long(51) }, dep.CodeSupportPhoneInvalid},
		}
		for _, tc := range cases {
			var p *dep.Profile
			if tc.edit != nil {
				p = base()
				tc.edit(p)
			}
			err := p.Validate()
			var pe *dep.ProfileError
			if !errors.As(err, &pe) || pe.Code != tc.code {
				t.Errorf("%s: got %v, want %s", tc.name, err, tc.code)
			}
		}
		ok := base()
		ok.ProfileName, ok.OrgMagic, ok.Department, ok.SupportEmailAddress, ok.SupportPhoneNumber = long(125), long(256), long(125), long(250), long(50)
		ok.URL = "https://x.example/" + strings.Repeat("a", 1981)
		if err := ok.Validate(); err != nil {
			t.Fatalf("at the limits: %v", err)
		}
	})
}
