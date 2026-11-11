package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/server/axmwebhook"
)

// axmWebhookNotification acknowledges only authenticated, committed receipt/work.
func (a *App) axmWebhookNotification(w http.ResponseWriter, r *http.Request) {
	_, err := a.AXMWebhooks.Receive(r.Context(), r.PathValue("binding"), r)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, axm.ErrWebhookSignature):
		status = http.StatusUnauthorized
	case errors.Is(err, axm.ErrWebhookNotification):
		status = http.StatusBadRequest
	case errors.Is(err, inventory.ErrNotFound):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": "callback_not_accepted"})
}

// axmWebhookRoutes keeps local transactions separate from remote network calls.
func (a *App) axmWebhookRoutes(add func(string, string, func(http.ResponseWriter, *http.Request) (any, error))) {
	if a.AXMWebhooks == nil {
		return
	}
	const prefix = "/axm/accounts/{id}/"
	add("GET "+prefix+"webhook-bindings", ActionReadBusinessMgr, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.AXMWebhooks.Bindings(r.Context(), r.PathValue("id"), r.URL.Query().Get("after"))
	})
	add("GET "+prefix+"webhook-bindings/{binding}", ActionReadBusinessMgr, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.AXMWebhooks.Binding(r.Context(), r.PathValue("id"), r.PathValue("binding"))
	})
	for _, raw := range []bool{false, true} {
		path, action := "webhook-bindings/{binding}/receipts", ActionReadBusinessMgr
		if raw {
			path += "/raw"
			action = ActionReadRawInventory
		}
		add("GET "+prefix+path, action, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			return a.AXMWebhooks.Receipts(r.Context(), r.PathValue("id"), r.PathValue("binding"), r.URL.Query().Get("after"), raw)
		})
	}
	add("GET "+prefix+"webhooks", ActionReadBusinessMgr, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		client, fence, err := a.accountClient(r.Context(), r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		out, remoteErr := client.ListWebhooks(r.Context(), listOptions(r))
		if err := fence(); err != nil {
			return nil, err
		}
		if remoteErr != nil {
			return nil, accountFailure(remoteErr)
		}
		return out, nil
	})
	add("POST "+prefix+"webhooks", ActionManageAxMAccounts, a.createAXMWebhook)
	add("POST "+prefix+"webhook-bindings/{binding}/reconcile", ActionManageAxMAccounts, a.reconcileAXMWebhook)
	for _, op := range []struct{ method, path, action string }{
		{"GET", "", ActionReadBusinessMgr},
		{"PATCH", "", ActionManageAxMAccounts},
		{"DELETE", "", ActionManageAxMAccounts},
		{"GET", "/deliveries", ActionReadBusinessMgr},
		{"POST", "/ping", ActionManageAxMAccounts},
		{"POST", "/redeliver", ActionManageAxMAccounts},
		{"POST", "/rotate", ActionManageAxMAccounts},
	} {
		add(op.method+" "+prefix+"webhooks/{binding}"+op.path, op.action, func(w http.ResponseWriter, r *http.Request) (any, error) { return a.axmWebhookOperation(w, r, op.path) })
	}
}

