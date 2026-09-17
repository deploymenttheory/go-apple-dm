package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/internal/httpsurl"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// ContentCacheConfig enables native cache reporting when PublicURL is nonempty.
// PublicURL is the HTTPS server origin, without a path, query or credentials.
type ContentCacheConfig struct {
	PublicURL string
	Retention time.Duration
}

const ActionManageContentCache = "manageContentCacheCredentials"

type contentCacheTokenKey struct{}

func (a *App) wireContentCache(ctx context.Context, mux *http.ServeMux) error {
	if a.cfg.ContentCache.PublicURL == "" {
		return nil
	}
	u, err := httpsurl.Parse(a.cfg.ContentCache.PublicURL)
	if err != nil || u.RawQuery != "" || (u.Path != "" && u.Path != "/") || a.cfg.ContentCache.Retention < 0 {
		return fmt.Errorf("%w: content-cache requires an HTTPS origin and non-negative retention", ErrConfig)
	}
	st, err := a.protocolState(ctx)
	if err != nil {
		return err
	}
	a.contentCache = &contentcache.StateStore{State: st, Retention: a.cfg.ContentCache.Retention}
	receiver, err := contentcache.NewReceiver(contentcache.Config{
		Authorize: func(ctx context.Context, r *http.Request) error {
			if !a.contentCacheHTTPS(r) {
				return contentcache.ErrCredential
			}
			token, _ := ctx.Value(contentCacheTokenKey{}).(string)
			id, err := a.contentCache.Authenticate(ctx, token)
			if err != nil {
				return err
			}
			enrollment, err := a.Store.Get(ctx, id)
			if err != nil || !enrollment.Enabled {
				return contentcache.ErrCredential
			}
			return nil
		},
		Accept: func(ctx context.Context, report *contentcache.Report) error {
			token, _ := ctx.Value(contentCacheTokenKey{}).(string)
			return a.contentCache.Accept(ctx, token, report)
		},
	})
	if err != nil {
		return err
	}
	mux.Handle("/content-cache/metrics", receiver)
	return nil
}

func (a *App) contentCacheHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, proxy := range a.cfg.TrustedProxies {
		if proxy.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

// Strip the bearer URL before internal middleware sees it. Reverse proxies
// must independently redact this route in their access logs.
func redactContentCacheURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := strings.CutPrefix(r.URL.Path, "/content-cache/metrics/"); ok {
			if strings.Contains(token, "/") || r.URL.RawQuery != "" {
				http.NotFound(w, r)
				return
			}
			copyURL := *r.URL
			copyURL.Path, copyURL.RawPath = "/content-cache/metrics", ""
			r.URL, r.RequestURI = &copyURL, copyURL.Path
			r = r.WithContext(context.WithValue(r.Context(), contentCacheTokenKey{}, token))
		}
		next.ServeHTTP(w, r)
	})
}

func contentCacheActions() []adminauth.Action {
	return []adminauth.Action{{ID: ActionManageContentCache, Help: "Issue, rotate or revoke a device's content-cache reporting credential.", Resource: adminauth.EntityEnrollment}}
}

func (a *App) contentCacheRoutes() []adminRoute {
	if a.contentCache == nil {
		return nil
	}
	const path = "/enrollments/{channel}/{id}/content-cache/"
	return []adminRoute{
		{Pattern: "POST " + path + "credential", Action: ActionManageContentCache, Family: "contentcache", LocalMutation: true, Handler: http.HandlerFunc(a.rotateContentCache)},
		{Pattern: "DELETE " + path + "credential", Action: ActionManageContentCache, Family: "contentcache", LocalMutation: true, Handler: http.HandlerFunc(a.revokeContentCache)},
		{Pattern: "GET " + path + "reports", Action: ActionReadEnrollmentStatus, Family: "contentcache", Handler: http.HandlerFunc(a.contentCacheReports)},
	}
}

func (a *App) rotateContentCache(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, err := a.contentCache.RotateCredential(r.Context(), id)
	if err != nil {
		contentCacheError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{"URL": strings.TrimRight(a.cfg.ContentCache.PublicURL, "/") + "/content-cache/metrics/" + token})
}

func (a *App) revokeContentCache(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.contentCache.RevokeCredential(r.Context(), id); err != nil {
		contentCacheError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) contentCacheReports(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if id.Channel != mdm.ChannelDevice {
		contentCacheError(w, state.ErrInvalid)
		return
	}
	p, err := page(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	reports, err := a.contentCache.Reports(r.Context(), id, p)
	if err != nil {
		contentCacheError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reports)
}

func contentCacheError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, state.ErrInvalid) {
		code = http.StatusBadRequest
	}
	writeError(w, code, fmt.Errorf("%w: content-cache request failed", errOperation))
}
