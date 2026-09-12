package axmtest

import (
	"time"
)

// AddMDMServer adds a device management service named name and returns
// its id (32 upper-case hex characters). attrs overrides the defaults
// (serverType MDM, status ACTIVE, enableMdmDisownFlag false).
func (s *Server) AddMDMServer(name string, attrs map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	id := randomHex(16)
	s.store.servers.put(&resource{typ: typeMDMServers, id: id, attrs: merge(map[string]any{
		"serverName": name, "serverType": "MDM", "enableMdmDisownFlag": false, "status": "ACTIVE",
		"deviceCount": 0, "createdDateTime": now, "updatedDateTime": now,
	}, attrs)})
	return id
}

// AddOrgDevice adds a device by serial number; attrs overrides the
// defaults (a Mac, UNASSIGNED, added now).
func (s *Server) AddOrgDevice(serial string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.store.devices.put(&resource{typ: typeOrgDevices, id: serial, attrs: merge(map[string]any{
		"serialNumber": serial, "addedToOrgDateTime": now, "updatedDateTime": now, "deviceModel": "MacBook Pro",
		"productFamily": "Mac", "productType": "Mac16,1", "deviceCapacity": "512GB", "partNumber": "MW2U3LL/A",
		"orderNumber": "ORDER-" + serial, "color": "SPACE BLACK", "status": "UNASSIGNED",
		"orderDateTime": now.Add(-30 * 24 * time.Hour), "imei": []string{}, "meid": []string{},
		"ethernetMacAddress": []string{}, "purchaseSourceId": "PS-1", "purchaseSourceType": "APPLE",
		"isMdmMigrationCapable": true,
	}, attrs), extra: map[string]any{}})
}

// AddAppleCareCoverage adds a coverage resource to a device.
func (s *Server) AddAppleCareCoverage(serial, id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, ok := s.store.devices.get(serial)
	if !ok {
		return
	}
	now := s.now().UTC()
	cov, _ := dev.extra["coverage"].([]*resource)
	cov = append(cov, &resource{typ: typeAppleCareCoverage, id: id, attrs: merge(map[string]any{
		"status": "ACTIVE", "paymentType": "NONE", "description": "Limited Warranty",
		"startDateTime": now, "endDateTime": now.Add(365 * 24 * time.Hour), "isRenewable": false, "isCanceled": false,
	}, attrs)})
	dev.extra["coverage"] = cov
}

// AddMDMDevice adds a device enrolled in the built-in device management
// with its details.
func (s *Server) AddMDMDevice(id string, attrs, details map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &resource{typ: typeMDMDevices, id: id, attrs: merge(map[string]any{
		"deviceName": "Device " + id, "productFamily": "Mac", "serialNumber": id,
	}, attrs), extra: map[string]any{}}
	r.extra["details"] = merge(map[string]any{
		"serialNumber": id, "deviceName": "Device " + id, "deviceModel": "MacBook Pro", "platform": "macOS",
		"osVersion": "26.0", "deviceEraseStatus": "NOT_ERASED", "deviceLockStatus": "UNLOCKED",
		"lostModeStatus": "DISABLED", "imei": []string{}, "meid": []string{}, "ethernetMacAddress": []string{},
		"isFileVaultEnabled": true, "isFirewallEnabled": true, "lastCheckInDateTime": s.now().UTC(),
		"storageFreeCapacity": 100_000_000_000, "storageTotalCapacity": 512_000_000_000,
	}, details)
	s.store.mdmDevices.put(r)
}

// AddUser adds a user.
func (s *Server) AddUser(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.store.users.put(&resource{typ: typeUsers, id: id, attrs: merge(map[string]any{
		"firstName": "User", "lastName": id, "status": "ACTIVE", "managedAppleAccount": id + "@example.com",
		"isExternalUser": false, "createdDateTime": now, "updatedDateTime": now,
	}, attrs)})
}

// AddUserGroup adds a user group holding userIDs.
func (s *Server) AddUserGroup(id string, attrs map[string]any, userIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.store.groups.put(&resource{typ: typeUserGroups, id: id, attrs: merge(map[string]any{
		"name": "Group " + id, "type": "STANDARD", "status": "ACTIVE", "totalMemberCount": len(userIDs),
		"createdDateTime": now, "updatedDateTime": now,
	}, attrs), rels: map[string][]string{"users": append([]string(nil), userIDs...)}})
}