// createAXMWebhook retains provisioning intent before creating the remote subscription.
func (a *App) createAXMWebhook(_ http.ResponseWriter, r *http.Request) (any, error) {
	var input struct {
		Name       string   `json:"name"`
		Enabled    bool     `json:"enabled"`
		EventTypes []string `json:"event_types"`
		PublicURL  string   `json:"public_url"`
	}
	if err := inventoryBody(r, &input); err != nil {
		return nil, err
	}
	if input.Name == "" || len(input.EventTypes) == 0 {
		return nil, inventory.ErrInvalid
	}
	if input.PublicURL == "" {
		input.PublicURL = a.cfg.Enroll.PublicURL
	}
	client, fence, err := a.accountClient(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	b, err := a.AXMWebhooks.Prepare(r.Context(), r.PathValue("id"), input.PublicURL)
	if err != nil {
		return nil, err
	}
	secret, err := a.AXMWebhooks.SigningSecret(r.Context(), b)
	if err != nil {
		return nil, err
	}
	remote, err := client.CreateWebhook(r.Context(), axm.WebhookCreateAttributes{Name: input.Name, Enabled: input.Enabled, EventTypes: input.EventTypes, URL: b.CallbackURL, Secret: string(secret)})
	if err != nil {
		a.markAXMWebhookUncertain(r.Context(), b)
		return nil, accountFailure(err)
	}
	if err := fence(); err != nil {
		return nil, err
	}
	state := "active"
	if !input.Enabled {
		state = "disabled"
	}
	return a.AXMWebhooks.SetState(r.Context(), b, state, remote.ID)
}

// reconcileAXMWebhook attaches a known remote subscription after verifying the callback URL.
func (a *App) reconcileAXMWebhook(_ http.ResponseWriter, r *http.Request) (any, error) {
	var input struct {
		RemoteID string `json:"remote_id"`
	}
	if err := inventoryBody(r, &input); err != nil {
		return nil, err
	}
	b, err := a.AXMWebhooks.Binding(r.Context(), r.PathValue("id"), r.PathValue("binding"))
	if err != nil {
		return nil, err
	}
	client, fence, err := a.accountClientAt(r.Context(), b.AccountID, b.AccountRevision, b.AccountCreatedAt)
	if err != nil {
		return nil, err
	}
	remote, err := client.GetWebhook(r.Context(), input.RemoteID, axm.GetOptions{})
	if err != nil {
		return nil, accountFailure(err)
	}
	if err := fence(); err != nil {
		return nil, err
	}
	if remote.Attributes.URL != b.CallbackURL {
		return nil, inventory.ErrConflict
	}
	// Apple's secret is write-only. Reassert the retained key explicitly so an
	// uncertain rotation cannot be activated with an unknown remote key.
	secret, err := a.AXMWebhooks.SigningSecret(r.Context(), b)
	if err != nil {
		return nil, err
	}
	value := string(secret)
	if _, err := client.UpdateWebhook(r.Context(), remote.ID, axm.WebhookUpdateAttributes{Secret: &value}); err != nil {
		return nil, accountFailure(err)
	}
	if err := fence(); err != nil {
		return nil, err
	}
	state := "active"
	if !remote.Attributes.Enabled {
		state = "disabled"
	}
	return a.AXMWebhooks.SetState(r.Context(), b, state, remote.ID)
}

// markAXMWebhookUncertain retains an uncertain outcome using a bounded independent cleanup context.
func (a *App) markAXMWebhookUncertain(ctx context.Context, b axmwebhook.Binding) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := a.AXMWebhooks.SetState(cleanup, b, "uncertain", b.RemoteID); err != nil {
		a.cfg.Logger.WarnContext(cleanup, "AXM webhook outcome remains pending reconciliation")
	}
}

