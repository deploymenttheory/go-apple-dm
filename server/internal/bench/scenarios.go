package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/push/pushtest"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

func (e *Environment) api(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	var err error
	switch v := in.(type) {
	case []byte:
		body = v
	case nil:
	default:
		body, err = json.Marshal(in)
		if err != nil {
			return wrapError(err)
		}
	}
	b, _, err := HTTP(ctx, e.Client, e.URL, e.Token, method, path, bytes.NewReader(body))
	if err != nil {
		return wrapError(err)
	}
	if out != nil && len(b) > 0 {
		return wrapError(json.Unmarshal(b, out))
	}
	return nil
}

func (e *Environment) device(
	ctx context.Context,
	opts ...simulator.Option,
) (*simulator.Device, error) {
	d := simulator.New(
		"BENCH-"+randomID(),
		simulator.WithClient(e.Client),
		simulator.WithDDM(map[string]any{}),
	)
	for _, option := range opts {
		option(d)
	}
	d.SerialNumber = benchSerial
	req, _ := json.Marshal(map[string]string{"DeviceID": d.UDID, "Serial": d.SerialNumber})
	b, _, err := HTTP(
		ctx,
		e.Client,
		e.URL,
		e.Token,
		"POST",
		"/enrollment-profiles",
		bytes.NewReader(req),
	)
	if err != nil {
		return nil, wrapError(err)
	}
	if err = d.ApplyProfile(ctx, b, profile.ParseOptions{}); err != nil {
		return nil, wrapError(err)
	}
	if err = d.Enroll(ctx); err != nil {
		return nil, wrapError(err)
	}
	return d, nil
}
func pathOf(d *simulator.Device) string { return "/enrollments/device/" + url.PathEscape(d.UDID) }
func enrollIdle(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	if err = d.Enroll(ctx); err != nil {
		return wrapError(err)
	}
	got, err := d.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 0 {
		return fmt.Errorf("%w: idle enrollment received commands", errOperation)
	}
	var v struct {
		Enabled        bool
		TokenUpdatedAt time.Time
	}
	if err = e.api(ctx, "GET", pathOf(d), nil, &v); err != nil {
		return wrapError(err)
	}
	if !v.Enabled || v.TokenUpdatedAt.IsZero() {
		return fmt.Errorf("%w: enrollment or TokenUpdate was not persisted", errOperation)
	}
	return nil
}

func enqueue(
	ctx context.Context,
	e *Environment,
	d *simulator.Device,
	payload commands.Command,
) (*mdm.Command, error) {
	cmd, err := mdm.NewCommand(payload)
	if err != nil {
		return nil, wrapError(err)
	}
	var res struct{ Queued int }
	if err = e.api(ctx, "POST", pathOf(d)+"/commands", cmd.Raw, &res); err != nil {
		return nil, wrapError(err)
	}
	if res.Queued != 1 {
		return nil, fmt.Errorf("%w: command not queued", errOperation)
	}
	return cmd, nil
}

func commandsInOrder(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	types := []commands.Command{
		&commands.DeviceInformation{Queries: []string{"OSVersion"}},
		&commands.ProfileList{},
		&commands.SecurityInfo{},
	}
	ids := []string{}
	for _, c := range types {
		cmd, err := enqueue(ctx, e, d, c)
		if err != nil {
			return wrapError(err)
		}
		ids = append(ids, cmd.UUID)
	}
	got, err := d.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != len(ids) {
		return fmt.Errorf("%w: command count differs", errOperation)
	}
	for i, c := range got {
		if c.UUID != ids[i] {
			return fmt.Errorf("%w: command order differs", errOperation)
		}
		var r struct {
			Status   string
			Response []byte
		}
		if err = e.api(
			ctx,
			"GET",
			pathOf(d)+"/commands/"+url.PathEscape(c.UUID)+"/result",
			nil,
			&r,
		); err != nil {
			return wrapError(err)
		}
		if r.Status != "Acknowledged" || len(r.Response) == 0 {
			return fmt.Errorf("%w: acknowledgement response missing", errOperation)
		}
	}
	return nil
}

