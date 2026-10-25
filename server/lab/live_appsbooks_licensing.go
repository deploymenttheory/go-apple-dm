package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

type appsLicensingIntent struct {
	RunID             string                        `json:"run_id"`
	EvidenceDirectory string                        `json:"evidence_directory"`
	Configuration     appsbooks.ClientConfiguration `json:"configuration"`
	ConfigurationRaw  []byte                        `json:"configuration_raw"`
	Location          appsbooks.Location            `json:"location"`
	Scope             appsAcceptance                `json:"scope"`
	Original          []appsbooks.Assignment        `json:"original"`
}

// appsFence is deliberately independent of report destinations. Never remove a
// prior fence merely because current remote state happens to match its snapshot.
func appsFence(s *Session, intent appsLicensingIntent) error {
	b, err := json.Marshal(intent)
	if err != nil {
		return appsError(context.Background(), "intent encoding")
	}
	if err = privateFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"), b); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: prior licensing requires stopped-worker operator reconciliation", ErrBlocked)
		}
		return appsError(context.Background(), "immutable pending fence")
	}
	return nil
}

func appsCheckFence(s *Session) error {
	_, err := os.Lstat(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("%w: prior licensing requires stopped-worker operator reconciliation", ErrBlocked)
	}
	return appsError(context.Background(), "pending fence inspection")
}

func appsRelease(s *Session, id string) error {
	b, err := appsRead(s, "pending-licensing.json")
	if err != nil {
		return err
	}
	var intent appsLicensingIntent
	if json.Unmarshal(b, &intent) != nil || intent.RunID != id || intent.EvidenceDirectory != s.Evidence {
		return appsError(context.Background(), "fence ownership")
	}
	if err = os.Remove(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); err != nil {
		return appsError(context.Background(), "fence release")
	}
	return nil
}

func (a *appsAccess) binding(ctx context.Context, s *Session) error {
	var current appsbooks.Location
	if err := appsRequest(ctx, s, "GET", appsBase(a.config), nil, &current); err != nil {
		return err
	}
	if current.ID != a.location.ID || current.Revision != a.location.Revision || current.UID != a.location.UID || current.MDMID != a.location.MDMID || current.ConfigurationPending {
		return appsError(ctx, "location revision fence")
	}
	c, err := a.client.ClientConfig(ctx)
	if err != nil {
		return appsError(ctx, "fresh ownership")
	}
	if c.UID != a.config.UID || c.MDMInfo == nil || c.MDMInfo.ID != a.config.MDMID || !reflect.DeepEqual(c.MDMInfo, a.configuration.MDMInfo) ||
		c.NotificationURL != a.configuration.NotificationURL || c.NotificationAuthToken != a.configuration.NotificationAuthToken || !reflect.DeepEqual(c.SubscribedNotificationTypes, a.configuration.SubscribedNotificationTypes) {
		return appsError(ctx, "current ownership and callback configuration")
	}
	return nil
}

// assignments demands target-specific readback, including associated user state.
// It rejects duplicates and unrelated rows even if the aggregate job succeeded.
func (a *appsAccess) assignments(ctx context.Context, s *Session) ([]appsbooks.Assignment, error) {
	var rows []appsbooks.Assignment
	if err := a.pages(ctx, s, "assignments", &rows); err != nil {
		return nil, err
	}
	if len(rows) > 1 {
		return nil, appsError(ctx, "duplicate assignment readback")
	}
	for _, row := range rows {
		if row.Asset != a.config.Asset || row.SerialNumber != a.config.Serial || row.ClientUserID != a.config.User || a.config.User != "" && row.UserStatus != "Associated" {
			return nil, appsError(ctx, "per-target assignment readback")
		}
	}
	return rows, nil
}

// appsPhase keeps every readback immutable rather than truncating prior evidence.
func appsPhase(s *Session, name string) (*Session, error) {
	copy := *s
	copy.Evidence = filepath.Join(s.Evidence, name)
	if err := os.Mkdir(copy.Evidence, 0o700); err != nil {
		return nil, appsError(context.Background(), "phase evidence")
	}
	return &copy, nil
}

