package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// accountRemoteError contains a reviewed category only. Apple error bodies, OAuth
// descriptions and request URLs may carry credentials and are never returned.
type accountRemoteError struct {
	status   int
	category string
}

// Error returns only the reviewed administrative category.
func (e *accountRemoteError) Error() string { return "axm: " + e.category }

// accountFailure maps remote failures without retaining credential-bearing details.
func accountFailure(err error) *accountRemoteError {
	category := "connection_failed"
	var api *axm.Error
	switch {
	case axm.IsUnauthorized(err):
		category = "authentication_failed"
	case errors.As(err, &api) && api.Status == http.StatusForbidden:
		category = "permission_denied"
	case axm.IsNotFound(err):
		category = "resource_not_found"
	case axm.IsRateLimited(err):
		category = "rate_limited"
	}
	return &accountRemoteError{status: axmStatus(err), category: category}
}

// accountClient always reads the selected account's current credential. Neither a
// missing account nor a revoked credential can fall back to the startup client.
func (a *App) accountClient(ctx context.Context, id string) (*axm.Client, func() error, error) {
	account, key, err := a.accountSnapshot(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !account.Enabled {
		return nil, nil, &accountRemoteError{status: http.StatusConflict, category: "account_disabled"}
	}
	client, err := a.inventoryClient(ctx, account, key)
	if err != nil {
		return nil, nil, &accountRemoteError{status: http.StatusBadGateway, category: "credential_unavailable"}
	}
	return client, func() error {
		current, currentKey, err := a.accountSnapshot(ctx, id)
		if err != nil {
			return err
		}
		if current.Revision != account.Revision || !current.CreatedAt.Equal(account.CreatedAt) || !bytes.Equal(currentKey, key) {
			return inventory.ErrConflict
		}
		return nil
	}, nil
}

// accountSnapshot reads metadata and its sealed credential in one short local
// transaction. No connector or remote authentication exists inside this boundary.
func (a *App) accountSnapshot(ctx context.Context, id string) (inventory.Account, []byte, error) {
	var account inventory.Account
	var key []byte
	err := a.Inventory.Backend.Update(ctx, func(tx inventory.Tx) error {
		var err error
		account, key, err = readAccountSnapshot(ctx, tx, id)
		return err
	})
	return account, key, err
}

// readAccountSnapshot decodes metadata and its key from the same locked view.
func readAccountSnapshot(ctx context.Context, reader inventory.Reader, id string) (inventory.Account, []byte, error) {
	var account inventory.Account
	var key []byte
	raw, err := reader.Get(ctx, "account/"+id)
	if err != nil {
		return account, nil, err
	}
	if err = json.Unmarshal(raw, &account); err != nil {
		return account, nil, err
	}
	raw, err = reader.Get(ctx, "credential/"+id)
	if err != nil {
		return account, nil, err
	}
	err = json.Unmarshal(raw, &key)
	return account, key, err
}

// verifyNamedAccount authenticates outside a transaction, then atomically stamps
// only the exact account incarnation, revision and key that reached Apple.
func (a *App) verifyNamedAccount(ctx context.Context, id string) error {
	account, key, err := a.accountSnapshot(ctx, id)
	if err != nil {
		return err
	}
	client, err := a.inventoryClient(ctx, account, key)
	if err != nil {
		return &accountRemoteError{status: http.StatusBadGateway, category: "credential_unavailable"}
	}
	if _, err = client.ListOrgDevices(ctx, axm.ListOptions{Limit: 1}); err != nil {
		return accountFailure(err)
	}
	return a.Inventory.Backend.Update(ctx, func(tx inventory.Tx) error {
		current, currentKey, err := readAccountSnapshot(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Revision != account.Revision || !current.CreatedAt.Equal(account.CreatedAt) || !bytes.Equal(currentKey, key) {
			return inventory.ErrConflict
		}
		at := time.Now().UTC()
		current.VerifiedAt = &at
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		return tx.Put(ctx, "account/"+id, raw)
	})
}

// accountOperationRoutes keeps every live read and durable operation inspection
// under explicit account selection. Apple calls run outside local transactions.
func (a *App) accountOperationRoutes(add func(string, string, func(http.ResponseWriter, *http.Request) (any, error))) {
	live := func(path string, raw bool, fn func(*http.Request, *axm.Client) (any, error)) {
		add("GET /axm/accounts/{id}/"+path, ActionReadBusinessMgr, func(w http.ResponseWriter, r *http.Request) (any, error) {
			if raw && !a.authorizeInventoryRaw(w, r) {
				return nil, nil
			}
			client, fence, err := a.accountClient(r.Context(), r.PathValue("id"))
			if err != nil {
				return nil, err
			}
			out, err := fn(r, client)
			if changed := fence(); changed != nil {
				return nil, changed
			}
			if err != nil {
				return nil, accountFailure(err)
			}
			return out, nil
		})
	}
	live("devices", true, func(r *http.Request, c *axm.Client) (any, error) {
		return c.ListOrgDevices(r.Context(), listOptions(r))
	})
	live("devices/{device}", true, func(r *http.Request, c *axm.Client) (any, error) {
		return c.GetOrgDevice(r.Context(), r.PathValue("device"), axm.GetOptions{})
	})
	live("devices/{device}/coverage", true, func(r *http.Request, c *axm.Client) (any, error) {
		return c.ListAppleCareCoverage(r.Context(), r.PathValue("device"), listOptions(r))
	})
	live("devices/{device}/assigned-service", false, func(r *http.Request, c *axm.Client) (any, error) {
		return c.GetAssignedServerLinkage(r.Context(), r.PathValue("device"))
	})
	live("services", false, func(r *http.Request, c *axm.Client) (any, error) {
		return c.ListMDMServers(r.Context(), listOptions(r))
	})
	live("services/{service}", false, func(r *http.Request, c *axm.Client) (any, error) {
		return c.GetMDMServer(r.Context(), r.PathValue("service"), axm.GetOptions{})
	})
	add("GET /axm/accounts/{id}/jobs/{job}", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		job, err := a.Inventory.Job(r.Context(), r.PathValue("job"))
		if err != nil {
			return nil, err
		}
		if job.AccountID != r.PathValue("id") {
			return nil, inventory.ErrNotFound
		}
		return job, nil
	})
	add("GET /axm/accounts/{id}/jobs", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		if _, err := a.Inventory.Account(r.Context(), r.PathValue("id")); err != nil {
			return nil, err
		}
		limit := 100
		if value := r.URL.Query().Get("limit"); value != "" {
			var err error
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 1000 {
				return nil, inventory.ErrInvalid
			}
		}

		out := struct {
			Items      []inventory.Job `json:"items"`
			NextCursor string          `json:"next_cursor,omitempty"`
		}{Items: []inventory.Job{}}
		cursor := r.URL.Query().Get("cursor")
		for {
			jobs, err := a.Inventory.Jobs(r.Context(), cursor, 100)
			if err != nil {
				return nil, err
			}
			for _, job := range jobs {
				if job.AccountID != r.PathValue("id") {
					continue
				}
				if len(out.Items) == limit {
					out.NextCursor = out.Items[len(out.Items)-1].ID
					return out, nil
				}
				out.Items = append(out.Items, job)
			}
			if len(jobs) < 100 {
				break
			}
			cursor = jobs[len(jobs)-1].ID
		}
		return out, nil
	})
	add("GET /axm/accounts/{id}/diagnostics", ActionReadBusinessMgr, func(_ http.ResponseWriter, r *http.Request) (any, error) { return a.accountDiagnostics(r) })
}

