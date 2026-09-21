package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

const (
	ActionReadInventory     = "readInventory"
	ActionReadRawInventory  = "readRawInventory"
	ActionManageInventory   = "manageInventory"
	ActionManageAxMAccounts = "manageAxMAccounts"
)

// inventoryError maps inventory domain failures to administrative HTTP statuses.
func inventoryError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, inventory.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, inventory.ErrConflict), errors.Is(err, inventory.ErrLease):
		status = http.StatusConflict
	}
	writeError(w, status, err)
}

// inventoryBody decodes one bounded administrative JSON request.
func inventoryBody(r *http.Request, out any) error {
	raw, e := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if e != nil {
		return e
	}
	if len(raw) > MaxAdminBody {
		return inventory.ErrInvalid
	}
	if json.Unmarshal(raw, out) != nil {
		return inventory.ErrInvalid
	}
	return nil
}

// inventoryQuery parses the common list, report and export filter contract.
func inventoryQuery(r *http.Request) (inventory.DeviceQuery, error) {
	v := r.URL.Query()
	q := inventory.DeviceQuery{AccountID: v.Get("account"), Source: v.Get("source"), Focus: v.Get("focus"), Search: v.Get("search"), Cursor: v.Get("cursor")}
	var err error
	if v.Get("limit") != "" {
		q.Limit, err = strconv.Atoi(v.Get("limit"))
		if err != nil {
			return q, inventory.ErrInvalid
		}
	}
	if v.Get("where") != "" {
		if json.Unmarshal([]byte(v.Get("where")), &q.Conditions) != nil {
			return q, inventory.ErrInvalid
		}
	}
	if v.Get("as_of") != "" {
		q.AsOf, err = time.Parse(time.RFC3339Nano, v.Get("as_of"))
		if err != nil {
			return q, inventory.ErrInvalid
		}
	}
	return q, q.Validate()
}

// publicInventoryQuery rejects filters on fields requiring raw-inventory authority.
func publicInventoryQuery(q inventory.DeviceQuery) error {
	for _, c := range q.Conditions {
		if !inventory.PublicField(c.Field) {
			return inventory.ErrInvalid
		}
	}
	return nil
}