func (a *appsAccess) readback(ctx context.Context, s *Session, phase string, want bool) error {
	p, err := appsPhase(s, phase)
	if err != nil {
		return err
	}
	rows, err := a.assignments(ctx, p)
	if err != nil {
		return err
	}
	if (len(rows) == 1) != want {
		return appsError(ctx, "assignment state")
	}
	return nil
}

// appsPause is cancellation-aware; the SDK separately enforces Apple's rate.
func appsPause(ctx context.Context) error {
	t := time.NewTimer(time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (a *appsAccess) sdkJob(ctx context.Context, s *Session, phase string, associate bool) error {
	if err := a.binding(ctx, s); err != nil {
		return err
	}
	r := appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{a.config.Asset}}
	if a.config.User != "" {
		r.ClientUserIDs = []string{a.config.User}
	} else {
		r.SerialNumbers = []string{a.config.Serial}
	}
	var event appsbooks.Event
	var err error
	if associate {
		event, err = a.client.Associate(ctx, r)
	} else {
		event, err = a.client.Disassociate(ctx, r)
	}
	if err != nil {
		return appsError(ctx, "SDK licensing submission; outcome requires reconciliation")
	}
	if err = appsSave(s, phase+"-event.json", json.RawMessage(event.Raw)); err != nil {
		return err
	}
	for {
		status, err := a.client.EventStatus(ctx, event.EventID)
		if err != nil {
			return appsError(ctx, "SDK licensing status; outcome requires reconciliation")
		}
		if status.EventStatus == "COMPLETE" {
			if err = appsSave(s, phase+"-status.json", json.RawMessage(status.Raw)); err != nil {
				return err
			}
			expected := "ASSOCIATE"
			if !associate {
				expected = "DISASSOCIATE"
			}
			if !status.Successful() || status.NumRequested != 1 || status.EventType != expected {
				return appsError(ctx, "partial or failed SDK licensing")
			}
			return nil
		}
		if status.EventStatus != "PENDING" {
			return appsError(ctx, "unknown SDK licensing outcome")
		}
		if err = appsPause(ctx); err != nil {
			return appsError(ctx, "SDK licensing wait")
		}
	}
}

// referencePrepare proves mutation and raw-evidence grants before Apple writes.
// CreateJob is local intent only; the reference SDK cannot submit it implicitly.
func (a *appsAccess) referencePrepare(ctx context.Context, s *Session, id string) error {
	if err := a.binding(ctx, s); err != nil {
		return err
	}
	r := appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{a.config.Asset}}
	if a.config.User != "" {
		r.ClientUserIDs = []string{a.config.User}
	} else {
		r.SerialNumbers = []string{a.config.Serial}
	}
	base := appsBase(a.config) + "/jobs/" + id
	var job appsbooks.LicensingJob
	request := appsbooks.JobRequest{Operation: appsbooks.JobAssociate, Assets: &r, MaxPages: a.config.MaxPages}
	if err := appsRequest(ctx, s, "PUT", base, map[string]any{"revision": a.location.Revision, "request": request}, &job); err != nil {
		return err
	}
	if err := appsRequest(ctx, s, "GET", base+"/evidence", nil, &job); err != nil {
		return err
	}
	if job.ID != id || job.State != appsbooks.JobQueued || job.LocationRevision != a.location.Revision || !reflect.DeepEqual(job.Request, request) {
		return appsError(ctx, "reference queued intent")
	}
	return appsSave(s, "reference-intent.json", job)
}