func notNow(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	count := 0
	d.Responder = func(c *mdm.Command) simulator.Reply {
		count++
		if count == 1 {
			return simulator.Reply{Status: mdm.StatusNotNow}
		}
		return simulator.AcknowledgeAll(c)
	}
	if _, err = enqueue(ctx, e, d, &commands.DeviceInformation{}); err != nil {
		return wrapError(err)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 1 {
		return fmt.Errorf("%w: NotNow initial delivery failed", errOperation)
	}
	got, err = d.Connect(ctx)
	if err != nil || len(got) != 0 {
		return fmt.Errorf("%w: NotNow was retried before its deadline", errOperation)
	}
	timer := time.NewTimer(31 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return wrapError(ctx.Err())
	case <-timer.C:
	}
	got, err = d.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 1 {
		return fmt.Errorf("%w: NotNow command not retried after deadline", errOperation)
	}
	return nil
}

func commandError(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	d.Responder = func(*mdm.Command) simulator.Reply {
		return simulator.Reply{
			Status:     mdm.StatusError,
			ErrorChain: []mdm.ErrorChainItem{{ErrorCode: 12001, ErrorDomain: "Bench"}},
		}
	}
	cmd, err := enqueue(ctx, e, d, &commands.DeviceInformation{})
	if err != nil {
		return wrapError(err)
	}
	if _, err = d.Connect(ctx); err != nil {
		return wrapError(err)
	}
	var r struct {
		Status     string
		ErrorChain []mdm.ErrorChainItem
	}
	if err = e.api(
		ctx,
		"GET",
		pathOf(d)+"/commands/"+url.PathEscape(cmd.UUID)+"/result",
		nil,
		&r,
	); err != nil {
		return wrapError(err)
	}
	if r.Status != "Error" || len(r.ErrorChain) != 1 || r.ErrorChain[0].ErrorCode != 12001 {
		return fmt.Errorf("%w: device ErrorChain not retained", errOperation)
	}
	return nil
}

func reenroll(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return err
	}
	cmd, err := enqueue(ctx, e, d, &commands.DeviceInformation{})
	if err != nil {
		return err
	}
	raw, err := requestEnrollmentProfile(ctx, e, d.UDID, d.SerialNumber, "")
	if err != nil {
		return err
	}
	old := d.Identity
	if err = d.ApplyProfile(ctx, raw, profile.ParseOptions{}); err != nil {
		return wrapError(err)
	}
	if bytes.Equal(old.Cert.Raw, d.Identity.Cert.Raw) {
		return fmt.Errorf("%w: second grant reused identity", errOperation)
	}
	if err = d.Enroll(ctx); err == nil {
		return fmt.Errorf("%w: unauthorized new identity reenrollment accepted", errOperation)
	}
	if err = expectedRejection(err); err != nil {
		return err
	}
	d.Identity = old
	got, err := d.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 1 || got[0].UUID != cmd.UUID {
		return fmt.Errorf("%w: denied reenrollment changed old queue", errOperation)
	}
	return nil
}

func scepPush(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	if _, err = enqueue(ctx, e, d, &commands.DeviceInformation{}); err != nil {
		return wrapError(err)
	}
	var res struct{ Sent bool }
	if err = e.api(ctx, "POST", pathOf(d)+"/push", nil, &res); err != nil {
		return wrapError(err)
	}
	if !res.Sent {
		return fmt.Errorf("%w: APNs did not accept MDM wake", errOperation)
	}
	got, err := d.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 1 {
		return fmt.Errorf("%w: device did not acknowledge queued command", errOperation)
	}
	return nil
}

func invalidToken(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	script := pushtest.Script{Status: 410, Reason: "Unregistered"}
	if e.APNS != nil {
		e.APNS.ScriptToken(d.PushToken, script)
	} else {
		b, _ := json.Marshal(map[string]any{"Token": d.PushToken, "Script": script})
		if _, err = e.Control(ctx, "POST", "/apns/script", bytes.NewReader(b)); err != nil {
			return wrapError(err)
		}
	}
	var res struct{ Outcome string }
	if err = e.api(ctx, "POST", pathOf(d)+"/push", nil, &res); err != nil {
		return wrapError(err)
	}
	if res.Outcome != "invalid-token" {
		return fmt.Errorf("%w: APNs 410 not classified as invalid-token", errOperation)
	}
	return nil
}

func ddmRoundTrip(ctx context.Context, e *Environment, _ string) error {
	return ddmScenario(ctx, e, false, false)
}

func ddmPredicate(ctx context.Context, e *Environment, _ string) error {
	return ddmScenario(ctx, e, true, false)
}

func ddmCheckout(ctx context.Context, e *Environment, _ string) error {
	return ddmScenario(ctx, e, false, true)
}

