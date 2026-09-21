package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// openInventory opens shared persistence, imports legacy configuration and registers supervised workers.
func (a *App) openInventory(ctx context.Context) error {
	var backend inventory.Backend = inventory.NewMemory()
	if a.db != nil {
		st, e := inventorystore.Open(ctx, a.db, a.dialect, a.keyring)
		if e != nil {
			return e
		}
		backend = st
	}
	r, e := inventory.New(backend)
	if e != nil {
		return e
	}
	a.Inventory = r
	if c := a.cfg.AxM; c.Enabled() {
		if _, e := r.Account(ctx, "default"); errors.Is(e, inventory.ErrNotFound) {
			key := c.KeyPEM
			if len(key) == 0 {
				key, e = os.ReadFile(c.KeyFile)
				if e != nil {
					return fmt.Errorf("%w: AxM private key: %w", ErrConfig, e)
				}
			}
			account, e := r.SaveAccount(ctx, inventory.Account{ID: "default", Name: "Default", ClientID: c.ClientID, KeyID: c.KeyID, Scope: c.Scope, BaseURL: c.BaseURL, TokenURL: c.TokenURL, Enabled: true}, key, time.Now().UTC())
			if e != nil {
				return e
			}
			if e := a.initialInventorySync(ctx, account); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
	}
	syncer := &inventory.Syncer{Repository: r, Client: a.inventoryClient, JobRetention: a.cfg.InventoryJobRetention}
	a.addWorker("inventory-axm", syncer.Run)
	a.addWorker("inventory-native", a.runNativeInventory)
	return nil
}

// inventoryClient constructs an isolated AxM client using the configured outbound TLS trust.
func (a *App) inventoryClient(ctx context.Context, account inventory.Account, key []byte) (*axm.Client, error) {
	transport, e := outboundClient(a.cfg.AxM.HTTPClient, a.cfg.AxM.RootCAFile)
	if e != nil {
		return nil, e
	}
	return axm.New(ctx, axm.Config{ClientID: account.ClientID, KeyID: account.KeyID, PrivateKeyPEM: key, Scope: account.Scope, BaseURL: account.BaseURL, TokenURL: account.TokenURL, HTTPClient: transport, Logger: a.cfg.Logger, PageCap: 100000})
}

// initialInventorySync installs the default daily schedule and queues initial discovery.
func (a *App) initialInventorySync(ctx context.Context, account inventory.Account) error {
	now := time.Now().UTC()
	if _, e := a.Inventory.SaveSchedule(ctx, inventory.Schedule{AccountID: account.ID, Expression: "0 2 * * *", TimeZone: "UTC", Enabled: account.Enabled}, now); e != nil {
		return e
	}
	if account.Enabled {
		_, e := a.Inventory.Enqueue(ctx, account.ID, false, now)
		return e
	}
	return nil
}

// deviceChannel identifies channels allowed to enrich a physical device record.
func deviceChannel(id mdm.EnrollmentID) bool {
	return id.Channel == mdm.ChannelDevice || id.Channel == mdm.ChannelUserEnrollmentDevice
}

// observeInventoryEnrollment projects public enrollment facts while excluding push and escrow credentials.
func (a *App) observeInventoryEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	if !deviceChannel(id) {
		return nil
	}
	e, err := a.Store.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	at := e.LastSeenAt
	if at.IsZero() {
		at = e.EnrolledAt
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	body := map[string]any{"managed": e.Enabled, "enrollment_id": id.ID, "enrollment_channel": id.Channel.String(), "enrolled_at": e.EnrolledAt, "last_seen": e.LastSeenAt, "disabled_at": e.DisabledAt}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	o, err := inventory.ResourceObservation(inventory.EnrollmentReference("enrollment", id), raw, at, 24*time.Hour)
	if err != nil {
		return err
	}
	if _, err = a.Inventory.Observe(ctx, e.Device.SerialNumber, o); err != nil {
		return err
	}
	raw, err = json.Marshal(e.Device)
	if err != nil {
		return err
	}
	o, err = inventory.ResourceObservation(inventory.EnrollmentReference("mdm.identity", id), raw, e.EnrolledAt, 24*time.Hour)
	if err != nil {
		return err
	}
	if o.ObservedAt.IsZero() {
		o.ObservedAt = at
	}
	_, err = a.Inventory.Observe(ctx, e.Device.SerialNumber, o)
	return err
}

// observeInventoryResult resolves the stored command type before projecting an acknowledged response.
func (a *App) observeInventoryResult(ctx context.Context, id mdm.EnrollmentID, response *mdm.Response, at time.Time) error {
	if !deviceChannel(id) {
		return nil
	}
	// Resolve the server's stored command type; a response cannot supply its own type.
	p := paging.Page{Limit: 100}
	for {
		page, e := a.Store.Commands(ctx, id, storage.CommandQuery{}, p)
		if e != nil {
			return e
		}
		for _, q := range page.Items {
			if q.Command.UUID == response.CommandUUID {
				if response.Status == mdm.StatusAcknowledged {
					return a.projectInventoryCommand(ctx, id, q.Command.RequestType, response.Raw, at)
				}
				if response.Status == mdm.StatusError || response.Status == mdm.StatusCommandFormatError || response.Status == mdm.StatusNotNow {
					return a.Inventory.Attempt(ctx, inventory.EnrollmentReference("mdm."+q.Command.RequestType, id), at, "mdm_"+string(response.Status))
				}
				return nil
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		p.Cursor = page.NextCursor
	}
}

// projectInventoryCommand retains full inventory plists and queryable response fields.
func (a *App) projectInventoryCommand(ctx context.Context, id mdm.EnrollmentID, kind string, raw []byte, at time.Time) error {
	switch kind {
	case "DeviceInformation", "SecurityInfo", "ProfileList", "InstalledApplicationList", "CertificateList":
	default:
		return nil
	}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	var body map[string]any
	if err = plist.Unmarshal(raw, &body); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"response": body, "plist": raw})
	if err != nil {
		return err
	}
	o, err := inventory.ResourceObservation(inventory.EnrollmentReference("mdm."+kind, id), data, at, 24*time.Hour)
	if err != nil {
		return err
	}
	o.Fields = inventory.Flatten(mustInventoryJSON(body))
	serial := e.Device.SerialNumber
	if value := inventory.String(o.Fields["QueryResponses.SerialNumber"]); value != "" {
		serial = value
	}
	_, err = a.Inventory.Observe(ctx, serial, o)
	return err
}

// mustInventoryJSON encodes an already JSON-compatible decoded response.
func mustInventoryJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }

