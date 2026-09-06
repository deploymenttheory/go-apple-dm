package axm

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// seedAll fills the fake with one of everything.
func seedAll(t *testing.T, f *fixture) (serverID string) {
	t.Helper()
	s := f.srv
	serverID = s.AddMDMServer("Production MDM", nil)
	s.AddOrgDevice("SERIAL1", map[string]any{"imei": []string{"1", "2"}, "meid": []string{"3"}})
	s.AddOrgDevice("SERIAL2", nil)
	s.AddAppleCareCoverage("SERIAL1", "cov-1", nil)
	s.AddMDMDevice("MDMDEV1", nil, map[string]any{"imei": []string{"9"}})
	s.AddUser("u1", nil)
	s.AddUser("u2", nil)
	s.AddUserGroup("g1", nil, "u1", "u2")
	s.AddOrganizationalUnit("ou1", nil, "u1")
	s.AddApp("app1", nil)
	s.AddPackage("pkg1", nil)
	s.AddConfiguration("cfg1", nil)
	s.AddBlueprint("bp1", nil)
	s.LinkBlueprint("bp1", "apps", "app1")
	s.AddAuditEvent("ev1", map[string]any{"eventDateTime": time.Now().UTC(), "type": "DEVICE_ADDED_TO_ORG", "subjectId": "SERIAL1"})
	s.Assign("SERIAL2", serverID)
	return serverID
}