func splitRoundTrip(ctx context.Context, e *Environment, _ string) error {
	if e.Topology != "split" {
		return fmt.Errorf("%w: use a split workspace", ErrBlocked)
	}
	return ddmScenario(ctx, e, false, false)
}

func ddmScenario(ctx context.Context, e *Environment, predicate, checkout bool) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	ident := "com.example.bench." + randomID()
	activation := ident + ".activation"
	set := ident + ".set"
	cfg := map[string]any{
		"Identifier": ident,
		"Type":       "com.apple.configuration.management.test",
		"Payload":    map[string]any{"Echo": "bench"},
	}
	act := map[string]any{
		"Identifier": activation,
		"Type":       "com.apple.activation.simple",
		"Payload":    map[string]any{"StandardConfigurations": []string{ident}},
	}
	if predicate {
		act["Payload"].(map[string]any)["Predicate"] = "FALSEPREDICATE"
	}
	admin := &Environment{
		Instance:  e.Instance,
		Client:    e.Client,
		Token:     e.Token,
		Workspace: e.Workspace,
	}
	if e.DDMURL != "" {
		admin.URL = e.DDMURL
	}
	for _, v := range []map[string]any{cfg, act} {
		if err = admin.api(ctx, "PUT", "/declarations", v, nil); err != nil {
			return wrapError(err)
		}
		if err = admin.api(
			ctx,
			"PUT",
			"/sets/"+set+"/declarations/"+v["Identifier"].(string),
			nil,
			nil,
		); err != nil {
			return wrapError(err)
		}
	}
	defer func() { _ = admin.api(context.WithoutCancel(ctx), "DELETE", "/declarations/"+activation, nil, nil) }()
	defer func() { _ = admin.api(context.WithoutCancel(ctx), "DELETE", "/declarations/"+ident, nil, nil) }()
	if err = admin.api(ctx, "PUT", pathOf(d)+"/sets/"+set, nil, nil); err != nil {
		return wrapError(err)
	}
	if _, err = d.SyncDDM(ctx); err != nil {
		return wrapError(err)
	}
	if err = d.PostDDMStatus(ctx, true); err != nil {
		return wrapError(err)
	}
	state := d.DDM()
	c := state.Declarations["configuration/"+ident]
	a := state.Declarations["activation/"+activation]
	if c == nil || a == nil {
		return fmt.Errorf("%w: assigned declarations not delivered", errOperation)
	}
	if a.Active == predicate {
		return fmt.Errorf("%w: activation predicate outcome differs: %+v", errOperation, a)
	}
	if err = d.PostDDMStatus(ctx, true); err != nil {
		return wrapError(err)
	}
	var status []json.RawMessage
	if err = admin.api(ctx, "GET", pathOf(d)+"/status", nil, &status); err != nil {
		return wrapError(err)
	}
	if len(status) == 0 {
		return fmt.Errorf("%w: DDM status was not persisted", errOperation)
	}
	if checkout {
		if err = d.CheckOut(ctx); err != nil {
			return wrapError(err)
		}
		if err = d.Enroll(ctx); err == nil {
			return fmt.Errorf("%w: disabled enrollment reactivated", errOperation)
		}
		if err = expectedRejection(err); err != nil {
			return err
		}
		if err = admin.api(ctx, "GET", pathOf(d)+"/status", nil, &status); err != nil {
			return wrapError(err)
		}
		if len(status) != 0 {
			return fmt.Errorf("%w: DDM status survived checkout", errOperation)
		}
	}
	return nil
}

func adminRoutes(ctx context.Context, e *Environment, _ string) error {
	var routes struct {
		Routes []struct{ Pattern, Action string }
	}
	if err := e.api(ctx, "GET", "/routes", nil, &routes); err != nil {
		return wrapError(err)
	}
	if len(routes.Routes) == 0 {
		return fmt.Errorf("%w: server advertises no routes", errOperation)
	}
	for _, r := range routes.Routes {
		if r.Action == "" {
			return fmt.Errorf("%w: route missing authorization action", errOperation)
		}
	}
	_, status, _ := HTTP(ctx, e.Client, e.URL, "invalid", "GET", "/routes", nil)
	if status != 401 {
		return fmt.Errorf("%w: unauthenticated admin access succeeded", errOperation)
	}
	return nil
}

func returnDisabled(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	r, err := d.ReturnToService(ctx)
	if err != nil {
		return wrapError(err)
	}
	if r.ReturnToService.Enabled {
		return fmt.Errorf("%w: default server authorized erasure", errOperation)
	}
	return nil
}
