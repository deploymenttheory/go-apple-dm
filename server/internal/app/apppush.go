package app

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/server/apppush"
	"github.com/deploymenttheory/go-apple-dm/state"
)

// AppPushConfig configures provider trust and optional alternative endpoints.
// Environments are always explicit on sends; credentials are never MDM credentials.
type AppPushConfig struct{ DevelopmentHost, ProductionHost, RootCAFile string }

func (a *App) wireAppPush(ctx context.Context) error {
	st, err := a.protocolState(ctx)
	if err != nil {
		return wrapError(err)
	}
	a.appPushStore = &apppush.Store{State: st, Keys: a.keyring, Now: a.cfg.Clock.Now}
	opts := []apns.Option{apns.WithClock(a.cfg.Clock)}
	if a.cfg.AppPush.RootCAFile != "" {
		certs, err := readCertsPEM(a.cfg.AppPush.RootCAFile)
		if err != nil {
			return wrapError(err)
		}
		roots := x509.NewCertPool()
		for _, c := range certs {
			roots.AddCert(c)
		}
		opts = append(opts, apns.WithRootCAs(roots))
	}
	a.appPushClients = map[string]*apns.AppClient{}
	for env, host := range map[string]string{"development": a.cfg.AppPush.DevelopmentHost, "production": a.cfg.AppPush.ProductionHost} {
		if host == "" {
			host = "https://api.push.apple.com"
			if env == "development" {
				host = "https://api.sandbox.push.apple.com"
			}
		}
		options := append(append([]apns.Option{}, opts...), apns.WithHost(host))
		c := apns.NewApp(a.appPushStore, options...)
		a.appPushClients[env] = c
		a.closers = append(a.closers, c.Close)
	}
	return nil
}

func (a *App) appPushRoutes() []adminRoute {
	return []adminRoute{
		{
			Pattern: "GET /apppush/credentials",
			Action:  ActionManageAppPush,
			Family:  "apppush",
			Handler: http.HandlerFunc(a.listAppPush),
		},
		{
			Pattern: "PUT /apppush/credentials",
			Action:  ActionManageAppPush,
			Family:  "apppush",
			Handler: http.HandlerFunc(a.putAppPush),
		},
		{
			Pattern: "POST /apppush/send",
			Action:  ActionSendAppPush,
			Family:  "apppush",
			Handler: http.HandlerFunc(a.sendAppPush),
		},
	}
}

func (a *App) appPushError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrInvalid):
		writeError(w, 400, fmt.Errorf("%w: invalid app push identity", errOperation))
	case errors.Is(err, state.ErrNotFound):
		writeError(w, 404, fmt.Errorf("%w: app push credential not found", errOperation))
	default:
		writeError(w, 500, fmt.Errorf("%w: app push credential storage unavailable", errOperation))
	}
}

func (a *App) listAppPush(w http.ResponseWriter, r *http.Request) {
	p, err := page(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	if p.Limit < 1 || p.Limit > 1000 {
		writeError(w, 400, fmt.Errorf("%w: limit must be 1..1000", errOperation))
		return
	}
	rows, next, err := a.appPushStore.List(r.Context(), p.Cursor, p.Limit)
	if err != nil {
		a.appPushError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"Items": rows, "NextCursor": next})
}

func readAdminJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(b) > MaxAdminBody {
		writeError(w, 413, ErrBodyTooLarge)
		return false
	}
	if json.Unmarshal(b, out) != nil {
		writeError(w, 400, fmt.Errorf("%w: invalid JSON request", errOperation))
		return false
	}
	return true
}

func (a *App) putAppPush(w http.ResponseWriter, r *http.Request) {
	var in struct{ Topic, CertPEM, KeyPEM string }
	if !readAdminJSON(w, r, &in) {
		return
	}
	m, err := a.appPushStore.Put(r.Context(), in.Topic, []byte(in.CertPEM), []byte(in.KeyPEM))
	if err != nil {
		a.appPushError(w, err)
		return
	}
	for _, client := range a.appPushClients {
		client.Retire(m.Topic)
	}
	writeJSON(w, 200, m)
}

func (a *App) sendAppPush(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Environment, Topic, Token, PushType string
		Payload                             jsontext.Value
		Priority                            int
		Expiration                          int64
	}
	if !readAdminJSON(w, r, &in) {
		return
	}
	client := a.appPushClients[in.Environment]
	if client == nil {
		writeError(
			w,
			400,
			fmt.Errorf("%w: environment must be development or production", errOperation),
		)
		return
	}
	token, err := hex.DecodeString(in.Token)
	if err != nil || len(token) == 0 {
		writeError(w, 400, fmt.Errorf("%w: token must be hex", errOperation))
		return
	}
	res := client.Send(
		r.Context(),
		apns.AppRequest{
			Token:      token,
			Topic:      in.Topic,
			PushType:   in.PushType,
			Payload:    in.Payload,
			Priority:   in.Priority,
			Expiration: in.Expiration,
		},
	)
	status := 200
	if res.Err != nil {
		status = 502
		if errors.Is(res.Err, apns.ErrRequest) {
			status = 400
		}
	}
	writeJSON(
		w,
		status,
		map[string]any{
			"Accepted": res.Sent(),
			"Outcome":  res.Outcome,
			"Status":   res.Status,
			"Reason":   res.Reason,
			"APNSID":   res.APNSID,
		},
	)
}

func (a *App) operatorRoutes(ctx context.Context) ([]adminRoute, error) {
	if err := a.wireAppPush(ctx); err != nil {
		return nil, err
	}
	return append(a.appPushRoutes(), a.enrollmentAdminRoutes()...), nil
}