// inventoryRoutes declares permission-gated account, device, job and reporting endpoints.
func (a *App) inventoryRoutes() []adminRoute {
	routes := []adminRoute{}
	add := func(pattern, action string, fn func(http.ResponseWriter, *http.Request) (any, error)) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: action, Family: "inventory", MaxResponseBytes: 32 << 20, StreamResponse: pattern == "GET /inventory/raw/exports", LocalMutation: !strings.HasPrefix(pattern, "GET ") && !strings.HasSuffix(pattern, "/verify") && !strings.HasSuffix(pattern, "/collect"), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := &inventoryResponse{ResponseWriter: w}
			out, e := fn(response, r)
			if e != nil {
				if response.started {
					w.Header().Set("X-Inventory-Error", "export_failed")
					return
				}
				inventoryError(w, e)
				return
			}
			if out != nil {
				writeJSON(w, http.StatusOK, out)
			}
		})})
	}
	add("GET /axm/accounts", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) { return a.Inventory.Accounts(r.Context()) })
	add("GET /axm/accounts/{id}", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.Inventory.Account(r.Context(), r.PathValue("id"))
	})
	save := func(_ http.ResponseWriter, r *http.Request) (any, error) {
		var input struct {
			Account    inventory.Account `json:"account"`
			PrivateKey string            `json:"private_key_pem"`
		}
		if e := inventoryBody(r, &input); e != nil {
			return nil, e
		}
		if id := r.PathValue("id"); id != "" {
			input.Account.ID = id
		}
		creating := input.Account.Revision == 0
		for _, name := range input.Account.DEPAccounts {
			if a.dep == nil {
				return nil, inventory.ErrInvalid
			}
			linked, err := a.dep.store.GetAccount(r.Context(), name)
			if err != nil {
				return nil, inventory.ErrInvalid
			}
			if input.Account.AppleOrgID != "" && linked.OrgID != "" && input.Account.AppleOrgID != linked.OrgID {
				return nil, inventory.ErrConflict
			}
		}
		key := []byte(input.PrivateKey)
		checkKey := key
		if len(key) == 0 {
			var e error
			checkKey, e = a.Inventory.PrivateKey(r.Context(), input.Account.ID)
			if e != nil {
				return nil, e
			}
		}
		if _, e := a.inventoryClient(r.Context(), input.Account, checkKey); e != nil {
			return nil, inventory.ErrInvalid
		}
		out, e := a.Inventory.SaveAccount(r.Context(), input.Account, key, time.Now().UTC())
		if e != nil {
			return nil, e
		}
		if creating {
			e = a.initialInventorySync(r.Context(), out)
		}
		return out, e
	}
	add("POST /axm/accounts", ActionManageAxMAccounts, save)
	add("PUT /axm/accounts/{id}", ActionManageAxMAccounts, save)
	add("DELETE /axm/accounts/{id}", ActionManageAxMAccounts, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		rev, e := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if e != nil {
			return nil, inventory.ErrInvalid
		}
		e = a.Inventory.DeleteAccount(r.Context(), r.PathValue("id"), rev, time.Now().UTC())
		return map[string]bool{"deleted": e == nil}, e
	})
	add("POST /axm/accounts/{id}/verify", ActionManageAxMAccounts, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		account, e := a.Inventory.Account(r.Context(), r.PathValue("id"))
		if e != nil {
			return nil, e
		}
		key, e := a.Inventory.PrivateKey(r.Context(), account.ID)
		if e != nil {
			return nil, e
		}
		client, e := a.inventoryClient(r.Context(), account, key)
		if e != nil {
			return nil, inventory.ErrInvalid
		}
		if _, e = client.ListOrgDevices(r.Context(), axm.ListOptions{Limit: 1}); e != nil {
			return nil, errors.New("inventory: Apple verification failed")
		}
		e = a.Inventory.VerifyAccount(r.Context(), account.ID, account.Revision, time.Now().UTC())
		return map[string]bool{"verified": e == nil}, e
	})
	add("POST /axm/accounts/{id}/sync", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.Inventory.Enqueue(r.Context(), r.PathValue("id"), r.URL.Query().Get("force") == "true", time.Now().UTC())
	})
	add("POST /inventory/sync", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		accounts, e := a.Inventory.Accounts(r.Context())
		if e != nil {
			return nil, e
		}
		jobs := []inventory.Job{}
		for _, account := range accounts {
			if account.Enabled {
				j, e := a.Inventory.Enqueue(r.Context(), account.ID, r.URL.Query().Get("force") == "true", time.Now().UTC())
				if e != nil {
					return nil, e
				}
				jobs = append(jobs, j)
			}
		}
		return jobs, nil
	})
	add("POST /inventory/jobs/cancel-all", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		n, e := a.Inventory.CancelAll(r.Context(), time.Now().UTC())
		return map[string]int{"cancelled": n}, e
	})
	add("GET /inventory/jobs", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		limit := 100
		if s := r.URL.Query().Get("limit"); s != "" {
			var e error
			limit, e = strconv.Atoi(s)
			if e != nil {
				return nil, inventory.ErrInvalid
			}
		}
		return a.Inventory.Jobs(r.Context(), r.URL.Query().Get("cursor"), limit)
	})
	add("GET /inventory/jobs/{id}", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.Inventory.Job(r.Context(), r.PathValue("id"))
	})
	add("POST /inventory/jobs/{id}/{action}", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		if e := a.Inventory.Control(r.Context(), r.PathValue("id"), r.PathValue("action"), time.Now().UTC()); e != nil {
			return nil, e
		}
		return a.Inventory.Job(r.Context(), r.PathValue("id"))
	})
	add("GET /inventory/schedules", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) { return a.Inventory.Schedules(r.Context()) })
	add("PUT /inventory/schedules/{id}", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		var s inventory.Schedule
		if e := inventoryBody(r, &s); e != nil {
			return nil, e
		}
		s.AccountID = r.PathValue("id")
		return a.Inventory.SaveSchedule(r.Context(), s, time.Now().UTC())
	})
	for _, raw := range []bool{false, true} {
		prefix := "/devices"
		permission := ActionReadInventory
		if raw {
			prefix = "/inventory/raw/devices"
			permission = ActionReadRawInventory
		}
		add("GET "+prefix, permission, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			q, e := inventoryQuery(r)
			if e != nil {
				return nil, e
			}
			if !raw {
				if e := publicInventoryQuery(q); e != nil {
					return nil, e
				}
			}
			page, e := a.Inventory.Devices(r.Context(), q)
			if !raw {
				for i := range page.Items {
					page.Items[i] = inventory.PublicRecord(page.Items[i])
				}
			}
			return page, e
		})
		add("GET "+prefix+"/fields", permission, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			q, e := inventoryQuery(r)
			if e != nil {
				return nil, e
			}
			if !raw {
				if e := publicInventoryQuery(q); e != nil {
					return nil, e
				}
			}
			return a.Inventory.Fields(r.Context(), q, raw)
		})
		add("GET "+prefix+"/{id}", permission, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			d, e := a.Inventory.Device(r.Context(), r.PathValue("id"))
			if !raw {
				d = inventory.PublicRecord(d)
			}
			return d, e
		})
	}
	add("POST /devices/{id}/collect", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		d, e := a.Inventory.Device(r.Context(), r.PathValue("id"))
		if e != nil {
			return nil, e
		}
		count := 0
		for _, o := range d.Sources {
			if o.Source.Kind == "enrollment" && !o.Disconnected {
				var fields struct {
					ID      string `json:"enrollment_id"`
					Channel string `json:"enrollment_channel"`
				}
				if json.Unmarshal(o.Raw, &fields) != nil {
					return nil, inventory.ErrInvalid
				}
				channel := mdm.ChannelDevice
				if fields.Channel == mdm.ChannelUserEnrollmentDevice.String() {
					channel = mdm.ChannelUserEnrollmentDevice
				}
				n, e := a.collectNative(r.Context(), mdm.EnrollmentID{Channel: channel, ID: fields.ID}, true)
				if e != nil {
					return nil, e
				}
				count += n
			}
		}
		return map[string]int{"queued": count}, nil
	})
	add("GET /inventory/reports", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		q, e := inventoryQuery(r)
		if e != nil {
			return nil, e
		}
		if e := publicInventoryQuery(q); e != nil {
			return nil, e
		}
		return a.Inventory.Report(r.Context(), q)
	})
	for _, raw := range []bool{false, true} {
		path := "/inventory/exports"
		permission := ActionReadInventory
		if raw {
			path = "/inventory/raw/exports"
			permission = ActionReadRawInventory
		}
		add("GET "+path, permission, func(w http.ResponseWriter, r *http.Request) (any, error) {
			q, e := inventoryQuery(r)
			if e != nil {
				return nil, e
			}
			if !raw {
				if e := publicInventoryQuery(q); e != nil {
					return nil, e
				}
			}
			format := r.URL.Query().Get("format")
			if format == "" {
				format = "json"
			}
			var columns []string
			if v := r.URL.Query().Get("columns"); v != "" {
				columns = strings.Split(v, ",")
			}
			w.Header().Set("Content-Type", map[string]string{"csv": "text/csv", "json": "application/json", "ndjson": "application/x-ndjson"}[format])
			w.Header().Set("Trailer", "X-Inventory-Error")
			w.Header().Set("Content-Disposition", `attachment; filename="inventory.`+format+`"`)
			return nil, a.Inventory.Export(r.Context(), w, q, format, columns, raw)
		})
	}
	add("GET /inventory/presets", ActionReadInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) { return a.Inventory.Presets(r.Context()) })
	add("PUT /inventory/presets/{id}", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		var p inventory.ExportPreset
		if e := inventoryBody(r, &p); e != nil {
			return nil, e
		}
		p.ID = r.PathValue("id")
		return a.Inventory.SavePreset(r.Context(), p)
	})
	add("GET /inventory/diagnostics", ActionReadInventory, func(w http.ResponseWriter, r *http.Request) (any, error) {
		w.Header().Set("Trailer", "X-Inventory-Error")
		w.Header().Set("Content-Type", "application/x-ndjson")
		return nil, a.Inventory.Diagnostics(r.Context(), w)
	})
	return routes
}

// inventoryResponse distinguishes validation failures from interrupted streaming responses.
type inventoryResponse struct {
	http.ResponseWriter
	started bool
}

// Write tracks whether an export has already sent bytes before a later storage error.
func (w *inventoryResponse) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}
