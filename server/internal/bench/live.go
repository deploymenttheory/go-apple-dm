package bench

import (
	"context"
	"crypto"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func appAlert(
	ctx context.Context,
	e *Environment,
	_ string,
) error {
	return appSend(ctx, e, "alert")
}

func appBackground(ctx context.Context, e *Environment, _ string) error {
	return appSend(ctx, e, "background")
}

func appSend(ctx context.Context, e *Environment, kind string) error {
	//nolint:tagliatelle // Match the host app registration document.
	reg := struct {
		Token       string `json:"token"`
		Topic       string `json:"topic"`
		Environment string `json:"environment"`
	}{
		Token:       "0123456789abcdef",
		Topic:       "com.weaveplatform.deviceweave",
		Environment: "development",
	}
	if e.Mode == "live" {
		// A partial registration must never inherit the simulated device token.
		reg.Token, reg.Topic, reg.Environment = "", "", ""
		b, err := os.ReadFile(e.Workspace.path("app", "registration.json"))
		if err != nil {
			return fmt.Errorf("%w: app registration is missing", ErrBlocked)
		}
		if err = json.Unmarshal(b, &reg); err != nil {
			return wrapError(err)
		}
		if reg.Token == "" || reg.Topic == "" ||
			(reg.Environment != "development" && reg.Environment != "production") {
			return fmt.Errorf("%w: app registration is incomplete", ErrBlocked)
		}
		if err := e.requireAppCredential(ctx, reg.Topic); err != nil {
			return err
		}
	}
	correlation := randomID()
	aps := map[string]any{"content-available": 1}
	if kind == "alert" {
		aps = map[string]any{
			"alert": map[string]string{"title": "DeviceWeave bench", "body": correlation},
		}
	}
	var res struct {
		Accepted bool
		Outcome  string
	}
	if err := e.api(
		ctx,
		"POST",
		"/apppush/send",
		map[string]any{
			"Environment": reg.Environment,
			"Topic":       reg.Topic,
			"Token":       reg.Token,
			"PushType":    kind,
			"Payload":     map[string]any{"aps": aps, "labCorrelationID": correlation},
		},
		&res,
	); err != nil {
		return wrapError(err)
	}
	if !res.Accepted {
		return fmt.Errorf("%w: app push was not accepted", errOperation)
	}
	if e.Mode != "live" {
		return nil
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return wrapError(ctx.Err())
		case <-deadline.C:
			return fmt.Errorf(
				"%w: APNs accepted the push but device receipt remains unverified",
				errOperation,
			)
		case <-tick.C:
			files, err := filepath.Glob(e.Workspace.path("app", "receipt-*.json"))
			if err != nil {
				return wrapError(err)
			}
			for _, file := range files {
				b, err := os.ReadFile(
					file,
				) // #nosec G304 -- Receipt glob is confined to the operator-owned bench workspace.
				if err != nil {
					continue
				}
				//nolint:tagliatelle // labCorrelationID is emitted by the host app.
				var receipt struct {
					Correlation string `json:"labCorrelationID"`
					Topic       string `json:"topic"`
					Environment string `json:"environment"`
				}
				if json.Unmarshal(b, &receipt) == nil && receipt.Correlation == correlation &&
					receipt.Topic == reg.Topic &&
					receipt.Environment == reg.Environment {
					return nil
				}
			}
		}
	}
}

func (e *Environment) requireAppCredential(ctx context.Context, topic string) error {
	path := "/apppush/credentials"
	for {
		var page struct {
			Items      []struct{ Topic string }
			NextCursor string
		}
		if err := e.api(ctx, "GET", path, nil, &page); err != nil {
			return err
		}
		for _, item := range page.Items {
			if item.Topic == topic {
				return nil
			}
		}
		if page.NextCursor == "" {
			return fmt.Errorf("%w: app credential has not been imported", ErrBlocked)
		}
		path = "/apppush/credentials?cursor=" + url.QueryEscape(page.NextCursor)
	}
}

