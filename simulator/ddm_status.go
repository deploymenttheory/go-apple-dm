package simulator

import (
	"maps"
	"slices"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/ddm/predicate"
	"github.com/deploymenttheory/go-apple-mdm/internal/canonjson"
	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddmproto"
	"github.com/deploymenttheory/go-apple-mdm/schema/status"
)

// Reason codes from declarative/declarations/declarationbase.yaml.
const (
	reasonPredicate            = "Info.Predicate"
	reasonNotReferencedByAct   = "Info.NotReferencedByActivation"
	reasonNotReferencedByConf  = "Info.NotReferencedByConfiguration"
	reasonActivationFailed     = "Error.ActivationFailed"
	reasonMissingConfigs       = "Error.MissingConfigurations"
	reasonUnknownType          = "Error.UnknownDeclarationType"
	reasonUnableToParsePred    = "Error.UnableToParsePredicate"
	reasonUnableToEvaluatePred = "Error.UnableToEvaluatePredicate"
)

// Values of the "valid" field.
const (
	validYes = "valid"
	validNo  = "invalid"
)

// ddmProtocolVersion is the protocol version the client reports.
const ddmProtocolVersion = "1.0.0"

// buildReport grades the declarations and renders the status report. It
// returns the canonical JSON body and the graded row of every declaration
// (keyed by "<kind>/<identifier>") so post can record the baseline an
// incremental report is computed against. The caller holds c.mu.
func (c *ddmChannel) buildReport(cfg ddmConfig, full bool) ([]byte, map[string]string) {
	c.grade(cfg)
	rows := make(map[string]string, len(c.state.Declarations))
	var decls status.ManagementDeclarationsDeclarations
	changed := false
	for _, key := range slices.Sorted(maps.Keys(c.state.Declarations)) {
		d := c.state.Declarations[key]
		row := status.ManagementDeclarationsDeclaration{Identifier: d.Identifier, ServerToken: d.ServerToken, Active: d.Active, Valid: d.Valid}
		for _, r := range d.Reasons {
			row.Reasons = append(row.Reasons, status.ManagementDeclarationsStatusReason{Code: r.Code, Description: r.Description, Details: r.Details})
		}
		canon := string(mustCanon(row))
		rows[key] = canon
		if !full && c.baseline[key] == canon {
			continue
		}
		changed = true
		switch d.Kind {
		case schemaddm.KindActivation:
			decls.Activations = append(decls.Activations, row)
		case schemaddm.KindConfiguration:
			decls.Configurations = append(decls.Configurations, row)
		case schemaddm.KindAsset:
			decls.Assets = append(decls.Assets, row)
		default:
			decls.Management = append(decls.Management, row)
		}
	}
	items := map[string]any{}
	mgmt := map[string]any{}
	if full || c.state.LastReport.IsZero() {
		items["device"] = c.dev.deviceStatusItems()
		mgmt["client-capabilities"] = clientCapabilities(cfg.testItems)
	}
	if full || changed {
		mgmt["declarations"] = decls
	}
	if len(mgmt) > 0 {
		items["management"] = mgmt
	}
	report := ddmproto.StatusReport{StatusItems: items}
	if full {
		report.FullReport = new(true)
	}
	return mustCanon(report), rows
}

