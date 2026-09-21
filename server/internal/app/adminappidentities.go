package app

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appartifact"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appleappidentity"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

const ActionDiscoverApplicationIdentities = "discoverApplicationIdentities"

// ApplicationIdentityConfig supplies authoring discovery dependencies. A nil
// PublicAppStore uses Apple's public endpoint. Artifact inspection defaults to
// one concurrent upload and the utility's bounded options. Discovery is separate
// from publication and never executes uploaded programs or installer scripts.
type ApplicationIdentityConfig struct {
	PublicAppStore *publicappstoreidentity.Client
	Artifacts      appartifact.Options
}

// applicationIdentityActions declares the system-scoped permission for application
// identity discovery and artifact inspection.
func applicationIdentityActions() []adminauth.Action {
	return []adminauth.Action{{ID: ActionDiscoverApplicationIdentities, Resource: adminauth.EntitySystem, Help: "Discover public and Apple app identifiers, and inspect uploaded application artifacts for configuration authoring."}}
}

// applicationIdentityRoutes builds the identity-authoring routes when Blueprints are
// available, with bounded queries and artifact inspection.
func (a *App) applicationIdentityRoutes() []adminRoute {
	if a.Blueprints == nil {
		return nil
	}
	client := a.cfg.ApplicationIdentities.PublicAppStore
	if client == nil {
		client = &publicappstoreidentity.Client{}
	}
	prefix := "/authoring/app-identities"
	var routes []adminRoute
	add := func(pattern string, handler http.HandlerFunc) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: ActionDiscoverApplicationIdentities, Family: "authoring", Handler: handler})
	}
	add("GET "+prefix+"/public-app-store", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.RawQuery) > 4096 {
			writeError(w, 400, publicappstoreidentity.ErrInvalid)
			return
		}
		q := r.URL.Query()
		limit := 0
		if q.Has("limit") {
			var err error
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil || limit < 1 {
				writeError(w, 400, publicappstoreidentity.ErrInvalid)
				return
			}
		}
		apps, err := client.Search(r.Context(), publicappstoreidentity.Query{Term: q.Get("term"), Developer: q.Get("developer"), Limit: limit, Store: storeQuery(r)})
		if err != nil {
			writeIdentityError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": apps})
	})
	add("GET "+prefix+"/public-app-store/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeError(w, 400, publicappstoreidentity.ErrInvalid)
			return
		}
		app, err := client.Lookup(r.Context(), id, storeQuery(r))
		if err != nil {
			writeIdentityError(w, err)
			return
		}
		writeJSON(w, 200, app)
	})
	add("GET "+prefix+"/apple", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.RawQuery) > 4096 {
			writeError(w, 400, publicappstoreidentity.ErrInvalid)
			return
		}
		writeJSON(w, 200, map[string]any{"items": appleappidentity.Search(r.URL.Query().Get("term")), "sourceURL": appleappidentity.SourceURL, "reviewedOn": appleappidentity.ReviewedOn})
	})
	add("GET "+prefix+"/apple/{bundleID}", func(w http.ResponseWriter, r *http.Request) {
		app, ok := appleappidentity.Lookup(r.PathValue("bundleID"))
		if !ok {
			writeError(w, 404, publicappstoreidentity.ErrNotFound)
			return
		}
		writeJSON(w, 200, map[string]any{"app": app, "sourceURL": appleappidentity.SourceURL, "reviewedOn": appleappidentity.ReviewedOn})
	})
	busy := make(chan struct{}, 1)
	add("POST "+prefix+"/artifacts", func(w http.ResponseWriter, r *http.Request) {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/octet-stream" {
			writeError(w, http.StatusUnsupportedMediaType, appartifact.ErrInvalid)
			return
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			w.Header().Set("Retry-After", "5")
			writeError(w, 503, appartifact.ErrLimit)
			return
		}
		opts := a.cfg.ApplicationIdentities.Artifacts
		maxBytes := opts.MaxBytes
		if maxBytes == 0 {
			maxBytes = appartifact.DefaultMaxBytes
		}
		if r.ContentLength > maxBytes {
			writeError(w, 413, ErrBodyTooLarge)
			return
		}
		file, err := os.CreateTemp(opts.TempDir, "dm-upload-")
		if err != nil {
			writeError(w, 500, ErrConfig)
			return
		}
		defer func() { _ = os.Remove(file.Name()) }()
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		_, err = io.Copy(file, r.Body)
		closeErr := file.Close()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, ErrBodyTooLarge)
			} else {
				writeError(w, 400, appartifact.ErrInvalid)
			}
			return
		}
		if closeErr != nil {
			writeError(w, 500, ErrConfig)
			return
		}
		report, err := appartifact.Inspect(r.Context(), file.Name(), opts)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				writeError(w, 504, context.DeadlineExceeded)
			case errors.Is(err, context.Canceled):
				writeError(w, 408, context.Canceled)
			case errors.Is(err, appartifact.ErrLimit):
				writeError(w, 413, appartifact.ErrLimit)
			case errors.Is(err, appartifact.ErrUnsupported):
				writeError(w, 415, appartifact.ErrUnsupported)
			default:
				writeError(w, 400, appartifact.ErrInvalid)
			}
			return
		}
		writeJSON(w, 200, report)
	})
	return routes
}

// storeQuery extracts the requested App Store country and entity without performing a
// lookup.
func storeQuery(r *http.Request) publicappstoreidentity.Store {
	q := r.URL.Query()
	return publicappstoreidentity.Store{Country: q.Get("country"), Entity: publicappstoreidentity.Entity(q.Get("entity"))}
}

// writeIdentityError maps discovery failures to bounded HTTP errors and forwards a
// validated Retry-After value for throttling.
func writeIdentityError(w http.ResponseWriter, err error) {
	var status *publicappstoreidentity.StatusError
	switch {
	case errors.Is(err, publicappstoreidentity.ErrInvalid):
		writeError(w, 400, publicappstoreidentity.ErrInvalid)
	case errors.Is(err, publicappstoreidentity.ErrNotFound):
		writeError(w, 404, publicappstoreidentity.ErrNotFound)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		writeError(w, 408, context.Canceled)
	case errors.As(err, &status) && status.StatusCode == 429:
		if len(status.RetryAfter) <= 128 && !strings.ContainsAny(status.RetryAfter, "\r\n") {
			w.Header().Set("Retry-After", status.RetryAfter)
		}
		writeError(w, 429, publicappstoreidentity.ErrStatus)
	default:
		writeError(w, 502, publicappstoreidentity.ErrRequest)
	}
}
