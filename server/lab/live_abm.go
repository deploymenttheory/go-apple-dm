package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/server/axmassignment"
)

// abmAcceptance selects operator-designated resources. It never contains credentials.
// Mutate is independent of selecting the catalogue's abm theme or all modules.
type abmAcceptance struct {
	Account string `json:"account"`
	Serial  string `json:"serial"`
	Native  string `json:"native_resource_id"`
	Service string `json:"service_id"`
	Mutate  bool   `json:"allow_assignment"`
}

// abmConfig reads only explicit operator-selected acceptance scope from the private workspace.
func abmConfig(s *Session) (abmAcceptance, error) {
	var c abmAcceptance
	if s.Env.Workspace == nil {
		return c, fmt.Errorf("%w: private ABM acceptance configuration required", ErrBlocked)
	}
	b, err := os.ReadFile(s.Env.Workspace.path("abm", "acceptance.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("%w: private abm/acceptance.json required", ErrBlocked)
	}
	if err != nil {
		return c, fmt.Errorf("%w: ABM configuration unreadable", errOperation)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Account == "" || inventory.CanonicalSerial(c.Serial) == "" {
		return c, fmt.Errorf("%w: ABM account and designated serial required", ErrBlocked)
	}
	c.Serial = inventory.CanonicalSerial(c.Serial)
	return c, nil
}

// abmRequest omits response bodies, resource paths and identifiers from public errors.
// Authorization failures remain distinct from unavailable prerequisites.
func abmRequest(ctx context.Context, s *Session, method, path string, input, output any) ([]byte, error) {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return nil, err
		}
	}
	b, status, err := HTTP(ctx, s.Env.Client, s.Env.URL, s.Env.Token, method, path, bytes.NewReader(body))
	if err != nil {
		switch status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, fmt.Errorf("%w: ABM permission denied (HTTP %d)", errOperation, status)
		case http.StatusNotFound:
			return nil, fmt.Errorf("%w: designated ABM prerequisite unavailable", ErrBlocked)
		}
		var remote struct {
			Error string `json:"Error"`
		}
		_ = json.Unmarshal(b, &remote)
		if remote.Error == "permission_denied" || remote.Error == "authentication_failed" {
			return nil, fmt.Errorf("%w: ABM %s", errOperation, remote.Error)
		}
		return nil, fmt.Errorf("%w: ABM request failed (HTTP %d)", errOperation, status)
	}
	if output != nil && json.Unmarshal(b, output) != nil {
		return nil, fmt.Errorf("%w: invalid ABM response", errOperation)
	}
	return b, nil
}

