package app

import (
	"io"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// configurationProfileAdminRoutes declares immutable profile upload, metadata and content
// routes with distinct permissions and response bounds.
func (a *App) configurationProfileAdminRoutes() []adminRoute {
	if a.ConfigurationProfiles == nil {
		return nil
	}
	var routes []adminRoute
	add := func(action, pattern string, fn http.HandlerFunc) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: action, Family: "ddm", LocalMutation: true, Handler: fn})
	}
	add(ActionUploadProfile, "POST /configuration-profiles", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, configurationprofile.MaxBytes+1))
		if err != nil || len(b) > configurationprofile.MaxBytes {
			writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
			return
		}
		v, err := a.ConfigurationProfiles.Upload(r.Context(), b)
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	add(ActionReadProfiles, "GET /configuration-profiles", func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		v, err := a.ConfigurationProfiles.List(r.Context(), p)
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	add("readConfigurationProfile", "GET /configuration-profiles/{revision}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.ConfigurationProfiles.Get(r.Context(), r.PathValue("revision"))
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	add(ActionDownloadProfile, "GET /configuration-profiles/{revision}/content", func(w http.ResponseWriter, r *http.Request) {
		b, v, err := a.ConfigurationProfiles.Data(r.Context(), r.PathValue("revision"))
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeProfile(w, b, v.ContentType)
	})
	routes[len(routes)-1].MaxResponseBytes = configurationprofile.MaxBytes
	return routes
}

// writeProfile writes profile bytes with the supplied content type, disables caching and
// prevents content sniffing.
func writeProfile(w http.ResponseWriter, b []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b) // #nosec G705 -- validated profile bytes with an explicit non-HTML content type
}