// mustCanon marshals values built from plain Go types; a failure is a
// programming error.
func mustCanon(v any) []byte {
	b, err := canonjson.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// grade evaluates every declaration into Active, Valid, and Reasons, and
// refreshes the merged properties.
func (c *ddmChannel) grade(cfg ddmConfig) {
	props := maps.Clone(cfg.props)
	if props == nil {
		props = map[string]any{}
	}
	byKind := map[schemaddm.Kind][]*DDMDeclaration{}
	for _, key := range slices.Sorted(maps.Keys(c.state.Declarations)) {
		d := c.state.Declarations[key]
		d.Active, d.Valid, d.Reasons = false, validYes, nil
		byKind[d.Kind] = append(byKind[d.Kind], d)
		if d.Kind == schemaddm.KindManagement && d.Type == schemaddm.DeclarationTypeManagementProperties {
			maps.Copy(props, d.Payload)
		}
	}
	c.state.Properties = props
	env := predicate.MapEnv{Properties: props}

	// Activations decide everything below them.
	configs := map[string]*DDMDeclaration{}
	for _, d := range byKind[schemaddm.KindConfiguration] {
		configs[d.Identifier] = d
	}
	referrers := map[string][]*DDMDeclaration{} // configuration identifier -> activations
	for _, a := range byKind[schemaddm.KindActivation] {
		if !knownType(a) {
			continue
		}
		for _, id := range gradeActivation(a, configs, env) {
			referrers[id] = append(referrers[id], a)
		}
	}
	activeConfigs := []*DDMDeclaration{}
	for _, d := range byKind[schemaddm.KindConfiguration] {
		if !knownType(d) {
			continue
		}
		gradeConfiguration(d, referrers[d.Identifier])
		if d.Active {
			activeConfigs = append(activeConfigs, d)
		}
	}
	for _, d := range byKind[schemaddm.KindAsset] {
		if !knownType(d) {
			continue
		}
		gradeAsset(d, activeConfigs)
	}
	// Management declarations are never "active"; an unknown type is still
	// flagged above.
	for _, d := range byKind[schemaddm.KindManagement] {
		knownType(d)
	}
}

// knownType marks a declaration invalid when the registry has no type of
// that name in its kind, and reports whether it is known.
func knownType(d *DDMDeclaration) bool {
	for _, e := range schemaddm.ByID(d.Type) {
		if e.Kind == d.Kind {
			return true
		}
	}
	d.Valid = validNo
	d.Reasons = append(d.Reasons, DDMReason{Code: reasonUnknownType, Details: map[string]any{"UnknownDeclarationType": d.Type}})
	return false
}

// gradeActivation grades one activation.simple and returns the
// configurations it references.
func gradeActivation(a *DDMDeclaration, configs map[string]*DDMDeclaration, env predicate.Env) []string {
	refs := stringSlice(a.Payload["StandardConfigurations"])
	var missing []any
	for _, id := range refs {
		if _, ok := configs[id]; !ok {
			missing = append(missing, id)
		}
	}
	details := func() map[string]any {
		return map[string]any{"Identifier": a.Identifier, "ServerToken": a.ServerToken}
	}
	if len(missing) > 0 {
		d := details()
		d["ConfigurationIdentifiers"] = missing
		a.Valid = validNo
		a.Reasons = append(a.Reasons, DDMReason{Code: reasonMissingConfigs, Description: new("activation references configurations that are not present"), Details: d})
	}
	active := true
	if pred, _ := a.Payload["Predicate"].(string); pred != "" {
		d := details()
		d["Predicate"] = pred
		p, err := predicate.Parse(pred)
		var ok bool
		if err == nil {
			ok, err = p.Eval(env)
		}
		switch {
		case err != nil:
			code := reasonUnableToEvaluatePred
			if p == nil {
				code = reasonUnableToParsePred
			}
			a.Valid = validNo
			a.Reasons = append(a.Reasons, DDMReason{Code: code, Description: new(err.Error()), Details: d})
		case !ok:
			a.Reasons = append(a.Reasons, DDMReason{Code: reasonPredicate, Details: d})
			active = false
		}
	}
	a.Active = active && a.Valid == validYes
	return refs
}

// gradeConfiguration grades a configuration from the activations that
// reference it.
func gradeConfiguration(d *DDMDeclaration, refs []*DDMDeclaration) {
	if len(refs) == 0 {
		d.Reasons = append(d.Reasons, DDMReason{Code: reasonNotReferencedByAct, Details: map[string]any{"Identifier": d.Identifier, "ServerToken": d.ServerToken}})
		return
	}
	for _, a := range refs {
		if a.Active {
			d.Active = true
			d.Reasons = nil
			return
		}
		d.Reasons = append(d.Reasons, DDMReason{
			Code: reasonActivationFailed, Description: new("activation " + a.Identifier + " is not active"),
			Details: map[string]any{"Identifier": a.Identifier, "ServerToken": a.ServerToken},
		})
	}
}

// gradeAsset marks an asset active when any active configuration mentions
// its identifier anywhere in its payload.
func gradeAsset(d *DDMDeclaration, activeConfigs []*DDMDeclaration) {
	for _, cfg := range activeConfigs {
		if mentions(cfg.Payload, d.Identifier) {
			d.Active = true
			return
		}
	}
	d.Reasons = append(d.Reasons, DDMReason{Code: reasonNotReferencedByConf, Details: map[string]any{"Identifier": d.Identifier, "ServerToken": d.ServerToken}})
}

// mentions reports whether any string value inside v equals want.
func mentions(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case map[string]any:
		for _, child := range x {
			if mentions(child, want) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if mentions(child, want) {
				return true
			}
		}
	}
	return false
}

