package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
)

// liveDDM requires fresh declaration status for a unique subscription. Status
// values are incremental, so retained OS/build values must match fresh inventory.
func liveDDM(ctx context.Context, e *Environment, device string) (err error) {
	inventory, err := liveInventory(ctx, e, device)
	if err != nil {
		return err
	}
	path := "/enrollments/device/" + url.PathEscape(device)
	prefix := "com.go-apple-dm.lab." + randomID()
	configuration, activation, set := prefix+".c", prefix+".a", prefix+".set"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		err = errors.Join(err, cleanupLiveDDM(cleanup, e, path, set, configuration, activation))
	}()
	names := []string{"device.operating-system.version", "device.operating-system.build-version"}
	expected := map[string]string{
		names[0]: inventory["OSVersion"],
		names[1]: inventory["BuildVersion"],
	}
	declarations := []struct {
		Identifier string `json:"Identifier"` // Apple declaration wire keys.
		Type       string `json:"Type"`       // Apple declaration wire keys.
		Payload    any    `json:"Payload"`    // Apple declaration wire keys.
	}{
		{
			configuration,
			schemaddm.DeclarationTypeManagementStatusSubscriptions,
			map[string]any{
				"StatusItems": []map[string]string{{"Name": names[0]}, {"Name": names[1]}},
			},
		},
		{
			activation,
			schemaddm.DeclarationTypeActivationSimple,
			map[string]any{"StandardConfigurations": []string{configuration}},
		},
	}
	started := time.Now().UTC()
	for _, declaration := range declarations {
		if err := e.api(ctx, "PUT", "/declarations", declaration, nil); err != nil {
			return err
		}
		if err := e.api(
			ctx,
			"PUT",
			"/sets/"+set+"/declarations/"+declaration.Identifier,
			nil,
			nil,
		); err != nil {
			return err
		}
	}
	if err := e.api(ctx, "PUT", path+"/sets/"+set, nil, nil); err != nil {
		return err
	}
	if err := e.api(ctx, "POST", "/notify", nil, nil); err != nil {
		return err
	}
	return waitLive(ctx, func() (bool, error) {
		var rows []ddm.DeclarationStatus
		if err := e.api(ctx, "GET", path+"/status", nil, &rows); err != nil {
			return false, err
		}
		active := map[string]bool{}
		for _, row := range rows {
			active[row.Identifier] = row.Active && row.Valid == "valid" &&
				!row.LastSeen.Before(started)
		}
		if !active[configuration] || !active[activation] || !active[ddm.SubscriptionIdentifier] ||
			!active[ddm.SubscriptionActivationIdentifier] {
			return false, nil
		}
		var values struct {
			Items []ddm.StatusValue `json:"Items"` // Existing admin API response key.
		}
		if err := e.api(ctx, "GET", path+"/status/values", nil, &values); err != nil {
			return false, err
		}
		matching := map[string]bool{}
		for _, row := range values.Items {
			var value string
			matching[row.Path] = !row.LastSeen.IsZero() &&
				json.Unmarshal(row.Value, &value) == nil &&
				value != "" &&
				value == expected[row.Path]
		}
		return matching[names[0]] && matching[names[1]], nil
	})
}

// cleanupLiveDDM removes the declarations and assignments created by a live DDM scenario.
func cleanupLiveDDM(
	ctx context.Context,
	e *Environment,
	path, set, configuration, activation string,
) error {
	var failures []error
	for _, target := range []string{path + "/sets/" + set, "/declarations/" + activation, "/declarations/" + configuration} {
		_, status, err := HTTP(ctx, e.Client, e.URL, e.Token, "DELETE", target, nil)
		if err != nil && status != http.StatusNotFound {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w: DDM test cleanup: %w", errOperation, errors.Join(failures...))
	}
	if err := e.api(ctx, "POST", "/notify", nil, nil); err != nil {
		return err
	}
	return waitLive(ctx, func() (bool, error) {
		var rows []ddm.DeclarationStatus
		if err := e.api(ctx, "GET", path+"/status", nil, &rows); err != nil {
			return false, err
		}
		for _, row := range rows {
			if row.Identifier == configuration || row.Identifier == activation {
				return false, nil
			}
		}
		return true, nil
	})
}

// waitLive polls the supplied live-state predicate until success, cancellation, or a
// reported failure.
func waitLive(ctx context.Context, ready func() (bool, error)) error {
	for {
		ok, err := ready()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: device evidence incomplete: %w", errOperation, ctx.Err())
		case <-timer.C:
		}
	}
}