// accountDiagnostics separates present connectivity from retained synchronization
// progress; an empty first page never proves a complete local snapshot.
func (a *App) accountDiagnostics(r *http.Request) (any, error) {
	id := r.PathValue("id")
	client, fence, err := a.accountClient(r.Context(), id)
	if err != nil {
		return nil, err
	}
	out := struct {
		AccountID       string `json:"account_id"`
		Connection      string `json:"connection"`
		Inventory       string `json:"inventory"`
		Synchronization string `json:"synchronization"`
	}{AccountID: id, Connection: "connected", Inventory: "unknown", Synchronization: "incomplete"}
	devices, err := client.ListOrgDevices(r.Context(), axm.ListOptions{Limit: 1})
	if changed := fence(); changed != nil {
		return nil, changed
	}
	if err != nil {
		out.Connection = accountFailure(err).category
		return out, nil
	}
	out.Inventory = "present"
	if len(devices.Items) == 0 {
		out.Inventory = "empty"
	}
	// The server stamps account creation and job enqueue times. A retained job
	// predating this incarnation cannot establish its synchronization, even when
	// deletion and recreation reuse the account ID and revision.
	account, err := a.Inventory.Account(r.Context(), id)
	if err != nil {
		return nil, err
	}
	var latest inventory.Job
	cursor := ""
	for {
		jobs, err := a.Inventory.Jobs(r.Context(), cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			if job.AccountID == id && job.Revision == account.Revision && !job.CreatedAt.Before(account.CreatedAt) && (latest.ID == "" || job.CreatedAt.After(latest.CreatedAt)) {
				latest = job
			}
		}
		if len(jobs) < 100 {
			break
		}
		cursor = jobs[len(jobs)-1].ID
	}
	if latest.State == "success" {
		out.Synchronization = "complete"
	}
	if err := fence(); err != nil {
		return nil, err
	}
	return out, nil
}