func TestEndpoints(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	serverID := seedAll(t, f)
	c := f.client(t, nil)
	ctx := context.Background()
	profile := base64.StdEncoding.EncodeToString([]byte("<plist/>"))
	deadline := time.Now().Add(24 * time.Hour)

	tests := []struct {
		name   string
		call   func() error
		method string
		path   string
		query  map[string]string
		body   func(t *testing.T, m map[string]any)
	}{
		{name: "ListOrgDevices", call: func() error {
			p, err := c.ListOrgDevices(ctx, ListOptions{Fields: []string{"serialNumber", "status"}, Limit: 10, Cursor: "0"})
			if err == nil && len(p.Items) != 2 {
				return errors.New("want 2 devices")
			}
			return err
		}, method: "GET", path: "/v1/orgDevices", query: map[string]string{"fields[orgDevices]": "serialNumber,status", "limit": "10", "cursor": "0"}},
		{name: "GetOrgDevice", call: func() error {
			d, err := c.GetOrgDevice(ctx, "SERIAL1", GetOptions{Fields: []string{"imei"}})
			if err == nil && (d.ID != "SERIAL1" || len(d.Attributes.IMEI) != 2) {
				return errors.New("bad device")
			}
			return err
		}, method: "GET", path: "/v1/orgDevices/SERIAL1", query: map[string]string{"fields[orgDevices]": "imei"}},
		{name: "ListAppleCareCoverage", call: func() error {
			p, err := c.ListAppleCareCoverage(ctx, "SERIAL1", ListOptions{Limit: 5})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Attributes.Status != AppleCareCoverageActive) {
				return errors.New("bad coverage")
			}
			return err
		}, method: "GET", path: "/v1/orgDevices/SERIAL1/appleCareCoverage", query: map[string]string{"limit": "5"}},
		{name: "ListMDMDevices", call: func() error {
			p, err := c.ListMDMDevices(ctx, ListOptions{Fields: []string{"serialNumber"}})
			if err == nil && len(p.Items) != 1 {
				return errors.New("want 1 mdm device")
			}
			return err
		}, method: "GET", path: "/v1/mdmDevices", query: map[string]string{"fields[mdmDevices]": "serialNumber"}},
		{name: "GetMDMDeviceDetails", call: func() error {
			d, err := c.GetMDMDeviceDetails(ctx, "MDMDEV1", GetOptions{Fields: []string{"imei", "osVersion"}})
			if err == nil && (d.Attributes.IMEI[0] != "9" || d.Attributes.OSVersion == "") {
				return errors.New("bad details")
			}
			return err
		}, method: "GET", path: "/v1/mdmDevices/MDMDEV1/details", query: map[string]string{"fields[mdmDeviceDetails]": "imei,osVersion"}},
		{name: "ListMDMServers", call: func() error {
			p, err := c.ListMDMServers(ctx, ListOptions{Limit: 1})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Attributes.DeviceCount != 1) {
				return errors.New("bad servers")
			}
			return err
		}, method: "GET", path: "/v1/mdmServers", query: map[string]string{"limit": "1"}},
		{name: "GetMDMServer", call: func() error {
			s, err := c.GetMDMServer(ctx, serverID, GetOptions{Fields: []string{"serverName"}})
			if err == nil && s.Attributes.ServerName != "Production MDM" {
				return errors.New("bad server")
			}
			return err
		}, method: "GET", path: "/v1/mdmServers/" + serverID, query: map[string]string{"fields[mdmServers]": "serverName"}},
		{name: "CreateMDMServer", call: func() error {
			s, err := c.CreateMDMServer(ctx, MDMServerCreateAttributes{ServerName: "New MDM", ServerCertificate: MDMServerCertificate{Name: "push.pem", Data: "Q0VSVA=="}, EnableMDMDisownFlag: true})
			if err == nil && (len(s.ID) != 32 || !s.Attributes.EnableMDMDisownFlag) {
				return errors.New("bad created server")
			}
			return err
		}, method: "POST", path: "/v1/mdmServers", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "type") != "mdmServers" || dig(t, m, "data", "attributes", "serverCertificate", "data") != "Q0VSVA==" {
				t.Errorf("body %v", m)
			}
		}},
		{name: "UpdateMDMServer", call: func() error {
			on := true
			s, err := c.UpdateMDMServer(ctx, serverID, MDMServerUpdateAttributes{ServerName: "Renamed", EnableMDMDisownFlag: &on, DefaultProductFamilies: []string{"MAC"}})
			if err == nil && (s.Attributes.ServerName != "Renamed" || !s.Attributes.EnableMDMDisownFlag) {
				return errors.New("bad updated server")
			}
			return err
		}, method: "PATCH", path: "/v1/mdmServers/" + serverID, body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "id") != serverID || dig(t, m, "data", "attributes", "enableMdmDisownFlag") != true {
				t.Errorf("body %v", m)
			}
		}},
		{name: "ListMDMServerDevices", call: func() error {
			p, err := c.ListMDMServerDevices(ctx, serverID, ListOptions{Limit: 100})
			if err == nil && (len(p.Items) != 1 || p.Items[0].ID != "SERIAL2" || p.Items[0].Type != TypeOrgDevices) {
				return errors.New("bad linkages")
			}
			return err
		}, method: "GET", path: "/v1/mdmServers/" + serverID + "/relationships/devices", query: map[string]string{"limit": "100"}},
		{name: "GetAssignedServerLinkage", call: func() error {
			l, err := c.GetAssignedServerLinkage(ctx, "SERIAL2")
			if err == nil && l.ID != serverID {
				return errors.New("bad linkage")
			}
			return err
		}, method: "GET", path: "/v1/orgDevices/SERIAL2/relationships/assignedServer"},
		{name: "GetAssignedServer", call: func() error {
			s, err := c.GetAssignedServer(ctx, "SERIAL2", GetOptions{Fields: []string{"serverName", "serverType"}})
			if err == nil && (s.ID != serverID || s.Attributes.ServerType != MDMServerTypeMDM) {
				return errors.New("bad assigned server")
			}
			return err
		}, method: "GET", path: "/v1/orgDevices/SERIAL2/assignedServer", query: map[string]string{"fields[mdmServers]": "serverName,serverType"}},
		{name: "CreateOrgDeviceActivity", call: func() error {
			req, err := NewActivityRequest(ActivityAssignDevices, serverID, []string{"SERIAL1"}, time.Time{}, time.Now())
			if err != nil {
				return err
			}
			req.Data.Type = ""
			a, err := c.CreateOrgDeviceActivity(ctx, req)
			if err == nil && (a.Attributes.Status != ActivityInProgress || a.Attributes.SubStatus != ActivitySubmitted || a.Attributes.ActivityType != ActivityAssignDevices) {
				return errors.New("bad activity")
			}
			return err
		}, method: "POST", path: "/v1/orgDeviceActivities", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "type") != "orgDeviceActivities" {
				t.Errorf("body %v", m)
			}
		}},
		{name: "GetOrgDeviceActivity", call: func() error {
			act, err := c.AssignDevices(ctx, serverID, []string{"SERIAL1"})
			if err != nil {
				return err
			}
			got, err := c.GetOrgDeviceActivity(ctx, act.ID, GetOptions{Fields: []string{"status", "subStatus"}})
			if err == nil && (got.ID != act.ID || got.Attributes.Status != ActivityInProgress) {
				return errors.New("bad activity")
			}
			return err
		}, method: "GET", path: "/v1/orgDeviceActivities/", query: map[string]string{"fields[orgDeviceActivities]": "status,subStatus"}},
		{name: "ListUsers", call: func() error {
			p, err := c.ListUsers(ctx, ListOptions{Fields: []string{"firstName", "status"}})
			if err == nil && (len(p.Items) != 2 || p.Items[0].Attributes.Status != UserActive) {
				return errors.New("bad users")
			}
			return err
		}, method: "GET", path: "/v1/users", query: map[string]string{"fields[users]": "firstName,status"}},
		{name: "GetUser", call: func() error {
			u, err := c.GetUser(ctx, "u1", GetOptions{})
			if err == nil && u.Attributes.ManagedAppleAccount != "u1@example.com" {
				return errors.New("bad user")
			}
			return err
		}, method: "GET", path: "/v1/users/u1"},
		{name: "ListUserGroups", call: func() error {
			p, err := c.ListUserGroups(ctx, ListOptions{Limit: 50})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Attributes.Type != UserGroupStandard) {
				return errors.New("bad groups")
			}
			return err
		}, method: "GET", path: "/v1/userGroups", query: map[string]string{"limit": "50"}},
		{name: "GetUserGroup", call: func() error {
			g, err := c.GetUserGroup(ctx, "g1", GetOptions{Fields: []string{"name"}})
			if err == nil && g.Attributes.Name != "Group g1" {
				return errors.New("bad group")
			}
			return err
		}, method: "GET", path: "/v1/userGroups/g1", query: map[string]string{"fields[userGroups]": "name"}},
		{name: "ListUserGroupUsers", call: func() error {
			p, err := c.ListUserGroupUsers(ctx, "g1", ListOptions{Limit: 1})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Type != TypeUsers || p.Links.Next == "") {
				return errors.New("bad group users")
			}
			return err
		}, method: "GET", path: "/v1/userGroups/g1/relationships/users", query: map[string]string{"limit": "1"}},
		{name: "ListOrganizationalUnits", call: func() error {
			p, err := c.ListOrganizationalUnits(ctx, ListOptions{})
			if err == nil && len(p.Items) != 1 {
				return errors.New("bad units")
			}
			return err
		}, method: "GET", path: "/v1/organizationalUnits"},
		{name: "GetOrganizationalUnit", call: func() error {
			u, err := c.GetOrganizationalUnit(ctx, "ou1", GetOptions{Fields: []string{"name", "description"}})
			if err == nil && u.Attributes.Name != "Unit ou1" {
				return errors.New("bad unit")
			}
			return err
		}, method: "GET", path: "/v1/organizationalUnits/ou1", query: map[string]string{"fields[organizationalUnits]": "name,description"}},
		{name: "ListOrganizationalUnitUsers", call: func() error {
			p, err := c.ListOrganizationalUnitUsers(ctx, "ou1", ListOptions{})
			if err == nil && (len(p.Items) != 1 || p.Items[0].ID != "u1") {
				return errors.New("bad unit users")
			}
			return err
		}, method: "GET", path: "/v1/organizationalUnits/ou1/relationships/users"},
		{name: "ListApps", call: func() error {
			p, err := c.ListApps(ctx, ListOptions{Fields: []string{"name", "bundleId"}, Limit: 1000})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Attributes.BundleID != "com.example.app1") {
				return errors.New("bad apps")
			}
			return err
		}, method: "GET", path: "/v1/apps", query: map[string]string{"fields[apps]": "name,bundleId", "limit": "1000"}},
		{name: "GetApp", call: func() error {
			a, err := c.GetApp(ctx, "app1", GetOptions{})
			if err == nil && (len(a.Attributes.SupportedOS) != 1 || a.Attributes.SupportedOS[0] != SupportedOSmacOS) {
				return errors.New("bad app")
			}
			return err
		}, method: "GET", path: "/v1/apps/app1"},
		{name: "ListPackages", call: func() error {
			p, err := c.ListPackages(ctx, ListOptions{Fields: []string{"name"}})
			if err == nil && len(p.Items) != 1 {
				return errors.New("bad packages")
			}
			return err
		}, method: "GET", path: "/v1/packages", query: map[string]string{"fields[packages]": "name"}},
		{name: "GetPackage", call: func() error {
			p, err := c.GetPackage(ctx, "pkg1", GetOptions{})
			if err == nil && len(p.Attributes.BundleIDs) != 1 {
				return errors.New("bad package")
			}
			return err
		}, method: "GET", path: "/v1/packages/pkg1"},
		{name: "ListConfigurations", call: func() error {
			p, err := c.ListConfigurations(ctx, ListOptions{Limit: 20})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Attributes.CustomSettingsValues != nil) {
				return errors.New("list must null customSettingsValues")
			}
			return err
		}, method: "GET", path: "/v1/configurations", query: map[string]string{"limit": "20"}},
		{name: "GetConfiguration", call: func() error {
			cfg, err := c.GetConfiguration(ctx, "cfg1", GetOptions{Fields: []string{"customSettingsValues", "type"}})
			if err == nil && (cfg.Attributes.CustomSettingsValues == nil || cfg.Attributes.Type != ConfigurationCustomSetting) {
				return errors.New("bad configuration")
			}
			return err
		}, method: "GET", path: "/v1/configurations/cfg1", query: map[string]string{"fields[configurations]": "customSettingsValues,type"}},
		{name: "CreateConfiguration", call: func() error {
			cfg, err := c.CreateConfiguration(ctx, ConfigurationCreateAttributes{Name: "Wi-Fi", CustomSettingsValues: CustomSettingsValues{ConfigurationProfile: profile, Filename: "wifi.mobileconfig"}})
			if err == nil && (cfg.Attributes.Name != "Wi-Fi" || cfg.Attributes.CustomSettingsValues.Filename != "wifi.mobileconfig") {
				return errors.New("bad created configuration")
			}
			return err
		}, method: "POST", path: "/v1/configurations", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "attributes", "type") != "CUSTOM_SETTING" || dig(t, m, "data", "attributes", "customSettingsValues", "configurationProfile") != profile {
				t.Errorf("body %v", m)
			}
		}},
		{name: "UpdateConfiguration", call: func() error {
			cfg, err := c.UpdateConfiguration(ctx, "cfg1", ConfigurationUpdateAttributes{Name: "Renamed", ConfiguredForPlatforms: []ConfigurationPlatform{PlatformIOS}})
			if err == nil && (cfg.Attributes.Name != "Renamed" || cfg.Attributes.ConfiguredForPlatforms[0] != PlatformIOS) {
				return errors.New("bad updated configuration")
			}
			return err
		}, method: "PATCH", path: "/v1/configurations/cfg1", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "id") != "cfg1" || dig(t, m, "data", "attributes", "name") != "Renamed" {
				t.Errorf("body %v", m)
			}
		}},
		{name: "ListBlueprints", call: func() error {
			p, err := c.ListBlueprints(ctx, BlueprintListOptions{ListOptions: ListOptions{Fields: []string{"name"}, Limit: 5}, BlueprintInclude: BlueprintInclude{Include: []BlueprintRelationship{BlueprintApps, BlueprintUsers}, Limits: map[BlueprintRelationship]int{BlueprintApps: 3}}})
			if err == nil && (len(p.Items) != 1 || len(p.Included) != 1 || p.Included[0].Type != TypeApps) {
				return errors.New("bad blueprints")
			}
			return err
		}, method: "GET", path: "/v1/blueprints", query: map[string]string{"fields[blueprints]": "name", "limit": "5", "include": "apps,users", "limit[apps]": "3"}},
		{name: "GetBlueprint", call: func() error {
			bp, err := c.GetBlueprint(ctx, "bp1", BlueprintGetOptions{BlueprintInclude: BlueprintInclude{Include: []BlueprintRelationship{BlueprintApps}, Limits: map[BlueprintRelationship]int{BlueprintApps: 1, BlueprintUsers: 2}}})
			if err == nil && (len(bp.Included) != 1 || bp.Included[0].ID != "app1" || bp.Attributes.Status != BlueprintActive) {
				return errors.New("bad blueprint")
			}
			return err
		}, method: "GET", path: "/v1/blueprints/bp1", query: map[string]string{"include": "apps", "limit[apps]": "1", "limit[users]": "2"}},
		{name: "CreateBlueprint", call: func() error {
			bp, err := c.CreateBlueprint(ctx, BlueprintCreateAttributes{Name: "Onboarding", Description: "d"}, BlueprintRequestRelationships{BlueprintApps: {Data: []Linkage{{Type: TypeApps, ID: "app1"}, {Type: TypeApps, ID: "missing"}}}})
			if err == nil && (bp.Attributes.Name != "Onboarding" || len(f.srv.BlueprintLinks(bp.ID, "apps")) != 1) {
				return errors.New("bad created blueprint")
			}
			return err
		}, method: "POST", path: "/v1/blueprints", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "attributes", "name") != "Onboarding" || len(dig(t, m, "data", "relationships", "apps", "data").([]any)) != 2 {
				t.Errorf("body %v", m)
			}
		}},
		{name: "UpdateBlueprint", call: func() error {
			bp, err := c.UpdateBlueprint(ctx, "bp1", &BlueprintUpdateAttributes{Name: "Renamed"}, nil)
			if err == nil && bp.Attributes.Name != "Renamed" {
				return errors.New("bad updated blueprint")
			}
			return err
		}, method: "PATCH", path: "/v1/blueprints/bp1", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "id") != "bp1" || dig(t, m, "data", "attributes", "name") != "Renamed" {
				t.Errorf("body %v", m)
			}
		}},
		{name: "ListBlueprintApps", call: func() error {
			p, err := c.ListBlueprintRelationship(ctx, "bp1", BlueprintApps, ListOptions{Limit: 10})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Type != TypeApps) {
				return errors.New("bad app linkages")
			}
			return err
		}, method: "GET", path: "/v1/blueprints/bp1/relationships/apps", query: map[string]string{"limit": "10"}},
		{name: "AddBlueprintUsers", call: func() error {
			return c.AddToBlueprint(ctx, "bp1", BlueprintUsers, []string{"u1", "u2"})
		}, method: "POST", path: "/v1/blueprints/bp1/relationships/users", body: func(t *testing.T, m map[string]any) {
			data := m["data"].([]any)
			if len(data) != 2 || dig(t, data[0], "type") != "users" || dig(t, data[1], "id") != "u2" {
				t.Errorf("body %v", m)
			}
		}},
		{name: "RemoveBlueprintUsers", call: func() error {
			if err := c.RemoveFromBlueprint(ctx, "bp1", BlueprintUsers, []string{"u1"}); err != nil {
				return err
			}
			if got := f.srv.BlueprintLinks("bp1", "users"); len(got) != 1 || got[0] != "u2" {
				return errors.New("user not removed")
			}
			return nil
		}, method: "DELETE", path: "/v1/blueprints/bp1/relationships/users", body: func(t *testing.T, m map[string]any) {
			if len(m["data"].([]any)) != 1 {
				t.Errorf("body %v", m)
			}
		}},
		{name: "ListBlueprintConfigurations", call: func() error {
			_, err := c.ListBlueprintRelationship(ctx, "bp1", BlueprintConfigurations, ListOptions{})
			return err
		}, method: "GET", path: "/v1/blueprints/bp1/relationships/configurations"},
		{name: "AddBlueprintConfigurations", call: func() error {
			return c.AddToBlueprint(ctx, "bp1", BlueprintConfigurations, []string{"cfg1"})
		}, method: "POST", path: "/v1/blueprints/bp1/relationships/configurations"},
		{name: "RemoveBlueprintConfigurations", call: func() error {
			return c.RemoveFromBlueprint(ctx, "bp1", BlueprintConfigurations, []string{"cfg1"})
		}, method: "DELETE", path: "/v1/blueprints/bp1/relationships/configurations"},
		{name: "ListBlueprintPackages", call: func() error {
			_, err := c.ListBlueprintRelationship(ctx, "bp1", BlueprintPackages, ListOptions{})
			return err
		}, method: "GET", path: "/v1/blueprints/bp1/relationships/packages"},
		{name: "AddBlueprintPackages", call: func() error {
			return c.AddToBlueprint(ctx, "bp1", BlueprintPackages, []string{"pkg1"})
		}, method: "POST", path: "/v1/blueprints/bp1/relationships/packages"},
		{name: "RemoveBlueprintPackages", call: func() error {
			return c.RemoveFromBlueprint(ctx, "bp1", BlueprintPackages, []string{"pkg1"})
		}, method: "DELETE", path: "/v1/blueprints/bp1/relationships/packages"},
		{name: "ListBlueprintOrgDevices", call: func() error {
			_, err := c.ListBlueprintRelationship(ctx, "bp1", BlueprintOrgDevices, ListOptions{})
			return err
		}, method: "GET", path: "/v1/blueprints/bp1/relationships/orgDevices"},
		{name: "AddBlueprintOrgDevices", call: func() error {
			return c.AddToBlueprint(ctx, "bp1", BlueprintOrgDevices, []string{"SERIAL1"})
		}, method: "POST", path: "/v1/blueprints/bp1/relationships/orgDevices"},
		{name: "RemoveBlueprintOrgDevices", call: func() error {
			return c.RemoveFromBlueprint(ctx, "bp1", BlueprintOrgDevices, []string{"SERIAL1"})
		}, method: "DELETE", path: "/v1/blueprints/bp1/relationships/orgDevices"},
		{name: "ListBlueprintUsers", call: func() error {
			_, err := c.ListBlueprintRelationship(ctx, "bp1", BlueprintUsers, ListOptions{})
			return err
		}, method: "GET", path: "/v1/blueprints/bp1/relationships/users"},
		{name: "ListBlueprintUserGroups", call: func() error {
			_, err := c.ListBlueprintRelationship(ctx, "bp1", BlueprintUserGroups, ListOptions{})
			return err
		}, method: "GET", path: "/v1/blueprints/bp1/relationships/userGroups"},
		{name: "AddBlueprintUserGroups", call: func() error {
			return c.AddToBlueprint(ctx, "bp1", BlueprintUserGroups, []string{"g1"})
		}, method: "POST", path: "/v1/blueprints/bp1/relationships/userGroups"},
		{name: "RemoveBlueprintUserGroups", call: func() error {
			return c.RemoveFromBlueprint(ctx, "bp1", BlueprintUserGroups, []string{"g1"})
		}, method: "DELETE", path: "/v1/blueprints/bp1/relationships/userGroups"},
		{name: "ListAuditEvents", call: func() error {
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			p, err := c.ListAuditEvents(ctx, AuditEventQuery{Start: start, End: start.Add(24 * 365 * time.Hour), SubjectID: "SERIAL1", Type: AuditDeviceAddedToOrg, ActorID: "system", Fields: []string{"type", "subjectId"}, Limit: 10})
			if err == nil && (len(p.Items) != 1 || p.Items[0].Attributes.Type != AuditDeviceAddedToOrg) {
				return errors.New("bad audit events")
			}
			return err
		}, method: "GET", path: "/v1/auditEvents", query: map[string]string{
			"filter[startTimestamp]": "2026-01-01T00:00:00Z", "filter[endTimestamp]": "2027-01-01T00:00:00Z",
			"filter[subjectId]": "SERIAL1", "filter[type]": "DEVICE_ADDED_TO_ORG", "filter[actorId]": "system",
			"fields[auditEvents]": "type,subjectId", "limit": "10",
		}},
		{name: "DeleteConfiguration", call: func() error { return c.DeleteConfiguration(ctx, "cfg1") }, method: "DELETE", path: "/v1/configurations/cfg1"},
		{name: "DeleteBlueprint", call: func() error { return c.DeleteBlueprint(ctx, "bp1") }, method: "DELETE", path: "/v1/blueprints/bp1"},
		{name: "DeleteMDMServer", call: func() error {
			id := f.srv.AddMDMServer("Empty", nil)
			return c.DeleteMDMServer(ctx, id)
		}, method: "DELETE", path: "/v1/mdmServers/"},
		{name: "AssignWithMigrationDeadline", call: func() error {
			_, err := c.AssignWithMigrationDeadline(ctx, serverID, []string{"SERIAL1"}, deadline)
			return err
		}, method: "POST", path: "/v1/orgDeviceActivities", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "attributes", "activityType") != "ASSIGN_DEVICES_WITH_MDM_MIGRATION_DEADLINE" {
				t.Errorf("body %v", m)
			}
			got := dig(t, m, "data", "attributes", "activityTypeMetadata", "mdmMigrationDeadlineDateTime").(string)
			if !strings.HasPrefix(got, deadline.UTC().Format("2006-01-02T15:04")) {
				t.Errorf("deadline %s", got)
			}
		}},
		{name: "UpdateMigrationDeadline", call: func() error {
			_, err := c.UpdateMigrationDeadline(ctx, []string{"SERIAL1"}, deadline)
			return err
		}, method: "POST", path: "/v1/orgDeviceActivities", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "attributes", "activityType") != "UPDATE_MDM_MIGRATION_DEADLINE" {
				t.Errorf("body %v", m)
			}
			if _, has := dig(t, m, "data", "relationships").(map[string]any)["mdmServer"]; has {
				t.Errorf("mdmServer must be absent: %v", m)
			}
		}},
		{name: "CancelMigration", call: func() error {
			_, err := c.CancelMigration(ctx, []string{"SERIAL1"})
			return err
		}, method: "POST", path: "/v1/orgDeviceActivities", body: func(t *testing.T, m map[string]any) {
			if dig(t, m, "data", "attributes", "activityType") != "CANCEL_MDM_MIGRATION" {
				t.Errorf("body %v", m)
			}
			if _, has := dig(t, m, "data", "attributes").(map[string]any)["activityTypeMetadata"]; has {
				t.Errorf("metadata must be absent: %v", m)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f.srv.Reset()
			if err := tc.call(); err != nil {
				t.Fatalf("call: %v", err)
			}
			last := f.srv.LastRequest()
			if last.Method != tc.method || !strings.HasPrefix(last.Path, tc.path) {
				t.Fatalf("got %s %s, want %s %s", last.Method, last.Path, tc.method, tc.path)
			}
			if last.Header.Get("Accept") != "application/json" {
				t.Errorf("Accept = %q", last.Header.Get("Accept"))
			}
			if ct := last.Header.Get("Content-Type"); (len(last.Body) > 0) != (ct == "application/json") {
				t.Errorf("Content-Type %q with body %d bytes", ct, len(last.Body))
			}
			if !strings.HasPrefix(last.Header.Get("Authorization"), "Bearer at-") {
				t.Errorf("Authorization = %q", last.Header.Get("Authorization"))
			}
			for k, v := range tc.query {
				if got := last.Query.Get(k); got != v {
					t.Errorf("query %s = %q, want %q (all %v)", k, got, v, last.Query)
				}
			}
			if len(last.Query) != len(tc.query) {
				t.Errorf("query %v, want exactly %v", last.Query, tc.query)
			}
			if tc.body != nil {
				tc.body(t, decodeBody(t, last))
			}
		})
	}
}