// stringSlice extracts the strings from a decoded JSON array.
func stringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// deviceStatusItems renders the device.* status items from the identity.
func (d *Device) deviceStatusItems() map[string]any {
	family, osFamily := deviceFamily(d.Model)
	return map[string]any{
		"identifier":       map[string]any{"serial-number": d.SerialNumber, "udid": d.UDID},
		"model":            map[string]any{"family": family, "identifier": d.Model, "marketing-name": d.ModelName},
		"operating-system": map[string]any{"build-version": d.BuildVersion, "family": osFamily, "marketing-name": osFamily, "version": d.OSVersion},
	}
}

// deviceFamily maps a model identifier to Apple's hardware and OS family
// names.
func deviceFamily(model string) (hardware, os string) {
	switch {
	case strings.HasPrefix(model, "iPhone"):
		return "iPhone", "iOS"
	case strings.HasPrefix(model, "iPad"):
		return "iPad", "iPadOS"
	case strings.HasPrefix(model, "AppleTV"):
		return "Apple TV", "tvOS"
	case strings.HasPrefix(model, "Watch"):
		return "Apple Watch", "watchOS"
	case strings.HasPrefix(model, "RealityDevice"):
		return "Apple Vision", "visionOS"
	}
	return "Mac", "macOS"
}

// clientCapabilities derives management.client-capabilities from the
// schema registries: every standalone declaration type by kind and every
// status item, leaving out Apple's "test.*" items unless asked for.
func clientCapabilities(testItems bool) status.ManagementClientCapabilitiesCapabilities {
	var decls status.ManagementClientCapabilitiesCapabilitiesSupportedPayloadsDeclarations
	for _, e := range schemaddm.Registry {
		switch e.Kind {
		case schemaddm.KindActivation:
			decls.Activations = append(decls.Activations, e.ID)
		case schemaddm.KindConfiguration:
			decls.Configurations = append(decls.Configurations, e.ID)
		case schemaddm.KindAsset:
			decls.Assets = append(decls.Assets, e.ID)
		case schemaddm.KindManagement:
			decls.Management = append(decls.Management, e.ID)
		}
	}
	for _, list := range []*[]string{&decls.Activations, &decls.Configurations, &decls.Assets, &decls.Management} {
		slices.Sort(*list)
		*list = slices.Compact(*list)
	}
	var items []string
	for _, id := range status.IDs() {
		if testItems || !strings.HasPrefix(id, "test.") {
			items = append(items, id)
		}
	}
	return status.ManagementClientCapabilitiesCapabilities{
		SupportedVersions: []string{ddmProtocolVersion},
		SupportedFeatures: map[string]any{},
		SupportedPayloads: status.ManagementClientCapabilitiesCapabilitiesSupportedPayloads{Declarations: decls, StatusItems: items},
	}
}