// observeInventoryStatus projects validated device-channel DDM status into the shared record.
func (a *App) observeInventoryStatus(ctx context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate) error {
	if !deviceChannel(id) {
		return nil
	}
	e, err := a.Store.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return a.Inventory.ObserveStatus(ctx, "", id, u)
	}
	if err != nil {
		return err
	}
	return a.Inventory.ObserveStatus(ctx, e.Device.SerialNumber, id, u)
}

// collectNative queues eligible inventory commands and sends their ordinary APNs wake.
func (a *App) collectNative(ctx context.Context, id mdm.EnrollmentID, force bool) (int, error) {
	enrollment, err := a.Store.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if !deviceChannel(id) || !enrollment.Enabled {
		return 0, inventory.ErrInvalid
	}
	target, err := service.EnrollmentTarget(ctx, a.Store, id)
	if err != nil {
		return 0, err
	}
	queries := []string{}
	if target.OS == "" || target.Version.IsZero() {
		queries = []string{"SerialNumber", "OSVersion", "BuildVersion", "ProductName"}
		if target.UserEnrollment {
			queries = []string{"OSVersion", "BuildVersion"}
		}
	} else {
		for _, path := range support.Paths("commands") {
			if name, ok := strings.CutPrefix(path, "DeviceInformation.Queries.QueriesItem."); ok && !strings.Contains(name, ".") {
				if entry := commands.Support(path); entry != nil && entry.Check(target).Supported {
					queries = append(queries, name)
				}
			}
		}
	}
	payloads := []commands.Command{&commands.DeviceInformation{Queries: queries}, &commands.SecurityInfo{}, &commands.ProfileList{}, &commands.InstalledApplicationList{}, &commands.CertificateList{}}
	interval := a.cfg.InventoryNativeInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	count := 0
	for _, payload := range payloads {
		kind := payload.RequestTypeName()
		if !force {
			page, e := a.Store.Commands(ctx, id, storage.CommandQuery{RequestType: kind}, paging.Page{Limit: 1})
			if e != nil {
				return count, e
			}
			if len(page.Items) > 0 && time.Since(page.Items[0].EnqueuedAt) < interval {
				expanded := false
				if kind == "DeviceInformation" && len(queries) > 4 {
					var previous struct{ Command struct{ Queries []string } }
					if plist.Unmarshal(page.Items[0].Command.Raw, &previous) == nil {
						expanded = len(previous.Command.Queries) < len(queries)
					}
				}
				if !expanded {
					continue
				}
			}
		}
		cmd, e := mdm.NewCommand(payload)
		if e != nil {
			return count, e
		}
		result, e := a.Core.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{DedupeKey: "inventory/" + kind})
		if e != nil {
			return count, e
		}
		count += len(result.Queued)
	}
	if count > 0 && a.Push != nil {
		if _, err := a.Push.Notify(ctx, []mdm.EnrollmentID{id}); err != nil {
			return count, err
		}
	}
	return count, nil
}

