package app

import (
	json "encoding/json/v2"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
)

// statusPage exposes the existing store queries without refreshing delivery
// snapshots. Missing status is an empty page, as in the original values route.
func (a *App) statusPage(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := enrollmentFromPath(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if kind == "values" && !r.URL.Query().Has("limit") {
			p.Limit = 1000 // Preserve the pre-pagination route's default.
		}
		var result any
		switch kind {
		case "values":
			res, queryErr := a.Engine.StatusValues(r.Context(), id,
				ddm.StatusValueQuery{PathPrefix: r.URL.Query().Get("prefix")}, p)
			err = queryErr
			for i := range res.Items {
				res.Items[i].Value = projectStatusJSON(res.Items[i].Path, res.Items[i].Value)
			}
			result = res
		case "errors":
			res, queryErr := a.Engine.StatusErrors(r.Context(), id, p)
			err = queryErr
			for i := range res.Items {
				res.Items[i].Reasons = projectStatusJSON("", res.Items[i].Reasons)
			}
			result = res
		case "reports":
			res, queryErr := a.Engine.StatusReports(r.Context(), id, p)
			err = queryErr
			for i := range res.Items {
				res.Items[i].Raw = projectStatusJSON("", res.Items[i].Raw)
			}
			result = res
		}
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// Projection changes copies, never retained evidence. Free-form error messages
// and details can echo submitted credentials, so diagnostics expose reason codes
// but omit those fields as well as known secret-bearing status paths.
func projectStatusJSON(path string, raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []byte("null")
	}
	out, err := json.Marshal(projectStatusValue(path, v))
	if err != nil {
		return []byte("null")
	}
	return out
}

// projectStatusValue recursively replaces sensitive values with a redaction marker,
// modifying nested maps and slices in place.
func projectStatusValue(path string, v any) any {
	if sensitiveStatusPath(path) {
		return "[redacted]"
	}
	switch value := v.(type) {
	case map[string]any:
		for k, child := range value {
			value[k] = projectStatusValue(path+"."+k, child)
		}
	case []any:
		for i, child := range value {
			value[i] = projectStatusValue(path, child)
		}
	}
	return v
}

// sensitiveStatusPath recognizes sensitive path segments after normalizing case, hyphens
// and underscores.
func sensitiveStatusPath(path string) bool {
	for _, part := range strings.Split(strings.ToLower(path), ".") {
		part = strings.NewReplacer("-", "", "_", "").Replace(part)
		switch part {
		case "description", "message", "details", "location", "authorization":
			return true
		}
		for _, marker := range []string{"password", "secret", "credential", "recoverykey", "bypass", "pushtoken", "pushmagic", "applecaretoken", "accesstoken", "refreshtoken", "identitytoken"} {
			if strings.Contains(part, marker) {
				return true
			}
		}
	}
	return false
}