func (a *appsAccess) referenceJob(ctx context.Context, s *Session, id string) error {
	if err := a.binding(ctx, s); err != nil {
		return err
	}
	base := appsBase(a.config) + "/jobs/" + id
	var job appsbooks.LicensingJob
	worker := map[string]any{"worker": "lab-" + id, "leaseSeconds": 30}
	if err := appsRequest(ctx, s, "POST", base+"/submit", worker, &job); err != nil {
		return err
	}
	for {
		if err := appsRequest(ctx, s, "GET", base+"/evidence", nil, &job); err != nil {
			return err
		}
		if job.State == appsbooks.JobSucceeded && job.Status != nil && job.Status.Successful() && job.Status.NumRequested == 1 && !job.NotificationFailure && len(job.NotificationIDs) > 0 && len(job.Targets) == 1 {
			if err := appsSave(s, "reference-job.json", job); err != nil {
				return err
			}
			for _, target := range job.Targets {
				if target.Assignment == nil || target.Result != "SUCCESS" || target.Assignment.Asset != a.config.Asset || target.Assignment.SerialNumber != a.config.Serial || target.Assignment.ClientUserID != a.config.User {
					return appsError(ctx, "per-target callback")
				}
			}
			for number, notificationID := range job.NotificationIDs {
				var receipt appsbooks.NotificationReceipt
				if err := appsRequest(ctx, s, "GET", appsBase(a.config)+"/notifications/"+url.PathEscape(notificationID)+"/evidence", nil, &receipt); err != nil {
					return err
				}
				if receipt.EventID != job.EventID || receipt.AppliedJobID != id || receipt.Notification.UID != a.config.UID || receipt.Notification.ID != notificationID || len(receipt.Raw) == 0 || receipt.ReceivedAt.IsZero() {
					return appsError(ctx, "durable authenticated callback receipt")
				}
				if err := appsSave(s, fmt.Sprintf("reference-callback-%04d.json", number), receipt); err != nil {
					return err
				}
			}
			return nil
		}
		if job.State == appsbooks.JobFailed || job.State == appsbooks.JobUncertain || job.State == appsbooks.JobAbandoned || job.NotificationFailure {
			return appsError(ctx, "reference licensing incomplete or uncertain")
		}
		if !job.NextPollAt.IsZero() && !appsNow(s).Before(job.NextPollAt) {
			if err := appsRequest(ctx, s, "POST", base+"/poll", worker, &job); err != nil {
				return err
			}
		}
		if err := appsPause(ctx); err != nil {
			return appsError(ctx, "reference callback/status wait")
		}
	}
}

