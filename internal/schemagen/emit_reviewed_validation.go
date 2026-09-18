package schemagen

import "bytes"

// reviewedValidation emits typed checks for the previously reviewed prose rules.
// Rules are scoped to their schema family and type, so a similarly named type
// in another family cannot accidentally receive an unrelated constraint.
func (e *emitter) reviewedValidation(b *bytes.Buffer, td *TypeDef) {
	if td.Name == "ContentCaching" && (e.pkg.Family == FamilyDDM || e.pkg.Family == FamilyProfiles) {
		// Both the DDM and legacy profile cache contracts require one cache class.
		b.WriteString(`
	c.Require(validation.Join(p, "AllowSharedCaching"),
		!(x.AllowPersonalCaching != nil && !*x.AllowPersonalCaching && x.AllowSharedCaching != nil && !*x.AllowSharedCaching),
		"personal or shared caching must be enabled")
`)
	}
	if e.pkg.Family != FamilyDDM {
		return
	}
	switch td.Name {
	case "SoftwareUpdateEnforcementSpecific":
		b.WriteString("\tc.LocalDateTime(validation.Join(p, \"TargetLocalDateTime\"), x.TargetLocalDateTime != \"\", x.TargetLocalDateTime)\n")
	case "SoftwareUpdateSettingsAutomaticActions":
		b.WriteString(`
	c.Require(p, !(x.Download != nil && *x.Download == "AlwaysOff" &&
		((x.InstallOSUpdates != nil && *x.InstallOSUpdates == "AlwaysOn") ||
		(x.InstallSecurityUpdate != nil && *x.InstallSecurityUpdate == "AlwaysOn"))),
		"automatic installation requires automatic downloads")
`)
	case "ContentCaching":
		b.WriteString(`
	if x.ManagementSecurityConfig != nil && (*x.ManagementSecurityConfig == "signedByCACert" || *x.ManagementSecurityConfig == "specificServerCert") {
		c.Require(validation.Join(p, "ManagementStatusCertificateReference"),
			x.ManagementStatusCertificateReference != nil && *x.ManagementStatusCertificateReference != "",
			"certificate reference is required for the selected trust mode")
	}
`)
	case "AppManaged":
		b.WriteString(`
	identifiers := 0
	for _, identifier := range []*string{x.AppStoreID, x.BundleID, x.ManifestURL, x.AppComposedIdentifier} {
		if identifier != nil && *identifier != "" { identifiers++ }
	}
	c.Require(p, identifiers == 1, "exactly one app identifier or manifest is required")
`)
	case "AppSettingsAllowedAllowedBinaries":
		// app.settings.yaml: qualifiers cannot identify an allowed binary alone.
		b.WriteString(`
	c.Require(p, (x.CDHash != nil && *x.CDHash != "") || (x.TeamID != nil && *x.TeamID != ""),
		"a code directory hash or team identifier is required")
`)
	case "AppSettingsAllowedDeniedBinaries":
		// Deny rules may also use a signing identifier without a hash or team.
		b.WriteString(`
	c.Require(p, (x.CDHash != nil && *x.CDHash != "") || (x.TeamID != nil && *x.TeamID != "") ||
		(x.SigningID != nil && *x.SigningID != ""),
		"a code directory hash, team identifier or signing identifier is required")
`)
	}
}