func appRenewal(ctx context.Context, e *Environment, _ string) error {
	pair, err := tls.LoadX509KeyPair(
		e.Workspace.path("fixtures", "device-root.pem"),
		e.Workspace.path("fixtures", "device-root.key"),
	)
	if err != nil {
		return wrapError(err)
	}
	issuer := &testpki.CA{
		Identity: testpki.Identity{Cert: pair.Leaf, Key: pair.PrivateKey.(crypto.Signer)},
	}
	identity, err := issuer.IssueApp("com.weaveplatform.deviceweave", time.Now().Add(-time.Minute))
	if err != nil {
		return wrapError(err)
	}
	c, k, err := identity.PEM()
	if err != nil {
		return wrapError(err)
	}
	var before struct {
		Items []struct {
			Topic   string
			Version int64
		}
	}
	if err = e.api(ctx, "GET", "/apppush/credentials", nil, &before); err != nil {
		return wrapError(err)
	}
	if err = e.upload(
		ctx,
		"/apppush/credentials",
		"com.weaveplatform.deviceweave",
		c,
		k,
	); err != nil {
		return wrapError(err)
	}
	var after struct {
		Items []struct {
			Topic   string
			Version int64
		}
	}
	if err = e.api(ctx, "GET", "/apppush/credentials", nil, &after); err != nil {
		return wrapError(err)
	}
	if len(before.Items) != 1 || len(after.Items) != 1 ||
		after.Items[0].Version != before.Items[0].Version+1 {
		return fmt.Errorf("%w: credential version did not advance", errOperation)
	}
	return appSend(ctx, e, "alert")
}

func liveMDM(ctx context.Context, e *Environment, device string) error {
	_, err := liveInventory(ctx, e, device)
	return err
}

func liveInventory(ctx context.Context, e *Environment, device string) (map[string]string, error) {
	if strings.TrimSpace(device) == "" {
		return nil, fmt.Errorf("%w: -device-id is required", ErrBlocked)
	}
	path := "/enrollments/device/" + url.PathEscape(device)
	var enrolled struct {
		Enabled        bool
		TokenUpdatedAt time.Time
	}
	if err := e.api(ctx, "GET", path, nil, &enrolled); err != nil {
		return nil, fmt.Errorf("%w: enrollment unavailable", ErrBlocked)
	}
	if !enrolled.Enabled || enrolled.TokenUpdatedAt.IsZero() {
		return nil, fmt.Errorf("%w: enrollment has not completed TokenUpdate", ErrBlocked)
	}
	cmd, err := mdm.NewCommand(
		&commands.DeviceInformation{Queries: []string{"OSVersion", "BuildVersion"}},
	)
	if err != nil {
		return nil, wrapError(err)
	}
	response, err := liveCommand(ctx, e, path, cmd)
	if err != nil {
		return nil, err
	}
	var answer struct{ QueryResponses map[string]string }
	if err = plist.Unmarshal(response, &answer); err != nil {
		return nil, wrapError(err)
	}
	if answer.QueryResponses["OSVersion"] == "" || answer.QueryResponses["BuildVersion"] == "" {
		return nil, fmt.Errorf("%w: acknowledgement missing requested inventory", errOperation)
	}
	return answer.QueryResponses, nil
}

// liveCommand requires a wake and a response from the exact enrollment channel
// on which the command was queued.
func liveCommand(ctx context.Context, e *Environment, path string, cmd *mdm.Command) ([]byte, error) {
	var queued struct{ Queued int }
	if err := e.api(ctx, "POST", path+"/commands", cmd.Raw, &queued); err != nil {
		return nil, wrapError(err)
	}
	if queued.Queued != 1 {
		return nil, fmt.Errorf("%w: %s was not queued", errOperation, cmd.RequestType)
	}
	var push struct{ Sent bool }
	if err := e.api(ctx, "POST", path+"/push", nil, &push); err != nil {
		return nil, wrapError(err)
	}
	if !push.Sent {
		return nil, fmt.Errorf("%w: APNs did not accept the wake", errOperation)
	}
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, wrapError(ctx.Err())
		case <-deadline.C:
			return nil, fmt.Errorf("%w: %s acknowledgement timed out", errOperation, cmd.RequestType)
		case <-tick.C:
			b, status, err := HTTP(
				ctx,
				e.Client,
				e.URL,
				e.Token,
				"GET",
				path+"/commands/"+url.PathEscape(cmd.UUID)+"/result",
				nil,
			)
			if err != nil {
				return nil, wrapError(err)
			}
			if status == 204 {
				continue
			}
			if status != 200 {
				return nil, fmt.Errorf("%w: command result HTTP %d", errOperation, status)
			}
			//nolint:tagliatelle // Administration wire names.
			var res struct {
				Status   string `json:"Status"`
				Response []byte `json:"Response"`
			}
			if err = json.Unmarshal(b, &res); err != nil {
				return nil, wrapError(err)
			}
			if res.Status == "Error" {
				return nil, fmt.Errorf("%w: device returned Error", errOperation)
			}
			if res.Status == "Acknowledged" {
				return res.Response, nil
			}
		}
	}
}

// Profile requests the actual configured enrollment service and writes a new file.
func Profile(ctx context.Context, e *Environment, device, destination string) error {
	return ProfileWithIdentity(ctx, e, device, "", destination)
}