// liveAppsBooksLicensing changes only one designated free, revocable app for
// one target. A durable uncertainty fence is acquired before the first POST.
// Books, broad target revocation and notification configuration are excluded.
func liveAppsBooksLicensing(ctx context.Context, session *Session) (result error) {
	s, id, err := appsScope(session, "licensing")
	if err != nil {
		return err
	}
	checks := map[string]any{"source_revision": appsRevision(), "mode": s.Env.Mode, "sdk": "unexercised", "reference": "unexercised", "callback": "unexercised", "readback": "unexercised", "cleanup": "unexercised", "installation": "unexercised"}
	defer func() { result = errors.Join(result, appsSave(s, "assertions.json", checks)) }()
	a, err := appsOpen(ctx, s)
	if err != nil {
		return err
	}
	c := a.config
	if !c.Mutate || !c.Quiescent || !c.FreeApp || c.Asset.AdamID == "" || c.Asset.PricingParam != "STDQ" || (c.Serial == "") == (c.User == "") {
		return fmt.Errorf("%w: explicit free-app licensing opt-in, one target and quiescent dedicated location required", ErrBlocked)
	}
	if err = appsCheckFence(s); err != nil {
		return err
	}
	if a.configuration.MDMInfo == nil || a.configuration.MDMInfo.ID != c.MDMID || a.configuration.NotificationURL == "" || a.configuration.NotificationAuthToken == "" {
		return fmt.Errorf("%w: owned location with working configured callback required", ErrBlocked)
	}
	if err = a.binding(ctx, s); err != nil {
		return err
	}
	if err = a.pages(ctx, s, "assets", nil); err != nil {
		return err
	}
	if c.User != "" {
		if err = a.pages(ctx, s, "users", nil); err != nil {
			return err
		}
		associated := 0
		_, err = a.client.WalkUsers(ctx, appsbooks.UsersQuery{ClientUserID: c.User}, c.MaxPages, func(user appsbooks.User) error {
			if user.ClientUserID != c.User || user.Status != "Associated" {
				return appsError(ctx, "associated designated user")
			}
			associated++
			return nil
		})
		if err != nil || associated != 1 {
			return fmt.Errorf("%w: designated associated Apple clientUserId required", ErrBlocked)
		}
	}
	var selected []appsbooks.AssetRecord
	var stock appsbooks.LicenseStock
	_, err = a.client.WalkAssets(ctx, appsbooks.AssetsQuery{AdamID: c.Asset.AdamID, PricingParam: c.Asset.PricingParam}, c.MaxPages, func(inventory appsbooks.AssetInventory) error {
		if inventory.Record != nil {
			selected = append(selected, *inventory.Record)
		}
		if inventory.Unlimited != nil {
			u := inventory.Unlimited
			selected = append(selected, appsbooks.AssetRecord{Asset: u.Asset, ProductType: u.ProductType, Revocable: u.Revocable, DeviceAssignable: u.DeviceAssignable, SupportedPlatforms: u.SupportedPlatforms, Raw: u.Raw})
		}
		stock = inventory.Stock()
		return nil
	})
	if err != nil {
		return appsError(ctx, "designated asset capabilities")
	}
	if len(selected) != 1 || selected[0].Asset != c.Asset || selected[0].ProductType != "App" || !selected[0].Revocable || c.Serial != "" && !selected[0].DeviceAssignable {
		return fmt.Errorf("%w: one available revocable app with applicable licensing capability required", ErrBlocked)
	}
	if err = appsSave(s, "asset.json", json.RawMessage(selected[0].Raw)); err != nil {
		return err
	}
	p, err := appsPhase(s, "original")
	if err != nil {
		return err
	}
	original, err := a.assignments(ctx, p)
	if err != nil {
		return err
	}
	if len(original) == 0 && (stock.Kind == appsbooks.StockUnknown || stock.Kind == appsbooks.StockFinite && stock.Available < 1) {
		return fmt.Errorf("%w: designated app license availability unreported or exhausted", ErrBlocked)
	}
	intent := appsLicensingIntent{RunID: id, EvidenceDirectory: s.Evidence, Configuration: a.configuration, ConfigurationRaw: append([]byte(nil), a.configuration.Raw...), Location: a.location, Scope: c, Original: original}
	if err = appsSave(s, "intent.json", intent); err != nil {
		return err
	}
	if err = a.referencePrepare(ctx, s, id); err != nil {
		return err
	}
	if err = appsFence(s, intent); err != nil {
		return err
	}
	checks["location_revision"] = a.location.Revision
	// Any unsuccessful submission, status, callback or readback retains the fence.
	// Cleanup cannot infer an inverse from a request whose outcome is unknown.
	if err = a.sdkJob(ctx, s, "sdk-associate", true); err != nil {
		return err
	}
	checks["sdk"] = "passed"
	if err = a.readback(ctx, s, "sdk-readback", true); err != nil {
		return err
	}
	if err = a.referenceJob(ctx, s, id); err != nil {
		return err
	}
	checks["reference"], checks["callback"] = "passed", "passed"
	if err = a.readback(ctx, s, "reference-readback", true); err != nil {
		return err
	}
	checks["readback"] = "passed"
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if len(original) == 0 {
		if err = a.sdkJob(cleanup, s, "cleanup-disassociate", false); err != nil {
			return err
		}
	}
	if err = a.readback(cleanup, s, "cleanup-readback", len(original) == 1); err != nil {
		return err
	}
	if err = appsSave(s, "cleanup.json", map[string]any{"restored": true, "preexisting_assignment_preserved": len(original) == 1}); err != nil {
		return err
	}
	if err = appsRelease(s, id); err != nil {
		return err
	}
	checks["cleanup"] = "passed"
	return nil
}