// abmSave retains identifiable run evidence with private directory and file permissions.
func abmSave(s *Session, name string, b []byte) error {
	if s.Evidence == "" {
		return fmt.Errorf("%w: private evidence directory required", ErrBlocked)
	}
	if err := os.MkdirAll(s.Evidence, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(s.Evidence, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- The operator selects the private evidence directory; callers supply fixed names or generated path-safe request IDs.
	if err != nil {
		return err
	}
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(b)
	}
	return errors.Join(err, file.Close())
}

// abmBase addresses the selected account without changing its opaque identifier.
func abmBase(c abmAcceptance) string { return "/axm/accounts/" + url.PathEscape(c.Account) }

// abmDevice enumerates every small page before matching a physical serial.
// Opaque IDs remain Apple resource addresses, never fallback serials.
func abmDevice(ctx context.Context, s *Session, c abmAcceptance) (axm.OrgDevice, error) {
	var found axm.OrgDevice
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for {
		var p axm.Page[axm.OrgDevice]
		if _, err := abmRequest(ctx, s, "GET", abmBase(c)+"/devices?limit=1&cursor="+url.QueryEscape(cursor), nil, &p); err != nil {
			return found, err
		}
		if len(p.Items) > 1 {
			return found, fmt.Errorf("%w: small-page bound ignored", errOperation)
		}
		for _, d := range p.Items {
			if d.ID == "" || seen[d.ID] {
				return found, fmt.Errorf("%w: incomplete ABM enumeration", errOperation)
			}
			seen[d.ID] = true
			if inventory.CanonicalSerial(d.Attributes.SerialNumber) == inventory.CanonicalSerial(c.Serial) {
				if found.ID != "" {
					return found, fmt.Errorf("%w: ambiguous designated serial", errOperation)
				}
				found = d
			}
		}
		next, err := abmNext(p.Links, p.Meta, cursors)
		if err != nil {
			return found, err
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if found.ID == "" {
		return found, fmt.Errorf("%w: populated designated device unexercised", ErrBlocked)
	}
	var detail axm.OrgDevice
	if _, err := abmRequest(ctx, s, "GET", abmBase(c)+"/devices/"+url.PathEscape(found.ID), nil, &detail); err != nil {
		return found, err
	}
	if detail.ID != found.ID || inventory.CanonicalSerial(detail.Attributes.SerialNumber) != inventory.CanonicalSerial(c.Serial) {
		return found, fmt.Errorf("%w: ABM detail identity differs", errOperation)
	}
	return detail, nil
}

// abmNext validates complete pagination without exposing remote URLs or cursors.
func abmNext(links axm.Links, meta axm.Meta, seen map[string]bool) (string, error) {
	next := meta.Paging.NextCursor
	if next == "" && links.Next != "" {
		u, err := url.Parse(links.Next)
		if err != nil {
			return "", fmt.Errorf("%w: malformed ABM continuation", errOperation)
		}
		next = u.Query().Get("cursor")
	}
	if next == "" {
		if links.Next != "" {
			return "", fmt.Errorf("%w: unusable ABM continuation", errOperation)
		}
		return "", nil
	}
	if seen["cursor:"+next] {
		return "", fmt.Errorf("%w: repeated ABM continuation", errOperation)
	}
	seen["cursor:"+next] = true
	return next, nil
}

// abmPause bounds polling by the scenario context.
func abmPause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// liveABMCollection drives the real account SDK and durable synchronization worker.
func liveABMCollection(ctx context.Context, s *Session) error {
	c, err := abmConfig(s)
	if err != nil {
		return err
	}
	d, err := abmDevice(ctx, s, c)
	if err != nil {
		return err
	}
	// Coverage is separately paginated; an empty coverage collection cannot pass.
	coverageIDs := map[string]bool{}
	cursor := ""
	seen := map[string]bool{}
	for {
		var p axm.Page[axm.AppleCareCoverage]
		if _, err = abmRequest(ctx, s, "GET", abmBase(c)+"/devices/"+url.PathEscape(d.ID)+"/coverage?limit=1&cursor="+url.QueryEscape(cursor), nil, &p); err != nil {
			return err
		}
		if len(p.Items) > 1 {
			return fmt.Errorf("%w: coverage page bound ignored", errOperation)
		}
		for _, plan := range p.Items {
			if plan.ID == "" || coverageIDs[plan.ID] {
				return fmt.Errorf("%w: incomplete coverage enumeration", errOperation)
			}
			coverageIDs[plan.ID] = true
		}
		next, err := abmNext(p.Links, p.Meta, seen)
		if err != nil {
			return err
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(coverageIDs) == 0 {
		return fmt.Errorf("%w: populated coverage unexercised", ErrBlocked)
	}
	var job inventory.Job
	if _, err = abmRequest(ctx, s, "POST", abmBase(c)+"/sync?force=true", nil, &job); err != nil {
		return err
	}
	if job.ID == "" {
		return fmt.Errorf("%w: synchronization job missing", errOperation)
	}
	for {
		if _, err = abmRequest(ctx, s, "GET", abmBase(c)+"/jobs/"+url.PathEscape(job.ID), nil, &job); err != nil {
			return err
		}
		if job.State == "success" || job.State == "partial" {
			break
		}
		if job.State != "queued" && job.State != "running" {
			return fmt.Errorf("%w: synchronization did not complete", errOperation)
		}
		if err = abmPause(ctx); err != nil {
			return err
		}
	}
	jobJSON, _ := json.Marshal(job)
	if err = abmSave(s, "synchronization.json", jobJSON); err != nil {
		return err
	}
	// Restrict exports by opaque protocol address within the selected account.
	where, _ := json.Marshal([]inventory.Condition{{Field: "meta.resource_id", Operator: "eq", Value: json.RawMessage(strconv.Quote(d.ID))}})
	query := "?account=" + url.QueryEscape(c.Account) + "&where=" + url.QueryEscape(string(where))
	var page inventory.ResourcePage
	if _, err = abmRequest(ctx, s, "GET", "/inventory/raw/devices"+query, nil, &page); err != nil {
		return err
	}
	retained := map[string]bool{}
	for _, record := range page.Items {
		if record.Source.Kind != "axm.device" && record.Source.Kind != "axm.detail" && record.Source.Kind != "axm.coverage" {
			continue
		}
		if record.Snapshot.Generation != job.Generation {
			return fmt.Errorf("%w: required source was not collected by this synchronization", errOperation)
		}
		var evidence struct {
			Originals [][]byte                `json:"original_json"`
			History   []inventory.Observation `json:"history"`
		}
		b, e := abmRequest(ctx, s, "GET", "/inventory/raw/devices/"+url.PathEscape(record.ID)+"/evidence", nil, &evidence)
		if e != nil {
			return e
		}
		if e = abmSave(s, record.Source.Kind+"-evidence.json", b); e != nil {
			return e
		}
		var exported []struct {
			ID       string `json:"id"`
			Original []byte `json:"original_json"`
		}
		b, e = abmRequest(ctx, s, "GET", "/inventory/raw/exports"+query+"&source="+url.QueryEscape(record.Source.Kind), nil, &exported)
		if e != nil {
			return e
		}
		if e = abmSave(s, record.Source.Kind+"-export.json", b); e != nil {
			return e
		}
		matched := false
		for _, r := range exported {
			if r.ID == record.ID {
				for i, raw := range evidence.Originals {
					if i < len(evidence.History) && evidence.History[i].Generation == job.Generation && len(raw) > 0 && bytes.Equal(raw, r.Original) {
						matched = true
					}
				}
			}
		}
		if !matched {
			return fmt.Errorf("%w: byte-exact source export missing", errOperation)
		}
		if record.Source.Kind == "axm.coverage" {
			var plans []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if json.Unmarshal(record.Snapshot.Raw, &plans) != nil || len(plans) != len(coverageIDs) {
				return fmt.Errorf("%w: collected coverage differs from preflight", errOperation)
			}
			checked := map[string]bool{}
			for _, plan := range plans {
				if !coverageIDs[plan.ID] || checked[plan.ID] || plan.Type != axm.TypeAppleCareCoverage {
					return fmt.Errorf("%w: collected coverage differs from preflight", errOperation)
				}
				checked[plan.ID] = true
			}
		}
		retained[record.Source.Kind] = true
	}
	if !retained["axm.device"] || !retained["axm.detail"] || !retained["axm.coverage"] {
		return fmt.Errorf("%w: synchronized designated source evidence missing", errOperation)
	}
	assertions, _ := json.Marshal(map[string]any{"required_sources": "passed", "exercised": retained, "generation": job.Generation, "synchronization": job.State})
	if err = abmSave(s, "source-assertions.json", assertions); err != nil {
		return err
	}
	if job.State == "partial" {
		return fmt.Errorf("%w: required populated sources verified; whole-account synchronization remains partial", ErrBlocked)
	}
	return nil
}

// liveABMCorrelation requests an explicit server-validated link between independent serial evidence.
func liveABMCorrelation(ctx context.Context, s *Session) error {
	c, err := abmConfig(s)
	if err != nil {
		return err
	}
	if c.Native == "" {
		return fmt.Errorf("%w: authenticated native resource required; User Enrollment is ineligible", ErrBlocked)
	}
	var inspection struct {
		Resources []struct {
			Record inventory.ResourceRecord `json:"record"`
		} `json:"resources"`
	}
	if _, err = abmRequest(ctx, s, "GET", "/inventory/raw/serials/"+url.PathEscape(c.Serial), nil, &inspection); err != nil {
		return err
	}
	apple := ""
	native := false
	for _, r := range inspection.Resources {
		if r.Record.Source.Kind == "axm.device" && r.Record.Source.AccountID == c.Account {
			if apple != "" {
				return fmt.Errorf("%w: ambiguous Apple evidence", errOperation)
			}
			apple = r.Record.ID
		}
		if r.Record.ID == c.Native {
			native = true
		}
	}
	if apple == "" || !native {
		return fmt.Errorf("%w: matching serial evidence unexercised", ErrBlocked)
	}
	var link inventory.SerialLink
	b, err := abmRequest(ctx, s, "POST", "/inventory/correlations", map[string]string{"apple": apple, "native": c.Native}, &link)
	if err != nil {
		return err
	}
	if inventory.CanonicalSerial(link.Serial) != inventory.CanonicalSerial(c.Serial) || link.Apple.AccountID != c.Account {
		return fmt.Errorf("%w: explicit serial correlation differs", errOperation)
	}
	return abmSave(s, "correlation.json", b)
}

// abmOperation requires all retained remote facts and independently verifies CSV
// and current linkage. A queued HTTP response alone never establishes success.
func abmOperation(ctx context.Context, s *Session, c abmAcceptance, d axm.OrgDevice, id, want string) error {
	for {
		var w axmassignment.Workflow
		b, err := abmRequest(ctx, s, "GET", abmBase(c)+"/assignments/"+id, nil, &w)
		if err != nil {
			return err
		}
		if err = abmSave(s, id+"-workflow.json", b); err != nil {
			return err
		}
		if w.State == "uncertain" || w.State == "fenced" {
			return fmt.Errorf("%w: assignment unresolved; inspect private workflow before recovery", errOperation)
		}
		if w.State == "complete" {
			if w.ID != id || w.Binding.AccountID != c.Account || w.ActivityID == "" || w.AcceptedAt == nil || w.TerminalStatus != "COMPLETED" || !w.LogComplete || !w.LogSucceeded || len(w.Targets) != 1 {
				return fmt.Errorf("%w: incomplete assignment evidence", errOperation)
			}
			t := w.Targets[0]
			if t.Serial != c.Serial || t.DeviceID != d.ID || t.Outcome != w.Binding.Columns.Success || !t.Converged || t.ObservedAt == nil || t.ObservedService != want {
				return fmt.Errorf("%w: designated assignment outcome differs", errOperation)
			}
			csv, err := abmRequest(ctx, s, "GET", abmBase(c)+"/assignments/"+id+"/log", nil, nil)
			if err != nil {
				return err
			}
			if err = abmSave(s, id+"-activity.csv", csv); err != nil {
				return err
			}
			log, err := axm.VerifyActivityLog(bytes.NewReader(csv), w.Binding.Columns, []string{c.Serial})
			if err != nil || !log.Complete || !log.Succeeded {
				return fmt.Errorf("%w: per-device activity log failed", errOperation)
			}
			observed, err := abmAssigned(ctx, s, c, d.ID)
			if err != nil {
				return err
			}
			if observed != want {
				return fmt.Errorf("%w: assignment readback differs", errOperation)
			}
			return nil
		}
		if w.State != "queued" && w.State != "submitting" && w.State != "accepted" && w.State != "observing" && w.State != "polling" && w.State != "terminal" {
			return fmt.Errorf("%w: assignment did not complete", errOperation)
		}
		if err = abmPause(ctx); err != nil {
			return err
		}
	}
}

// abmAssigned reads the current Apple linkage independently of retained workflow observations.
func abmAssigned(ctx context.Context, s *Session, c abmAcceptance, id string) (string, error) {
	var link *axm.Linkage
	_, err := abmRequest(ctx, s, "GET", abmBase(c)+"/devices/"+url.PathEscape(id)+"/assigned-service", nil, &link)
	if link == nil {
		return "", err
	}
	return link.ID, err
}

// abmSubmit persists explicit serial intent through the production assignment API.
func abmSubmit(ctx context.Context, s *Session, c abmAcceptance, id, operation string) error {
	_, err := abmRequest(ctx, s, "POST", abmBase(c)+"/"+operation, map[string]any{"request_id": id, "serials": []string{c.Serial}}, nil)
	return err
}

// liveABMAssignment changes only an explicit lab target, records its original
// assignment before submission, and restores through the same durable workflow.
// Uncertain remote outcomes are retained for read-only reconciliation; restoration
// never circumvents the controller's overlapping-submission protection.
func liveABMAssignment(ctx context.Context, s *Session) (result error) {
	c, err := abmConfig(s)
	if err != nil {
		return err
	}
	if !c.Mutate || c.Service == "" {
		return fmt.Errorf("%w: explicit designated assignment opt-in required", ErrBlocked)
	}
	id := inventory.ID()
	if s.Evidence == "" {
		return fmt.Errorf("%w: private evidence directory required", ErrBlocked)
	}
	if err = os.MkdirAll(s.Evidence, 0o700); err != nil {
		return err
	}
	scoped := *s
	scoped.Evidence = filepath.Join(s.Evidence, "assignment-"+id)
	if err = os.Mkdir(scoped.Evidence, 0o700); err != nil {
		return err
	}
	s = &scoped
	d, err := abmDevice(ctx, s, c)
	if err != nil {
		return err
	}
	var binding axmassignment.Binding
	if _, err = abmRequest(ctx, s, "GET", abmBase(c)+"/assignment-service", nil, &binding); err != nil {
		return err
	}
	if binding.ServiceID != c.Service {
		return fmt.Errorf("%w: designated MDM binding differs", ErrBlocked)
	}
	original, err := abmAssigned(ctx, s, c, d.ID)
	if err != nil {
		return err
	}
	if original != "" && original != c.Service {
		return fmt.Errorf("%w: original service is outside this MDM; select another lab device", ErrBlocked)
	}
	operation, want := "assignments", c.Service
	if original == c.Service {
		operation, want = "unassignments", ""
	}
	before, _ := json.Marshal(map[string]string{"account": c.Account, "serial": c.Serial, "org_device_id": d.ID, "original_service": original, "request_id": id})
	if err = abmSave(s, "original-assignment.json", before); err != nil {
		return err
	}
	// Cleanup has its own lifetime even if the run's deadline expired. It still
	// honors durable overlap protection and records failure without hiding it.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		// Wait for the first durable operation to settle before any inverse request.
		var settled axmassignment.Workflow
		var e error
		for {
			_, e = abmRequest(cleanup, s, "GET", abmBase(c)+"/assignments/"+id, nil, &settled)
			if e != nil || settled.State == "complete" {
				break
			}
			if settled.State == "uncertain" || settled.State == "fenced" {
				e = fmt.Errorf("%w: original request unresolved; restoration unverified", errOperation)
				break
			}
			if e = abmPause(cleanup); e != nil {
				break
			}
		}
		current := ""
		if e == nil {
			current, e = abmAssigned(cleanup, s, c, d.ID)
		}
		if e == nil && current != original {
			if current != "" && current != c.Service {
				e = fmt.Errorf("%w: external service change prevents restoration", errOperation)
			} else {
				restore := "assignments"
				if original == "" {
					restore = "unassignments"
				}
				restoreID := inventory.ID()
				// Save the restoration request before starting network I/O.
				b, _ := json.Marshal(map[string]string{"request_id": restoreID, "original_service": original})
				e = abmSave(s, "restoration-intent.json", b)
				if e == nil {
					e = abmSubmit(cleanup, s, c, restoreID, restore)
				}
				if e == nil {
					e = abmOperation(cleanup, s, c, d, restoreID, original)
				}
			}
		}
		// Even matching readback cannot prove an outstanding mutation is settled.
		if e == nil {
			var w axmassignment.Workflow
			_, e = abmRequest(cleanup, s, "GET", abmBase(c)+"/assignments/"+id, nil, &w)
			if e == nil && w.State != "complete" {
				e = fmt.Errorf("%w: original request unresolved; restoration unverified", errOperation)
			}
		}
		status := "restored"
		if e != nil {
			status = "pending_manual_reconciliation"
		}
		b, _ := json.Marshal(map[string]string{"status": status})
		saveErr := abmSave(s, "cleanup.json", b)
		result = errors.Join(result, e, saveErr)
	}()
	if err = abmSubmit(ctx, s, c, id, operation); err != nil {
		return err
	}
	return abmOperation(ctx, s, c, d, id, want)
}