// runNativeInventory runs bounded native collection sweeps until shutdown.
func (a *App) runNativeInventory(ctx context.Context) error {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		if err := a.refreshNativeInventory(ctx); err != nil && ctx.Err() == nil {
			a.cfg.Logger.WarnContext(ctx, "native inventory refresh failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// refreshNativeInventory backfills existing observations and collects due native inventory.
func (a *App) refreshNativeInventory(ctx context.Context) error {
	p := paging.Page{Limit: 100}
	for {
		page, e := a.Store.List(ctx, storage.EnrollmentQuery{}, p)
		if e != nil {
			return e
		}
		for _, enrollment := range page.Items {
			if !deviceChannel(enrollment.ID) {
				continue
			}
			if e := a.observeInventoryEnrollment(ctx, enrollment.ID); e != nil {
				return e
			}
			if err := a.backfillInventoryStatus(ctx, enrollment); err != nil {
				return err
			}
			// Backfill acknowledged command results idempotently, newest successful snapshot wins.
			for _, kind := range []string{"DeviceInformation", "SecurityInfo", "ProfileList", "InstalledApplicationList", "CertificateList"} {
				results, e := a.Store.Commands(ctx, enrollment.ID, storage.CommandQuery{RequestType: kind, States: []storage.State{storage.StateAcknowledged}}, paging.Page{Limit: 1})
				if e != nil {
					return e
				}
				if len(results.Items) > 0 {
					q := results.Items[0]
					if q.Result != nil {
						if e := a.projectInventoryCommand(ctx, enrollment.ID, kind, q.Result.Raw, q.CompletedAt); e != nil {
							return e
						}
					}
				}
			}
			if enrollment.Enabled {
				if _, e := a.collectNative(ctx, enrollment.ID, false); e != nil {
					return e
				}
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		p.Cursor = page.NextCursor
	}
}

// observeInventoryCertificate uses the authenticated enrollment certificate, not arbitrary installed certificates.
func (a *App) observeInventoryCertificate(ctx context.Context, id mdm.EnrollmentID, cert *x509.Certificate, at time.Time) error {
	if !deviceChannel(id) || cert == nil {
		return nil
	}
	enrollment, e := a.Store.Get(ctx, id)
	if errors.Is(e, storage.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	raw, e := json.Marshal(map[string]any{"identity_certificate_expiry": cert.NotAfter, "identity_certificate_issued": cert.NotBefore})
	if e != nil {
		return e
	}
	o, e := inventory.ResourceObservation(inventory.EnrollmentReference("mdm.certificate", id), raw, at, 24*time.Hour)
	if e != nil {
		return e
	}
	_, e = a.Inventory.Observe(ctx, enrollment.Device.SerialNumber, o)
	return e
}

// backfillInventoryStatus projects existing effective DDM values on upgrades, preserving per-item age.
func (a *App) backfillInventoryStatus(ctx context.Context, enrollment storage.Enrollment) error {
	ref := inventory.EnrollmentReference("ddm.status", enrollment.ID)
	if _, e := a.Inventory.SourceDevice(ctx, ref); e == nil {
		return nil
	} else if !errors.Is(e, inventory.ErrNotFound) {
		return e
	}
	values := []ddm.StatusValue{}
	p := paging.Page{Limit: 250}
	at := time.Time{}
	for {
		page, e := a.Engine.StatusValues(ctx, enrollment.ID, ddm.StatusValueQuery{}, p)
		if e != nil {
			return e
		}
		for _, v := range page.Items {
			values = append(values, v)
			if v.LastSeen.After(at) {
				at = v.LastSeen
			}
		}
		if page.NextCursor == "" {
			break
		}
		p.Cursor = page.NextCursor
	}
	if len(values) == 0 {
		return nil
	}
	raw, e := json.Marshal(map[string]any{"backfilled_effective_values": values})
	if e != nil {
		return e
	}
	return a.Inventory.ObserveStatus(ctx, enrollment.Device.SerialNumber, enrollment.ID, ddm.StatusUpdate{FullReport: true, Values: values, ReceivedAt: at, Raw: raw})
}
