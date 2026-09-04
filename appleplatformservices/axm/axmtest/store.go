package axmtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Resource type names.
const (
	typeOrgDevices          = "orgDevices"
	typeAppleCareCoverage   = "appleCareCoverage"
	typeMDMDevices          = "mdmDevices"
	typeMDMDeviceDetails    = "mdmDeviceDetails"
	typeMDMServers          = "mdmServers"
	typeOrgDeviceActivities = "orgDeviceActivities"
	typeUsers               = "users"
	typeUserGroups          = "userGroups"
	typeOrganizationalUnits = "organizationalUnits"
	typeApps                = "apps"
	typePackages            = "packages"
	typeConfigurations      = "configurations"
	typeBlueprints          = "blueprints"
	typeAuditEvents         = "auditEvents"
)

// fields lists the attribute names each type accepts in fields[type].
var fields = map[string][]string{
	typeOrgDevices: {
		"serialNumber", "addedToOrgDateTime", "releasedFromOrgDateTime", "updatedDateTime", "deviceModel",
		"productFamily", "productType", "deviceCapacity", "partNumber", "orderNumber", "color", "status",
		"orderDateTime", "imei", "meid", "eid", "wifiMacAddress", "bluetoothMacAddress", "ethernetMacAddress",
		"purchaseSourceId", "purchaseSourceType", "assignedServer", "appleCareCoverage",
		"isMdmMigrationCapable", "mdmMigrationStatus", "mdmMigrationDeadlineDateTime",
	},
	typeAppleCareCoverage: {
		"status", "paymentType", "description", "startDateTime", "endDateTime", "isRenewable", "isCanceled",
		"contractCancelDateTime", "agreementNumber",
	},
	typeMDMDevices: {"deviceName", "enrolledUserId", "productFamily", "serialNumber", "details"},
	typeMDMDeviceDetails: {
		"bluetoothMacAddress", "deviceEraseStatus", "deviceLockStatus", "deviceModel", "deviceName",
		"ethernetMacAddress", "imei", "isFileVaultEnabled", "isFirewallEnabled", "lastCheckInDateTime",
		"lostModeStatus", "meid", "osVersion", "platform", "serialNumber", "storageFreeCapacity",
		"storageTotalCapacity", "wifiMacAddress",
	},
	typeMDMServers: {
		"serverName", "serverType", "enableMdmDisownFlag", "defaultProductFamilies", "status", "deviceCount",
		"lastConnectedDateTime", "lastConnectedIp", "createdDateTime", "updatedDateTime", "devices",
	},
	typeOrgDeviceActivities: {"status", "subStatus", "createdDateTime", "completedDateTime", "downloadUrl", "activityType"},
	typeUsers: {
		"firstName", "lastName", "middleName", "status", "managedAppleAccount", "isExternalUser", "roleOuList",
		"email", "employeeNumber", "costCenter", "division", "department", "jobTitle", "startDateTime",
		"createdDateTime", "updatedDateTime", "phoneNumbers",
	},
	typeUserGroups:          {"ouId", "name", "type", "totalMemberCount", "status", "createdDateTime", "updatedDateTime", "users"},
	typeOrganizationalUnits: {"name", "description", "createdDateTime", "updatedDateTime", "users"},
	typeApps:                {"name", "bundleId", "websiteUrl", "version", "supportedOS", "isCustomApp", "appStoreUrl"},
	typePackages:            {"name", "url", "hash", "bundleIds", "description", "version", "createdDateTime", "updatedDateTime"},
	typeConfigurations:      {"type", "name", "configuredForPlatforms", "customSettingsValues", "createdDateTime", "updatedDateTime"},
	typeBlueprints: {
		"name", "description", "status", "appLicenseDeficient", "createdDateTime", "updatedDateTime",
		"apps", "configurations", "packages", "orgDevices", "users", "userGroups",
	},
	typeAuditEvents: {
		"eventDateTime", "type", "category", "actorType", "actorId", "actorName", "subjectType", "subjectId",
		"subjectName", "outcome", "groupId", "eventDataPropertyKey",
	},
}