// TestEndpointsFailing covers the argument checks and API errors of every
// endpoint method.
func TestEndpointsFailing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.client(t, nil)
	ctx := context.Background()
	argument := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrArgument) {
			t.Errorf("%s: %v, want ErrArgument", name, err)
		}
	}
	limit := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrLimit) {
			t.Errorf("%s: %v, want ErrLimit", name, err)
		}
	}
	notFound := func(name string, err error) {
		t.Helper()
		if !IsNotFound(err) {
			t.Errorf("%s: %v, want 404", name, err)
		}
	}
	bad := ListOptions{Limit: 1001}

	_, err := c.GetOrgDevice(ctx, " ", GetOptions{})
	argument("GetOrgDevice", err)
	_, err = c.ListOrgDevices(ctx, bad)
	limit("ListOrgDevices", err)
	_, err = c.ListAppleCareCoverage(ctx, "", ListOptions{})
	argument("ListAppleCareCoverage", err)
	_, err = c.ListAppleCareCoverage(ctx, "X", bad)
	limit("ListAppleCareCoverage", err)
	_, err = c.ListAppleCareCoverage(ctx, "X", ListOptions{})
	notFound("ListAppleCareCoverage", err)
	_, err = c.ListMDMDevices(ctx, bad)
	limit("ListMDMDevices", err)
	_, err = c.GetMDMDeviceDetails(ctx, "", GetOptions{})
	argument("GetMDMDeviceDetails", err)
	_, err = c.GetMDMDeviceDetails(ctx, "nope", GetOptions{})
	notFound("GetMDMDeviceDetails", err)
	_, err = c.ListMDMServers(ctx, bad)
	limit("ListMDMServers", err)
	_, err = c.GetMDMServer(ctx, "", GetOptions{})
	argument("GetMDMServer", err)
	_, err = c.GetMDMServer(ctx, "nope", GetOptions{})
	notFound("GetMDMServer", err)
	_, err = c.CreateMDMServer(ctx, MDMServerCreateAttributes{ServerName: "x"})
	argument("CreateMDMServer", err)
	_, err = c.UpdateMDMServer(ctx, "", MDMServerUpdateAttributes{})
	argument("UpdateMDMServer", err)
	_, err = c.UpdateMDMServer(ctx, "nope", MDMServerUpdateAttributes{ServerName: "x"})
	notFound("UpdateMDMServer", err)
	argument("DeleteMDMServer", c.DeleteMDMServer(ctx, ""))
	notFound("DeleteMDMServer", c.DeleteMDMServer(ctx, "nope"))
	_, err = c.ListMDMServerDevices(ctx, "", ListOptions{})
	argument("ListMDMServerDevices", err)
	_, err = c.ListMDMServerDevices(ctx, "x", bad)
	limit("ListMDMServerDevices", err)
	_, err = c.ListMDMServerDevices(ctx, "nope", ListOptions{})
	notFound("ListMDMServerDevices", err)
	_, err = c.GetAssignedServerLinkage(ctx, "")
	argument("GetAssignedServerLinkage", err)
	_, err = c.GetAssignedServerLinkage(ctx, "nope")
	notFound("GetAssignedServerLinkage", err)
	_, err = c.GetAssignedServer(ctx, "", GetOptions{})
	argument("GetAssignedServer", err)
	_, err = c.GetAssignedServer(ctx, "nope", GetOptions{})
	notFound("GetAssignedServer", err)
	_, err = c.CreateOrgDeviceActivity(ctx, OrgDeviceActivityCreateRequest{})
	argument("CreateOrgDeviceActivity", err)
	_, err = c.GetOrgDeviceActivity(ctx, "", GetOptions{})
	argument("GetOrgDeviceActivity", err)
	_, err = c.GetOrgDeviceActivity(ctx, "nope", GetOptions{})
	notFound("GetOrgDeviceActivity", err)
	_, err = c.ListUsers(ctx, bad)
	limit("ListUsers", err)
	_, err = c.GetUser(ctx, "", GetOptions{})
	argument("GetUser", err)
	_, err = c.GetUser(ctx, "nope", GetOptions{})
	notFound("GetUser", err)
	_, err = c.ListUserGroups(ctx, bad)
	limit("ListUserGroups", err)
	_, err = c.GetUserGroup(ctx, "", GetOptions{})
	argument("GetUserGroup", err)
	_, err = c.ListUserGroupUsers(ctx, "", ListOptions{})
	argument("ListUserGroupUsers", err)
	_, err = c.ListUserGroupUsers(ctx, "x", bad)
	limit("ListUserGroupUsers", err)
	_, err = c.ListUserGroupUsers(ctx, "nope", ListOptions{})
	notFound("ListUserGroupUsers", err)
	_, err = c.ListOrganizationalUnits(ctx, bad)
	limit("ListOrganizationalUnits", err)
	_, err = c.GetOrganizationalUnit(ctx, "", GetOptions{})
	argument("GetOrganizationalUnit", err)
	_, err = c.ListOrganizationalUnitUsers(ctx, "", ListOptions{})
	argument("ListOrganizationalUnitUsers", err)
	_, err = c.ListOrganizationalUnitUsers(ctx, "x", bad)
	limit("ListOrganizationalUnitUsers", err)
	_, err = c.ListOrganizationalUnitUsers(ctx, "nope", ListOptions{})
	notFound("ListOrganizationalUnitUsers", err)
	_, err = c.ListApps(ctx, bad)
	limit("ListApps", err)
	_, err = c.GetApp(ctx, "", GetOptions{})
	argument("GetApp", err)
	_, err = c.GetApp(ctx, "nope", GetOptions{})
	notFound("GetApp", err)
	_, err = c.ListPackages(ctx, bad)
	limit("ListPackages", err)
	_, err = c.GetPackage(ctx, "", GetOptions{})
	argument("GetPackage", err)
	_, err = c.GetPackage(ctx, "nope", GetOptions{})
	notFound("GetPackage", err)
	_, err = c.ListConfigurations(ctx, bad)
	limit("ListConfigurations", err)
	_, err = c.GetConfiguration(ctx, "", GetOptions{})
	argument("GetConfiguration", err)
	_, err = c.GetConfiguration(ctx, "nope", GetOptions{})
	notFound("GetConfiguration", err)
	_, err = c.CreateConfiguration(ctx, ConfigurationCreateAttributes{Name: "x"})
	argument("CreateConfiguration", err)
	_, err = c.UpdateConfiguration(ctx, "", ConfigurationUpdateAttributes{Name: "x"})
	argument("UpdateConfiguration empty id", err)
	_, err = c.UpdateConfiguration(ctx, "x", ConfigurationUpdateAttributes{})
	argument("UpdateConfiguration no attrs", err)
	_, err = c.UpdateConfiguration(ctx, "nope", ConfigurationUpdateAttributes{Name: "x"})
	notFound("UpdateConfiguration", err)
	argument("DeleteConfiguration", c.DeleteConfiguration(ctx, ""))
	notFound("DeleteConfiguration", c.DeleteConfiguration(ctx, "nope"))
	_, err = c.ListBlueprints(ctx, BlueprintListOptions{ListOptions: bad})
	limit("ListBlueprints limit", err)
	_, err = c.ListBlueprints(ctx, BlueprintListOptions{BlueprintInclude: BlueprintInclude{Limits: map[BlueprintRelationship]int{BlueprintApps: 0}}})
	limit("ListBlueprints include limit", err)
	_, err = c.GetBlueprint(ctx, "", BlueprintGetOptions{})
	argument("GetBlueprint", err)
	_, err = c.GetBlueprint(ctx, "x", BlueprintGetOptions{BlueprintInclude: BlueprintInclude{Limits: map[BlueprintRelationship]int{BlueprintUsers: 5000}}})
	limit("GetBlueprint include limit", err)
	_, err = c.GetBlueprint(ctx, "nope", BlueprintGetOptions{})
	notFound("GetBlueprint", err)
	_, err = c.CreateBlueprint(ctx, BlueprintCreateAttributes{}, nil)
	argument("CreateBlueprint", err)
	_, err = c.UpdateBlueprint(ctx, "", &BlueprintUpdateAttributes{}, nil)
	argument("UpdateBlueprint empty id", err)
	_, err = c.UpdateBlueprint(ctx, "x", nil, nil)
	argument("UpdateBlueprint nothing", err)
	_, err = c.UpdateBlueprint(ctx, "nope", nil, BlueprintRequestRelationships{BlueprintApps: {}})
	notFound("UpdateBlueprint", err)
	argument("DeleteBlueprint", c.DeleteBlueprint(ctx, ""))
	notFound("DeleteBlueprint", c.DeleteBlueprint(ctx, "nope"))
	_, err = c.ListBlueprintRelationship(ctx, "", BlueprintApps, ListOptions{})
	argument("ListBlueprintRelationship empty id", err)
	_, err = c.ListBlueprintRelationship(ctx, "x", "bogus", ListOptions{})
	argument("ListBlueprintRelationship bogus rel", err)
	_, err = c.ListBlueprintRelationship(ctx, "x", BlueprintApps, bad)
	limit("ListBlueprintRelationship", err)
	_, err = c.ListBlueprintRelationship(ctx, "nope", BlueprintApps, ListOptions{})
	notFound("ListBlueprintRelationship", err)
	argument("AddToBlueprint empty id", c.AddToBlueprint(ctx, "", BlueprintApps, []string{"a"}))
	argument("AddToBlueprint bogus rel", c.AddToBlueprint(ctx, "x", "bogus", []string{"a"}))
	argument("AddToBlueprint no ids", c.AddToBlueprint(ctx, "x", BlueprintApps, nil))
	notFound("AddToBlueprint", c.AddToBlueprint(ctx, "nope", BlueprintApps, []string{"a"}))
	argument("RemoveFromBlueprint", c.RemoveFromBlueprint(ctx, "x", BlueprintApps, nil))
	notFound("RemoveFromBlueprint", c.RemoveFromBlueprint(ctx, "nope", BlueprintApps, []string{"a"}))
	_, err = c.ListAuditEvents(ctx, AuditEventQuery{})
	argument("ListAuditEvents missing range", err)
	_, err = c.ListAuditEvents(ctx, AuditEventQuery{Start: time.Now(), End: time.Now().Add(-time.Hour)})
	argument("ListAuditEvents reversed", err)
	_, err = c.ListAuditEvents(ctx, AuditEventQuery{Start: time.Now(), End: time.Now(), Limit: 0x7fff})
	limit("ListAuditEvents", err)

	f.srv.AddBlueprint("bp", nil)
	if err := c.AddToBlueprint(ctx, "bp", BlueprintApps, []string{"missing"}); !IsConflict(err) {
		t.Errorf("AddToBlueprint unknown app: %v, want 409", err)
	}
	srvID := f.srv.AddMDMServer("Busy", nil)
	f.srv.AddOrgDevice("D1", nil)
	f.srv.Assign("D1", srvID)
	if err := c.DeleteMDMServer(ctx, srvID); !IsConflict(err) {
		t.Errorf("DeleteMDMServer with devices: %v, want 409", err)
	}
	if _, err := c.ListOrgDevices(ctx, ListOptions{Fields: []string{"bogus"}}); err == nil || !hasStatus(err, http.StatusBadRequest) {
		t.Errorf("unknown field: %v, want 400", err)
	} else {
		var e *Error
		errors.As(err, &e)
		if e.Errors[0].Source == nil || e.Errors[0].Source.Parameter != "fields[orgDevices]" {
			t.Errorf("source = %+v", e.Errors[0].Source)
		}
	}
}
