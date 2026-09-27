package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/server/applicationpackages"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

type packageTokenKey struct{}

const packageTransferTimeout = 30 * time.Minute

// packageTransfer extends the connection deadlines only after authorization and
// bounds storage work with the same deadline. HTTP test recorders and custom
// embedders may not support connection deadlines; the context still bounds work.
func packageTransfer(next http.Handler, readBody bool, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(timeout)
		controller := http.NewResponseController(w)
		if readBody {
			if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				http.Error(w, "package transfer unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
			http.Error(w, "package transfer unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// redactPackageURL removes download capabilities before request observation and logs.
func redactPackageURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, matched := strings.CutPrefix(r.URL.Path, applicationpackages.Path)
		if matched {
			token, resource, ok := strings.Cut(raw, "/")
			if !ok || token == "" || (resource != "manifest.plist" && resource != "package.pkg") || r.URL.RawQuery != "" {
				http.NotFound(w, r)
				return
			}
			clone := r.Clone(context.WithValue(r.Context(), packageTokenKey{}, token))
			clone.URL.Path = applicationpackages.Path + resource
			clone.URL.RawPath = ""
			clone.RequestURI = clone.URL.Path
			r = clone
		}
		next.ServeHTTP(w, r)
	})
}

// wireApplicationPackageDownloads mounts HTTPS grant-protected manifest and content routes.
func (a *App) wireApplicationPackageDownloads(mux *http.ServeMux) {
	if a.packageHost == nil {
		return
	}
	mux.HandleFunc("GET "+applicationpackages.Path+"manifest.plist", func(w http.ResponseWriter, r *http.Request) {
		if !a.contentCacheHTTPS(r) {
			http.NotFound(w, r)
			return
		}
		token, _ := r.Context().Value(packageTokenKey{}).(string)
		data, grant, err := a.packageHost.Manifest(r.Context(), token)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		webhook.ObserveSubject(r.Context(), webhook.Subject{Kind: "enrollment", ID: grant.Enrollment.ID, Channel: grant.Enrollment.Channel.String()})
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		} // #nosec G705 -- Validated XML property list with a non-HTML content type and nosniff.
	})
	mux.HandleFunc("GET "+applicationpackages.Path+"package.pkg", func(w http.ResponseWriter, r *http.Request) {
		if !a.contentCacheHTTPS(r) {
			http.NotFound(w, r)
			return
		}
		token, _ := r.Context().Value(packageTokenKey{}).(string)
		content, grant, err := a.packageHost.Content(r.Context(), token)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		webhook.ObserveSubject(r.Context(), webhook.Subject{Kind: "enrollment", ID: grant.Enrollment.ID, Channel: grant.Enrollment.Channel.String()})
		packageTransfer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			servePackage(w, r, content, func(offset, length int64) (io.ReadCloser, error) {
				body, _, err := a.packageHost.Open(r.Context(), token, offset, length)
				return body, err
			})
		}), false, packageTransferTimeout).ServeHTTP(w, r)
	})
}

// downloadAdminPackage streams a reviewed immutable revision under operator authorization.
func (a *App) downloadAdminPackage(w http.ResponseWriter, r *http.Request) {
	id, revision := r.PathValue("package"), r.PathValue("revision")
	content, err := a.ApplicationPackages.Revision(r.Context(), id, revision)
	if err != nil {
		packageError(w, err)
		return
	}
	servePackage(w, r, content, func(offset, length int64) (io.ReadCloser, error) {
		body, _, err := a.ApplicationPackages.Open(r.Context(), id, revision, offset, length)
		return body, err
	})
}

// packageRange accepts one standard byte range, including suffix and open-ended
// forms. Multiple ranges are rejected so streaming stays bounded to one backend read.
func packageRange(raw string, size int64) (offset, length int64, err error) {
	if raw == "" {
		return 0, size, nil
	}
	bounds, ok := strings.CutPrefix(raw, "bytes=")
	if !ok || strings.Contains(bounds, ",") {
		return 0, 0, applications.ErrInvalid
	}
	start, end, ok := strings.Cut(bounds, "-")
	if !ok || size <= 0 {
		return 0, 0, applications.ErrInvalid
	}
	if start == "" {
		suffix, e := strconv.ParseInt(end, 10, 64)
		if e != nil || suffix <= 0 {
			return 0, 0, applications.ErrInvalid
		}
		length = min(suffix, size)
		return size - length, length, nil
	}
	offset, err = strconv.ParseInt(start, 10, 64)
	if err != nil || offset < 0 || offset >= size {
		return 0, 0, applications.ErrInvalid
	}
	last := size - 1
	if end != "" {
		last, err = strconv.ParseInt(end, 10, 64)
		if err != nil || last < offset {
			return 0, 0, applications.ErrInvalid
		}
		last = min(last, size-1)
	}
	return offset, last - offset + 1, nil
}

// servePackage validates HTTP range semantics before opening a storage stream.
// A backend failure before headers returns 503; a mid-stream failure aborts the
// response so a truncated package cannot look like a complete successful download.
func servePackage(w http.ResponseWriter, r *http.Request, content applications.Content, open func(int64, int64) (io.ReadCloser, error)) {
	etag := `"` + content.SHA256 + `"`
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	if match := r.Header.Get("If-None-Match"); match == etag || match == "*" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	requested := r.Header.Get("Range")
	if condition := r.Header.Get("If-Range"); condition != "" && condition != etag {
		requested = ""
	}
	offset, length, err := packageRange(requested, content.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", content.Size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	var body io.ReadCloser
	if r.Method != http.MethodHead {
		body, err = open(offset, length)
		if err != nil {
			http.Error(w, "package storage unavailable", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = body.Close() }()
	}
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	if requested != "" {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, content.Size))
		w.WriteHeader(http.StatusPartialContent)
	}
	if body != nil {
		if _, err = io.CopyN(w, body, length); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
