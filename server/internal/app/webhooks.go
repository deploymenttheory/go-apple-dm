package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

const (
	ActionManageSensitiveWebhooks = "manageSensitiveWebhooks"
	ActionReplaySensitiveWebhooks = "replaySensitiveWebhooks"
	ActionReadWebhooks            = "readWebhooks"
	ActionManageWebhooks          = "manageWebhooks"
	ActionReadWebhookDeliveries   = "readWebhookDeliveries"
	ActionReplayWebhooks          = "replayWebhooks"
)

// parseWebhookEnv reads managed webhook limits and outbound policy, fixes the source to
// device-management and rejects legacy single-destination settings.
func parseWebhookEnv(cfg *Config, get func(string) string) error {
	if get("DM_WEBHOOK_URL") != "" || get("DM_WEBHOOK_HMAC_KEY") != "" {
		return fmt.Errorf("%w: replace DM_WEBHOOK_URL/DM_WEBHOOK_HMAC_KEY with DM_WEBHOOKS_ENABLED and dmctl webhooks create; existing delivery history is retained", ErrConfig)
	}
	if value := get("DM_WEBHOOKS_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: DM_WEBHOOKS_ENABLED", ErrConfig)
		}
		cfg.Webhooks.Enabled = enabled
	}
	cfg.Webhooks.Source = "device-management"
	cfg.Webhooks.RootCAFile = get("DM_WEBHOOK_ROOT_CA_FILE")
	if value := get("DM_WEBHOOK_PRIVATE_NETWORKS"); value != "" {
		for _, p := range strings.Split(value, ",") {
			cfg.Webhooks.PrivateNetworks = append(cfg.Webhooks.PrivateNetworks, strings.TrimSpace(p))
		}
	}
	for key, target := range map[string]*time.Duration{"DM_WEBHOOK_PAYLOAD_RETENTION": &cfg.Webhooks.PayloadRetention, "DM_WEBHOOK_METADATA_RETENTION": &cfg.Webhooks.MetadataRetention} {
		if value := get(key); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return fmt.Errorf("%w: %s", ErrConfig, key)
			}
			*target = d
		}
	}
	if value := get("DM_WEBHOOK_MAX_BODY_BYTES"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("%w: DM_WEBHOOK_MAX_BODY_BYTES", ErrConfig)
		}
		cfg.Webhooks.MaxBody = n
	}
	return nil
}

// openWebhooks opens encrypted SQL webhook capture, attaches certificate observation and
// registers retention when managed webhooks are enabled.
func (a *App) openWebhooks(ctx context.Context, store *eventstore.Store) error {
	if !a.cfg.Webhooks.Enabled {
		return nil
	}
	cfg := a.cfg.Webhooks
	cfg.Source = "device-management"
	if a.cfg.Clock != nil {
		cfg.Now = a.cfg.Clock.Now
	}
	cfg.CommandType = func(ctx context.Context, id mdm.EnrollmentID, uuid string) (string, error) {
		var typ string
		err := sqlcommon.Query(ctx, a.db).QueryRowContext(ctx, a.dialect.Rebind("SELECT request_type FROM commands WHERE enrollment_id = ? AND command_uuid = ?"), id.ID, uuid).Scan(&typ)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return typ, err
	}
	s, err := webhook.Open(ctx, a.db, a.dialect, a.keyring, store, cfg)
	if err != nil {
		return fmt.Errorf("managed webhooks require SQL and storage encryption: %w", err)
	}
	a.webhooks = s
	if a.Certificates != nil {
		a.Certificates.Store = s.ObserveCertificates(a.Certificates.Store)
	}
	a.addWorker("webhook-retention", s.RunRetention)
	return nil
}

// webhookRoutes declares subscription and delivery routes and evaluates additional Cedar
// grants before allowing sensitive capture or replay operations.
func (a *App) webhookRoutes() []adminRoute {
	if a.webhooks == nil {
		return nil
	}
	routes := []adminRoute{}
	add := func(action, pattern string, mutation bool) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: action, Family: "webhooks", LocalMutation: mutation, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := a.principal(r)
			if err != nil {
				writeError(w, http.StatusUnauthorized, ErrUnauthorized)
				return
			}
			sensitiveAction := ActionManageSensitiveWebhooks
			if action == ActionReplayWebhooks {
				sensitiveAction = ActionReplaySensitiveWebhooks
			}
			decision, err := a.admin.Authorize(r.Context(), principal, sensitiveAction, adminauth.SystemResource, map[string]types.Value{"method": types.String(r.Method)})
			sensitive := err == nil && decision.Allowed
			if sensitive {
				if req, ok := r.Context().Value(authorizationKey{}).(*authorizationRequest); ok {
					req.Decision.Policies = append(req.Decision.Policies, decision.Policies...)
				}
			}
			a.webhooks.Admin(w, r, sensitive)
		})})
	}
	for _, p := range []string{"GET /webhooks", "GET /webhooks/{id}", "GET /webhooks/catalogue", "GET /webhooks/status"} {
		add(ActionReadWebhooks, p, false)
	}
	for _, p := range []string{"GET /webhooks/deliveries", "GET /webhooks/deliveries/{id}"} {
		add(ActionReadWebhookDeliveries, p, false)
	}
	for _, p := range []string{"POST /webhooks", "PUT /webhooks/{id}", "DELETE /webhooks/{id}", "POST /webhooks/{id}/pause", "POST /webhooks/{id}/resume", "POST /webhooks/{id}/enable", "POST /webhooks/{id}/disable", "POST /webhooks/{id}/credentials", "POST /webhooks/{id}/test"} {
		add(ActionManageWebhooks, p, true)
	}
	for _, p := range []string{"POST /webhooks/replays", "POST /webhooks/deliveries/{id}/retry"} {
		add(ActionReplayWebhooks, p, true)
	}
	return routes
}
