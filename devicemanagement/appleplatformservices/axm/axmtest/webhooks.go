package axmtest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"uuid"
)

type (
	testWebhook  struct{ Secret string }
	testDelivery struct {
		ID, WebhookID, EventID, PingID, State string
		Redelivery                            bool
		Created                               time.Time
		Body                                  []byte
	}
)

type webhookRequest struct {
	Data struct {
		Type          string         `json:"type"`
		ID            string         `json:"id"`
		Attributes    map[string]any `json:"attributes"`
		Relationships map[string]struct {
			Data struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"data"`
		} `json:"relationships"`
	} `json:"data"`
}

// resource presents delivery outcomes with optional event linkage.
func (d *testDelivery) resource(include bool) map[string]any {
	out := map[string]any{"type": typeWebhookDeliveries, "id": d.ID, "attributes": map[string]any{"createdDateTime": d.Created, "deliveryState": d.State, "redelivery": d.Redelivery}}
	if include {
		out["relationships"] = map[string]any{"event": map[string]any{"data": map[string]string{"type": typeWebhookEvents, "id": d.EventID}}}
	}
	return out
}

// handleCreateWebhook is POST /v1/webhooks; signing secrets remain separate from resources.
func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req webhookRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeWebhooks {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be a webhooks resource", map[string]any{"pointer": "/data/type"})
		return
	}
	a := req.Data.Attributes
	name, _ := a["name"].(string)
	secret, _ := a["secret"].(string)
	callback, _ := a["url"].(string)
	events, _ := a["eventTypes"].([]any)
	if name == "" || secret == "" || callback == "" || len(events) == 0 {
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.REQUIRED", "name, url, secret and eventTypes required", map[string]any{"pointer": "/data/attributes"})
		return
	}
	delete(a, "secret")
	id := uuid.New().String()
	s.mu.Lock()
	a["createdDateTime"] = s.now().UTC()
	a["updatedDateTime"] = s.now().UTC()
	hook := &resource{typ: typeWebhooks, id: id, attrs: a}
	s.store.webhooks.put(hook)
	s.webhooks[id] = &testWebhook{Secret: secret}
	doc := s.render(hook, nil)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": doc})
}

// handleUpdateWebhook is PATCH /v1/webhooks/{id}, including explicit false updates.
func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req webhookRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeWebhooks || req.Data.ID != id {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must identify the webhook being updated", map[string]any{"pointer": "/data/id"})
		return
	}
	s.mu.Lock()
	hook, found := s.store.webhooks.get(id)
	var doc map[string]any
	if found {
		for key, value := range req.Data.Attributes {
			if key == "secret" {
				if secret, ok := value.(string); ok {
					s.webhooks[id].Secret = secret
				}
			} else {
				hook.attrs[key] = value
			}
		}
		hook.attrs["updatedDateTime"] = s.now().UTC()
		doc = s.render(hook, nil)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeWebhooks, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc})
}

// handleDeleteWebhook is DELETE /v1/webhooks/{id}, removing its signing key as well.
func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	_, found := s.store.webhooks.get(id)
	if found {
		s.store.webhooks.del(id)
		delete(s.webhooks, id)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeWebhooks, id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWebhookDeliveries lists one filtered page with optional included event templates.
func (s *Server) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	selection, ok := s.selectFields(w, r, typeWebhookDeliveries, typeWebhookEvents)
	if !ok {
		return
	}
	eventSelection, ok := s.selectFields(w, r, typeWebhookEvents, typeWebhookDeliveries)
	if !ok {
		return
	}
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	start, e1 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("filter[createdDateTimeAtOrAfter]"))
	end, e2 := time.Parse(time.RFC3339Nano, r.URL.Query().Get("filter[createdDateTimeAtBefore]"))
	if e1 != nil || e2 != nil || !end.After(start) {
		s.badParameter(w, "filter[createdDateTimeAtOrAfter]", "ordered date bounds required")
		return
	}
	states := []string{}
	if value := r.URL.Query().Get("filter[deliveryState]"); value != "" {
		states = strings.Split(value, ",")
	}
	include := r.URL.Query().Get("include") == "event"
	id := r.PathValue("id")
	s.mu.Lock()
	_, found := s.store.webhooks.get(id)
	if !found {
		s.mu.Unlock()
		s.notFound(w, typeWebhooks, id)
		return
	}
	items := []any{}
	events := []any{}
	seen := map[string]bool{}
	for _, did := range s.deliveryOrder {
		d := s.deliveries[did]
		if d.WebhookID != id || d.Created.Before(start) || !d.Created.Before(end) || (len(states) > 0 && !slices.Contains(states, d.State)) {
			continue
		}
		doc := d.resource(include)
		if attrs, ok := doc["attributes"].(map[string]any); ok && selection != nil {
			for name := range attrs {
				if _, selected := selection[name]; !selected {
					delete(attrs, name)
				}
			}
		}
		items = append(items, doc)
		if include && !seen[d.EventID] {
			seen[d.EventID] = true
			var template map[string]any
			_ = json.Unmarshal(d.Body, &template)
			if data, ok := template["data"].(map[string]any); ok {
				data["id"] = ""
			}
			payload, _ := json.Marshal(template)
			events = append(events, s.render(&resource{typ: typeWebhookEvents, id: d.EventID, attrs: map[string]any{"isPingEvent": true, "payload": string(payload), "createdDateTime": d.Created}}, eventSelection))
		}
	}
	s.mu.Unlock()
	s.writePage(w, r, items, limit, offset, events)
}

// handleCreateWebhookPing is POST /v1/webhookPings, retaining an explicitly driven attempt.
func (s *Server) handleCreateWebhookPing(w http.ResponseWriter, r *http.Request) {
	var req webhookRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != "webhookPings" || req.Data.Relationships["webhook"].Data.Type != typeWebhooks {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "webhook linkage required", map[string]any{"pointer": "/data/relationships/webhook"})
		return
	}
	id := req.Data.Relationships["webhook"].Data.ID
	s.mu.Lock()
	_, found := s.store.webhooks.get(id)
	if !found {
		s.mu.Unlock()
		s.notFound(w, typeWebhooks, id)
		return
	}
	ping := uuid.New().String()
	body, _ := json.Marshal(map[string]any{"data": map[string]any{"type": "webhookPingCreated", "id": ping, "version": 1, "attributes": map[string]any{"createdDateTime": s.now().UTC()}}})
	d := &testDelivery{ID: uuid.New().String(), WebhookID: id, EventID: uuid.New().String(), PingID: ping, State: "PENDING", Created: s.now().UTC(), Body: body}
	s.deliveries[d.ID] = d
	s.deliveryOrder = append(s.deliveryOrder, d.ID)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"type": "webhookPings", "id": ping}})
}

// handleCreateWebhookDelivery is POST /v1/webhookDeliveries using a template delivery.
func (s *Server) handleCreateWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	var req webhookRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeWebhookDeliveries || req.Data.Relationships["template"].Data.Type != typeWebhookDeliveries {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "template linkage required", map[string]any{"pointer": "/data/relationships/template"})
		return
	}
	id := req.Data.Relationships["template"].Data.ID
	s.mu.Lock()
	original := s.deliveries[id]
	if original == nil {
		s.mu.Unlock()
		s.notFound(w, typeWebhookDeliveries, id)
		return
	}
	d := *original
	d.ID = uuid.New().String()
	d.Redelivery = true
	d.State = "PENDING"
	d.Created = s.now().UTC()
	s.deliveries[d.ID] = &d
	s.deliveryOrder = append(s.deliveryOrder, d.ID)
	doc := d.resource(true)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": doc})
}

// DeliverWebhook sends one modeled attempt using a caller-supplied HTTP client.
// The fake never starts external callbacks automatically. Tests supply TLS trust
// explicitly; signing and POST behavior are independent of the production codec.
func (s *Server) DeliverWebhook(ctx context.Context, id string, client *http.Client) error {
	if client == nil {
		return errors.New("axmtest: callback client required")
	}
	s.mu.Lock()
	d := s.deliveries[id]
	if d == nil {
		s.mu.Unlock()
		return errors.New("axmtest: delivery missing")
	}
	hook := s.webhooks[d.WebhookID]
	if hook == nil {
		s.mu.Unlock()
		return errors.New("axmtest: webhook missing")
	}
	resource, exists := s.store.webhooks.get(d.WebhookID)
	if !exists {
		s.mu.Unlock()
		return errors.New("axmtest: webhook missing")
	}
	callback, _ := resource.attrs["url"].(string)
	secret := hook.Secret
	body := append([]byte(nil), d.Body...)
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callback, bytes.NewReader(body))
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	req.Header.Set("x-apple-signature", "hmacsha256="+hex.EncodeToString(mac.Sum(nil)))
	response, err := client.Do(req)
	state := "FAILED"
	if err == nil {
		_ = response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			state = "SUCCEEDED"
		} else {
			err = errors.New("axmtest: callback rejected")
		}
	}
	s.mu.Lock()
	d.State = state
	s.mu.Unlock()
	return err
}
