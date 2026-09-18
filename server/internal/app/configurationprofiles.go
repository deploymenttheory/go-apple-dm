package app

import (
	"context"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func (a *App) wireConfigurationProfiles(ctx context.Context) error {
	st, err := a.protocolState(ctx)
	if err != nil {
		return err
	}
	a.ConfigurationProfiles, err = configurationprofile.New(configurationprofile.Config{
		State: st, Engine: a.Engine, BaseURL: a.cfg.Enroll.PublicURL,
		Target: func(ctx context.Context, id mdm.EnrollmentID) (support.Target, error) {
			return service.EnrollmentTarget(ctx, a.Store, id)
		},
	})
	return err
}

func (a *App) wireConfigurationProfileDownloads(mux *http.ServeMux, remote proxyclient.ConfigurationProfileFetcher) {
	mux.Handle("GET "+configurationprofile.Path+"{revision}", a.certSource()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		channel, err := channelFromName(q.Get("channel"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		id := mdm.EnrollmentID{ID: q.Get("id"), ParentID: q.Get("parent"), Channel: channel}
		if err := a.Core.AuthorizeResource(r.Context(), &mdm.Request{ID: id, Certificate: httpapi.CertFromContext(r.Context())}); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if remote != nil {
			resp, err := remote(r.Context(), id, r.PathValue("revision"))
			if err != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			if resp.Status != http.StatusOK {
				http.NotFound(w, r)
				return
			}
			writeProfile(w, resp.Body, resp.ContentType)
			return
		}
		b, info, err := a.ConfigurationProfiles.Fetch(r.Context(), id, r.PathValue("revision"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeProfile(w, b, info.ContentType)
	})))
}