// blueprintRelationships in Apple's order.
var blueprintRelationships = []string{"apps", "configurations", "packages", "orgDevices", "users", "userGroups"}

// relationshipTypes maps a blueprint relationship to its linkage type.
var relationshipTypes = map[string]string{
	"apps": typeApps, "configurations": typeConfigurations, "packages": typePackages,
	"orgDevices": typeOrgDevices, "users": typeUsers, "userGroups": typeUserGroups,
}

// resource is one JSON:API resource.
type resource struct {
	typ   string
	id    string
	attrs map[string]any
	// rels holds to-many relationships as id lists (blueprints, groups,
	// organizational units).
	rels map[string][]string
	// extra is per-type state: MDM device details, a device's coverages.
	extra map[string]any
}

// collection keeps resources in insertion order.
type collection struct {
	order []string
	items map[string]*resource
}

func newCollection() *collection { return &collection{items: map[string]*resource{}} }

func (c *collection) put(r *resource) {
	if _, ok := c.items[r.id]; !ok {
		c.order = append(c.order, r.id)
	}
	c.items[r.id] = r
}

func (c *collection) get(id string) (*resource, bool) {
	r, ok := c.items[id]
	return r, ok
}

func (c *collection) del(id string) {
	if _, ok := c.items[id]; !ok {
		return
	}
	delete(c.items, id)
	c.order = slices.DeleteFunc(c.order, func(s string) bool { return s == id })
}

func (c *collection) all() []*resource {
	out := make([]*resource, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.items[id])
	}
	return out
}

// assignment is a device's assignment to a server with when it becomes
// visible.
//
// Apple's assignment endpoints are eventually consistent, so a fake that
// answers immediately would let a client pass without ever polling. Two
// ways of expressing the delay are offered because they answer different
// questions. A wall-clock lag says how long convergence takes. A read
// count says how many reads see the old answer, and only that one is
// deterministic: a machine slow enough to spend the whole lag between the
// assignment and the first read would otherwise observe no delay at all.
type assignment struct {
	serverID  string
	visibleAt time.Time
	// readsLeft is how many more reads must see the assignment as not yet
	// visible, whatever the clock says.
	readsLeft int
}

// store is the organization's state.
type store struct {
	devices, mdmDevices, servers, users, groups, ous, apps, packages, configurations, blueprints, audits *collection
	assignments                                                                                          map[string]assignment
}

func newStore() *store {
	return &store{
		devices: newCollection(), mdmDevices: newCollection(), servers: newCollection(),
		users: newCollection(), groups: newCollection(), ous: newCollection(), apps: newCollection(),
		packages: newCollection(), configurations: newCollection(), blueprints: newCollection(), audits: newCollection(),
		assignments: map[string]assignment{},
	}
}

// byType returns the collection holding typ.
func (st *store) byType(typ string) *collection {
	switch typ {
	case typeOrgDevices:
		return st.devices
	case typeMDMDevices:
		return st.mdmDevices
	case typeMDMServers:
		return st.servers
	case typeUsers:
		return st.users
	case typeUserGroups:
		return st.groups
	case typeOrganizationalUnits:
		return st.ous
	case typeApps:
		return st.apps
	case typePackages:
		return st.packages
	case typeConfigurations:
		return st.configurations
	case typeBlueprints:
		return st.blueprints
	case typeAuditEvents:
		return st.audits
	}
	return nil
}