// AddOrganizationalUnit adds an organizational unit holding userIDs.
func (s *Server) AddOrganizationalUnit(id string, attrs map[string]any, userIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.store.ous.put(&resource{typ: typeOrganizationalUnits, id: id, attrs: merge(map[string]any{
		"name": "Unit " + id, "description": "", "createdDateTime": now, "updatedDateTime": now,
	}, attrs), rels: map[string][]string{"users": append([]string(nil), userIDs...)}})
}

// AddApp adds an app.
func (s *Server) AddApp(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.apps.put(&resource{typ: typeApps, id: id, attrs: merge(map[string]any{
		"name": "App " + id, "bundleId": "com.example." + id, "version": "1.0",
		"supportedOS": []string{"SUPPORTED_OS_MACOS"}, "isCustomApp": false,
		"appStoreUrl": "https://apps.apple.com/app/id" + id,
	}, attrs)})
}

// AddPackage adds a package.
func (s *Server) AddPackage(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.store.packages.put(&resource{typ: typePackages, id: id, attrs: merge(map[string]any{
		"name": "Package " + id, "url": "https://example.com/" + id + ".pkg", "hash": randomHex(16),
		"bundleIds": []string{"com.example." + id}, "version": "1.0", "createdDateTime": now, "updatedDateTime": now,
	}, attrs)})
}

// AddConfiguration adds a configuration; profile is the base64 profile of
// a CUSTOM_SETTING configuration (may be empty for other types).
func (s *Server) AddConfiguration(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.store.configurations.put(&resource{typ: typeConfigurations, id: id, attrs: merge(map[string]any{
		"type": "CUSTOM_SETTING", "name": "Configuration " + id, "configuredForPlatforms": []string{"PLATFORM_MACOS"},
		"customSettingsValues": map[string]any{"configurationProfile": "", "filename": id + ".mobileconfig"},
		"createdDateTime":      now, "updatedDateTime": now,
	}, attrs)})
}

// AddBlueprint adds a blueprint.
func (s *Server) AddBlueprint(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	rels := map[string][]string{}
	for _, rel := range blueprintRelationships {
		rels[rel] = nil
	}
	s.store.blueprints.put(&resource{typ: typeBlueprints, id: id, attrs: merge(map[string]any{
		"name": "Blueprint " + id, "description": "", "status": "ACTIVE", "appLicenseDeficient": false,
		"createdDateTime": now, "updatedDateTime": now,
	}, attrs), rels: rels})
}

// LinkBlueprint links ids to a blueprint through rel without checks.
func (s *Server) LinkBlueprint(id, rel string, ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, ok := s.store.blueprints.get(id)
	if !ok {
		return
	}
	bp.rels[rel] = append(bp.rels[rel], ids...)
}

// BlueprintLinks returns the ids linked to a blueprint through rel.
func (s *Server) BlueprintLinks(id, rel string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, ok := s.store.blueprints.get(id)
	if !ok {
		return nil
	}
	return append([]string(nil), bp.rels[rel]...)
}

// AddAuditEvent adds an audit event; attrs must carry eventDateTime and
// type, the rest defaults.
func (s *Server) AddAuditEvent(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addAuditLocked(id, attrs)
}

func (s *Server) addAuditLocked(id string, attrs map[string]any) {
	s.store.audits.put(&resource{typ: typeAuditEvents, id: id, attrs: merge(map[string]any{
		"eventDateTime": s.now().UTC(), "type": "DEVICE_ADDED_TO_ORG", "category": "DEVICE_INVENTORY",
		"actorType": "SYSTEM", "actorId": "system", "subjectType": "DEVICE", "outcome": "SUCCESS",
	}, attrs)})
}

// Assign records serial as assigned to serverID at once, bypassing the
// activity engine. An empty serverID unassigns.
func (s *Server) Assign(serial, serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.store.assignments[serial] = assignment{serverID: serverID, visibleAt: now}
	s.refreshDeviceCounts(now)
}

// AssignedServer returns the server serial is visibly assigned to, or "".
func (s *Server) AssignedServer(serial string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assignedServer(serial, s.now())
}

// UnassignedLinkage404 makes the relationships/assignedServer endpoint
// answer 404 for an unassigned device instead of 200 with an empty id.
// Apple has been observed doing both.
func (s *Server) UnassignedLinkage404(on bool) {
	s.mu.Lock()
	s.unassigned404 = on
	s.mu.Unlock()
}

// HasOrgDevice reports whether serial is still in the organization.
func (s *Server) HasOrgDevice(serial string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.store.devices.get(serial)
	return ok
}

// DeviceAttribute returns one attribute of a device (nil when absent).
func (s *Server) DeviceAttribute(serial, name string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, ok := s.store.devices.get(serial)
	if !ok {
		return nil
	}
	return dev.attrs[name]
}
