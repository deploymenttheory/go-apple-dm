package app

import (
	"slices"
	"strings"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

const (
	ActionListEnrollments       = "listEnrollments"
	ActionReadRawCommandResult  = "readRawCommandResult"
	ActionEnqueueUnknownCommand = "enqueueUnknownCommand"
	ActionReadPushCerts         = "readPushCertificates"
	ActionReadAppPush           = "readAppPushCredentials"
	ActionReadProfiles          = "readConfigurationProfiles"
	ActionDownloadProfile       = "downloadConfigurationProfile"
	ActionUploadProfile         = "uploadConfigurationProfile"
	ActionListBlueprints        = "listBlueprints"
	ActionDeleteBlueprint       = "deleteBlueprint"
	ActionEditSet               = "editDeclarationSet"
	ActionReadPrincipals        = "readPrincipals"
	ActionReadRoles             = "readRoles"
	ActionManageRoles           = "manageRoles"
	ActionReadPolicies          = "readPolicies"
	ActionReadDEP               = "readDEP"
	ActionListDEP               = "listDEPAccounts"
	ActionReadBusinessMgr       = "readBusinessManager"
	ActionAssignBusinessMgr     = "assignBusinessManagerDevices"
	ActionUnassignBusinessMgr   = "unassignBusinessManagerDevices"
)

// AdminActions is the complete permission catalogue. Membership in a routine
// action group is explicit; new permissions never inherit a routine grant.
func AdminActions() []adminauth.Action {
	actions := baseAdminActions()
	add := func(id, help string, resource types.EntityType) {
		actions = append(actions, adminauth.Action{ID: id, Help: help, Resource: resource})
	}
	add(ActionReadInventory, "Read agentless device inventory, reports and exports.", adminauth.EntitySystem)
	add(ActionReadRawInventory, "Read complete Apple inventory responses, including sensitive fields.", adminauth.EntitySystem)
	add(ActionManageInventory, "Collect inventory and manage sync jobs and schedules.", adminauth.EntitySystem)
	add(ActionManageAxMAccounts, "Manage Apple Business or School Manager connections and keys.", adminauth.EntitySystem)
	add(ActionManageSensitiveWebhooks, "Manage webhook destinations that receive sensitive events.", adminauth.EntitySystem)
	add(ActionReplaySensitiveWebhooks, "Replay or retry sensitive webhook events.", adminauth.EntitySystem)
	add(ActionListEnrollments, "List fleet enrollment metadata.", adminauth.EntitySystem)
	add(ActionReadRawCommandResult, "Read a raw command response, which may contain device secrets.", adminauth.EntityEnrollment)
	add(ActionEnqueueUnknownCommand, "Send an unrecognized raw MDM command. No routine role grants this permission.", adminauth.EntityEnrollment)
	add(ActionReadPushCerts, "Read push certificate topics and expiry.", adminauth.EntitySystem)
	add(ActionReadAppPush, "Read app push credential metadata.", adminauth.EntitySystem)
	add(ActionReadProfiles, "Read configuration profile metadata.", adminauth.EntitySystem)
	add(ActionDownloadProfile, "Download a configuration profile, which may embed secrets.", adminauth.EntityConfigurationProfile)
	add(ActionUploadProfile, "Upload an immutable configuration profile.", adminauth.EntitySystem)
	add(ActionListBlueprints, "List authored blueprints.", adminauth.EntitySystem)
	add(ActionDeleteBlueprint, "Delete a blueprint and its declaration set.", adminauth.EntityBlueprint)
	add(ActionEditSet, "Edit the declarations belonging to a set.", adminauth.EntitySet)
	add(ActionReadPrincipals, "Read administrative principal metadata.", adminauth.EntitySystem)
	add(ActionReadRoles, "Read roles and their descriptions.", adminauth.EntitySystem)
	add(ActionManageRoles, "Administer roles. Requires root independently of Cedar.", adminauth.EntitySystem)
	add(ActionReadPolicies, "Read stored Cedar policies.", adminauth.EntitySystem)
	add(ActionListDEP, "List device enrollment service accounts.", adminauth.EntitySystem)
	add("manageDEPCredentials", "Create and import device enrollment service credentials.", adminauth.EntityDEPAccount)
	add("manageDEPProfiles", "Configure an automated enrollment profile.", adminauth.EntityDEPAccount)
	add("syncDEPDevices", "Synchronize automated enrollment devices.", adminauth.EntityDEPAccount)
	add("readConfigurationProfile", "Read one configuration profile metadata record.", adminauth.EntityConfigurationProfile)
	add(ActionReadDEP, "Read devices in a device enrollment service account.", adminauth.EntityDEPAccount)
	add(ActionReadBusinessMgr, "Read Apple Business Manager hardware, servers, and activities.", adminauth.EntitySystem)
	add(ActionAssignBusinessMgr, "Assign Apple Business Manager hardware to a server.", adminauth.EntitySystem)
	add(ActionUnassignBusinessMgr, "Unassign Apple Business Manager hardware.", adminauth.EntitySystem)
	for _, id := range commands.IDs() {
		add("enqueueCommand."+id, "Send the "+id+" command to a device.", adminauth.EntityEnrollment)
	}
	viewer := []string{ActionListEnrollments, ActionReadEnrollment, ActionReadEnrollmentStatus, ActionReadCommands, ActionReadPushCerts, ActionReadAppPush, ActionReadProfiles, "readConfigurationProfile", ActionReadCertificates, ActionReadACME}
	operator := []string{ActionPushEnrollment, "enqueueCommand.DeviceInformation", "enqueueCommand.SecurityInfo", "enqueueCommand.ProfileList", "enqueueCommand.InstalledApplicationList", "enqueueCommand.CertificateList"}
	administrator := []string{ActionPutDeclaration, ActionDeleteDeclaration, ActionEditSet, ActionAssignSet, ActionNotify, ActionPublishBlueprints, ActionDeleteBlueprint, ActionAssignBlueprint, ActionUploadProfile, ActionDiscoverApplicationIdentities}
	auditor := []string{ActionReadAudit, ActionReadRoles, ActionReadPrincipals, ActionReadPolicies}
	sensitive := []string{ActionReadRawInventory, ActionManageAxMAccounts, ActionReadRawCommandResult, ActionDownloadProfile, ActionExportEnrollments, ActionImportEnrollments, ActionEnqueueUnknownCommand, ActionManagePushCerts, ActionManageAppPush, ActionManageVendor, ActionManageHTTPS, ActionManageIssuer, ActionManageContentCache, ActionReadBlueprints, ActionListBlueprints, ActionGetDeclaration, "manageDEPCredentials", ActionManageSensitiveWebhooks, ActionReplaySensitiveWebhooks}
	for i := range actions {
		a := &actions[i]
		a.Group = "device-management"
		a.Context = map[string]adminauth.ContextAttribute{"method": {Type: "String", Required: true}}
		if a.Resource == adminauth.EntityEnrollment {
			a.Context["channel"] = adminauth.ContextAttribute{Type: "String", Required: true}
		}
		if a.ID == ActionAssignSet || a.ID == ActionEditSet {
			a.Context["set"] = adminauth.ContextAttribute{Type: "String", Required: true}
		}
		if a.ID == ActionAssignBlueprint {
			a.Context["blueprint"] = adminauth.ContextAttribute{Type: "String", Required: true}
		}
		if strings.HasPrefix(a.ID, "enqueueCommand.") || a.ID == ActionEnqueueUnknownCommand {
			a.Context["requestType"] = adminauth.ContextAttribute{Type: "String", Required: true}
			a.Sensitive = !slices.Contains(operator, a.ID)
		}
		a.Sensitive = a.Sensitive || slices.Contains(sensitive, a.ID)
		a.RootOnly = a.ID == ActionManageRoles || a.ID == ActionManagePrincipals || a.ID == ActionManagePolicies
		if a.RootOnly {
			a.Group = "authorization"
		}
		switch {
		case slices.Contains(viewer, a.ID):
			a.Groups = []string{"ViewerActions", "OperatorActions", "DeviceAdminActions"}
		case slices.Contains(operator, a.ID):
			a.Groups = []string{"OperatorActions", "DeviceAdminActions"}
		case slices.Contains(administrator, a.ID):
			a.Groups = []string{"DeviceAdminActions"}
		case slices.Contains(auditor, a.ID):
			a.Groups = []string{"AuditorActions"}
			a.Group = "authorization"
		}
	}
	return actions
}