// normalize round-trips attrs through JSON so times and typed values
// become plain JSON values.
func normalize(attrs map[string]any) map[string]any {
	out := map[string]any{}
	if attrs == nil {
		return out
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

// merge returns defaults overlaid with attrs.
func merge(defaults, attrs map[string]any) map[string]any {
	out := normalize(defaults)
	maps.Copy(out, normalize(attrs))
	return out
}

// selectFields validates fields[typ] against the known names and returns
// the selection (nil when absent), or writes 400.
func (s *Server) selectFields(w http.ResponseWriter, r *http.Request, typ string) (map[string]struct{}, bool) {
	param := "fields[" + typ + "]"
	for name := range r.URL.Query() {
		if strings.HasPrefix(name, "fields[") && name != param {
			s.badParameter(w, name, "unknown resource type in fields selection")
			return nil, false
		}
	}
	raw := r.URL.Query().Get(param)
	if raw == "" {
		return nil, true
	}
	known := fields[typ]
	sel := map[string]struct{}{}
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if !slices.Contains(known, f) {
			s.badParameter(w, param, fmt.Sprintf("'%s' is not a valid field name for '%s'", f, typ))
			return nil, false
		}
		sel[f] = struct{}{}
	}
	return sel, true
}

// render turns a resource into its JSON:API form under the selection.
func (s *Server) render(r *resource, sel map[string]struct{}) map[string]any {
	attrs := map[string]any{}
	for k, v := range r.attrs {
		if sel == nil {
			attrs[k] = v
			continue
		}
		if _, ok := sel[k]; ok {
			attrs[k] = v
		}
	}
	out := map[string]any{"type": r.typ, "id": r.id, "attributes": attrs}
	self := s.URL + "/v1/" + r.typ + "/" + r.id
	out["links"] = map[string]any{"self": self}
	rels := map[string]any{}
	switch r.typ {
	case typeOrgDevices:
		rels["assignedServer"] = map[string]any{"links": map[string]any{
			"self": self + "/relationships/assignedServer", "related": self + "/assignedServer",
		}}
	case typeMDMServers:
		rels["devices"] = map[string]any{"links": map[string]any{"self": self + "/relationships/devices"}}
	case typeMDMDevices:
		rels["details"] = map[string]any{"links": map[string]any{"related": self + "/details"}}
	case typeUserGroups, typeOrganizationalUnits:
		rels["users"] = map[string]any{"links": map[string]any{"self": self + "/relationships/users"}}
	case typeBlueprints:
		for _, rel := range blueprintRelationships {
			rels[rel] = map[string]any{"links": map[string]any{
				"self": self + "/relationships/" + rel, "related": self + "/" + rel,
			}}
		}
	}
	if len(rels) > 0 {
		out["relationships"] = rels
	}
	return out
}

// renderAll renders a collection.
func (s *Server) renderAll(rs []*resource, sel map[string]struct{}) []any {
	out := make([]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, s.render(r, sel))
	}
	return out
}

// linkages renders {type, id} pairs.
func linkages(typ string, ids []string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"type": typ, "id": id})
	}
	return out
}

// serverDevices returns the serials whose assignment to serverID is
// visible now, in order.
func (s *Server) serverDevices(serverID string, now time.Time) []string {
	var out []string
	for _, id := range s.store.devices.order {
		if a, ok := s.store.assignments[id]; ok && a.serverID == serverID && a.visible(now) {
			out = append(out, id)
		}
	}
	return out
}

// assignedServer returns the visible assignment of serial.
func (s *Server) assignedServer(serial string, now time.Time) string {
	a, ok := s.store.assignments[serial]
	if !ok || !a.visible(now) {
		return ""
	}
	return a.serverID
}

// visible reports whether an assignment has converged.
func (a assignment) visible(now time.Time) bool {
	return a.readsLeft == 0 && !now.Before(a.visibleAt)
}

// takeLinkageRead consumes one of an assignment's inconsistent reads. Only
// the linkage endpoints call it, so the server's own bookkeeping does not
// spend a budget that exists to be observed by a client.
func (s *Server) takeLinkageRead(serial string) {
	if a, ok := s.store.assignments[serial]; ok && a.readsLeft > 0 {
		a.readsLeft--
		s.store.assignments[serial] = a
	}
}

// refreshDeviceCounts recomputes deviceCount on every server.
func (s *Server) refreshDeviceCounts(now time.Time) {
	for _, srv := range s.store.servers.all() {
		srv.attrs["deviceCount"] = len(s.serverDevices(srv.id, now))
	}
	for _, dev := range s.store.devices.all() {
		status := "UNASSIGNED"
		if s.assignedServer(dev.id, now) != "" {
			status = "ASSIGNED"
		}
		dev.attrs["status"] = status
	}
}