// axmWebhookOperation executes named-account management outside local transactions and fences results.
func (a *App) axmWebhookOperation(w http.ResponseWriter, r *http.Request, op string) (any, error) {
	b, err := a.AXMWebhooks.Binding(r.Context(), r.PathValue("id"), r.PathValue("binding"))
	if err != nil {
		return nil, err
	}
	if b.RemoteID == "" {
		return nil, inventory.ErrConflict
	}
	client, fence, err := a.accountClientAt(r.Context(), b.AccountID, b.AccountRevision, b.AccountCreatedAt)
	if err != nil {
		return nil, err
	}
	var out any
	switch op {
	case "/ping":
		out, err = client.CreateWebhookPing(r.Context(), b.RemoteID)
	case "/redeliver":
		var input struct {
			TemplateID string    `json:"template_id"`
			Start      time.Time `json:"start"`
			End        time.Time `json:"end"`
		}
		if e := inventoryBody(r, &input); e != nil {
			return nil, e
		}
		first, e := client.ListWebhookDeliveries(r.Context(), b.RemoteID, axm.WebhookDeliveryQuery{Start: input.Start, End: input.End})
		if e != nil {
			return nil, accountFailure(e)
		}
		found := false
		for page, e := range axm.Pages(r.Context(), client, first) {
			if e != nil {
				return nil, accountFailure(e)
			}
			for _, d := range page.Items {
				found = found || d.ID == input.TemplateID
			}
		}
		if !found {
			return nil, inventory.ErrNotFound
		}
		out, err = client.CreateWebhookDelivery(r.Context(), input.TemplateID)
	case "/deliveries":
		start, e1 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("start"))
		end, e2 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end"))
		if e1 != nil || e2 != nil {
			return nil, inventory.ErrInvalid
		}
		raw := r.URL.Query().Get("raw") == "true"
		if raw && !a.authorizeInventoryRaw(w, r) {
			return nil, nil
		}
		query := axm.WebhookDeliveryQuery{ListOptions: listOptions(r), Start: start, End: end, IncludeEvent: raw}
		if !raw {
			query.Fields = []string{"deliveryState", "redelivery", "createdDateTime", "sentDateTime"}
		}
		page, remoteErr := client.ListWebhookDeliveries(r.Context(), b.RemoteID, query)
		err = remoteErr
		if !raw {
			page.Raw = nil
			page.Included = nil
			for i := range page.Items {
				page.Items[i].Raw = nil
				page.Items[i].Included = nil
				page.Items[i].Attributes.Request = nil
				page.Items[i].Attributes.Response = nil
				page.Items[i].Attributes.ErrorMessage = ""
			}
		}
		out = page
	case "/rotate":
		var input struct {
			OverlapSeconds int64 `json:"overlap_seconds"`
		}
		if e := inventoryBody(r, &input); e != nil {
			return nil, e
		}
		if input.OverlapSeconds < 0 || input.OverlapSeconds > 86400 {
			return nil, inventory.ErrInvalid
		}
		b, err = a.AXMWebhooks.Rotate(r.Context(), b, time.Duration(input.OverlapSeconds)*time.Second)
		if err != nil {
			return nil, err
		}
		secret, e := a.AXMWebhooks.SigningSecret(r.Context(), b)
		if e != nil {
			return nil, e
		}
		value := string(secret)
		var remote *axm.Webhook
		remote, err = client.UpdateWebhook(r.Context(), b.RemoteID, axm.WebhookUpdateAttributes{Secret: &value})
		if err == nil {
			state := "active"
			if !remote.Attributes.Enabled {
				state = "disabled"
			}
			out, err = a.AXMWebhooks.SetState(r.Context(), b, state, b.RemoteID)
		}
	default:
		switch r.Method {
		case http.MethodGet:
			out, err = client.GetWebhook(r.Context(), b.RemoteID, axm.GetOptions{})
		case http.MethodPatch:
			var attrs axm.WebhookUpdateAttributes
			if e := inventoryBody(r, &attrs); e != nil {
				return nil, e
			}
			if attrs.URL != nil || attrs.Secret != nil {
				return nil, inventory.ErrInvalid
			}
			b, err = a.AXMWebhooks.SetState(r.Context(), b, "uncertain", b.RemoteID)
			if err != nil {
				return nil, err
			}
			var remote *axm.Webhook
			remote, err = client.UpdateWebhook(r.Context(), b.RemoteID, attrs)
			if err == nil {
				state := "active"
				if !remote.Attributes.Enabled {
					state = "disabled"
				}
				out, err = a.AXMWebhooks.SetState(r.Context(), b, state, b.RemoteID)
			}
		case http.MethodDelete:
			b, err = a.AXMWebhooks.SetState(r.Context(), b, "uncertain", b.RemoteID)
			if err != nil {
				return nil, err
			}
			err = client.DeleteWebhook(r.Context(), b.RemoteID)
			if err == nil || axm.IsNotFound(err) {
				out, err = a.AXMWebhooks.SetState(r.Context(), b, "disabled", b.RemoteID)
			}
		}
	}
	if e := fence(); e != nil {
		return nil, e
	}
	if err != nil {
		var remote *axm.Error
		var auth *axm.AuthError
		if errors.As(err, &remote) || errors.As(err, &auth) || errors.Is(err, axm.ErrTransport) {
			return nil, accountFailure(err)
		}
		return nil, err
	}
	return out, nil
}
