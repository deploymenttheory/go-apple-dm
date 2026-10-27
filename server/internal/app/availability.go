package app

import (
	"context"
	"net/http"
	"slices"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// latestVersion renders an absent deprecation or removal bound.
const latestVersion = "latest"

// availabilityView is the admin API projection of one key's availability
// issue. Versions are dotted strings, and an absent deprecation or removal
// bound is "latest". An issue without version bounds, such as for a key that is
// not available on the target OS or that has no availability data, has no
// window. Views never contain configuration values.
type availabilityView struct {
	Path       string
	OS         support.OS
	Rule       support.Rule
	Severity   validation.Severity
	Introduced string `json:",omitempty"`
	Deprecated string `json:",omitempty"`
	Removed    string `json:",omitempty"`
}

// availabilityViews projects availability issues for the admin API.
func availabilityViews(issues []validation.AvailabilityIssue) []availabilityView {
	if len(issues) == 0 {
		return nil
	}
	out := make([]availabilityView, 0, len(issues))
	for _, issue := range issues {
		view := availabilityView{Path: issue.Path, OS: issue.OS, Rule: issue.Rule, Severity: issue.Severity}
		if !issue.Introduced.IsZero() || !issue.Deprecated.IsZero() || !issue.Removed.IsZero() {
			view.Introduced = versionView(issue.Introduced, "")
			view.Deprecated = versionView(issue.Deprecated, latestVersion)
			view.Removed = versionView(issue.Removed, latestVersion)
		}
		out = append(out, view)
	}
	return out
}

// versionView renders v, or absent when v is zero.
func versionView(v osversion.Version, absent string) string {
	if v.IsZero() {
		return absent
	}
	return v.String()
}

// compatibilityView is the admin API projection of a DDM compatibility report.
type compatibilityView struct {
	Enforced bool
	Target   support.Target
	Eligible []ddm.DeclarationRef
	Issues   []compatibilityIssueView
}

// compatibilityIssueView is the admin API projection of one declaration issue.
type compatibilityIssueView struct {
	Identifier   string
	Reason       string
	Withheld     bool
	Availability []availabilityView `json:",omitempty"`
}

// viewCompatibility projects report, keeping only the declarations for which
// keep reports true; a nil keep keeps every declaration.
func viewCompatibility(report ddm.CompatibilityReport, keep func(identifier string) bool) compatibilityView {
	view := compatibilityView{
		Enforced: report.Enforced,
		Target:   report.Target,
		Eligible: []ddm.DeclarationRef{},
		Issues:   []compatibilityIssueView{},
	}
	for _, ref := range report.Eligible {
		if keep == nil || keep(ref.Identifier) {
			view.Eligible = append(view.Eligible, ref)
		}
	}
	for _, issue := range report.Issues {
		if keep == nil || keep(issue.Identifier) {
			view.Issues = append(view.Issues, compatibilityIssueView{
				Identifier:   issue.Identifier,
				Reason:       issue.Reason,
				Withheld:     issue.Withheld,
				Availability: availabilityViews(issue.Availability),
			})
		}
	}
	return view
}

// setVerdict reports the enrollment's compatibility for the declarations in set.
func (a *App) setVerdict(ctx context.Context, id mdm.EnrollmentID, set string) (compatibilityView, error) {
	members, err := a.Engine.SetDeclarations(ctx, set)
	if err != nil {
		return compatibilityView{}, err
	}
	report, err := a.Engine.Compatibility(ctx, id)
	if err != nil {
		return compatibilityView{}, err
	}
	return viewCompatibility(report, func(identifier string) bool { return slices.Contains(members, identifier) }), nil
}

// respondAssignment reports whether a set assignment changed. Assigning (PUT)
// also returns the enrollment's verdict for the set's declarations; the
// assignment stands when no verdict can be produced, and the response then
// omits it.
func (a *App) respondAssignment(w http.ResponseWriter, r *http.Request, id mdm.EnrollmentID, set string, changed bool, err error) {
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	out := map[string]any{"Changed": changed}
	if r.Method == http.MethodPut {
		verdict, err := a.setVerdict(r.Context(), id, set)
		if err != nil {
			a.cfg.Logger.WarnContext(r.Context(), "assignment verdict unavailable", "set", set, "error", err)
		} else {
			out["Verdict"] = verdict
		}
	}
	writeJSON(w, http.StatusOK, out)
}
