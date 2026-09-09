package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/pki/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/pki/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

func (e *Environment) attestation() (*attesttest.CA, error) {
	b, err := os.ReadFile(e.Workspace.path("fixtures", "attestation.json"))
	if err != nil {
		return nil, wrapError(err)
	}
	ca, err := attesttest.ParsePrivate(b)
	return ca, wrapError(err)
}

func (e *Environment) acmeDevice(
	ctx context.Context,
	faults simulator.ACMEFaults,
) (*simulator.Device, []byte, error) {
	authority, err := e.attestation()
	if err != nil {
		return nil, nil, wrapError(err)
	}
	d := simulator.New(
		"BENCH-"+randomID(),
		simulator.WithClient(e.Client),
		simulator.WithACME(simulator.ACMEOptions{Attestation: authority, Faults: faults}),
	)
	req, _ := json.Marshal(map[string]string{"DeviceID": d.UDID, "Serial": d.SerialNumber})
	raw, _, err := HTTP(
		ctx,
		e.Client,
		e.URL,
		e.Token,
		"POST",
		"/enrollment-profiles",
		bytes.NewReader(req),
	)
	if err != nil {
		return nil, nil, wrapError(err)
	}
	err = d.ApplyProfile(ctx, raw, profile.ParseOptions{})
	return d, raw, wrapError(err)
}

func acmeEnroll(ctx context.Context, e *Environment, _ string) error {
	d, raw, err := e.acmeDevice(ctx, simulator.ACMEFaults{})
	if err != nil {
		return wrapError(err)
	}
	if err = d.Enroll(ctx); err != nil {
		return wrapError(err)
	}
	authority, err := e.attestation()
	if err != nil {
		return wrapError(err)
	}
	replay := simulator.New(
		d.UDID,
		simulator.WithClient(e.Client),
		simulator.WithACME(simulator.ACMEOptions{Attestation: authority}),
	)
	replay.SerialNumber = d.SerialNumber
	if replay.ApplyProfile(ctx, raw, profile.ParseOptions{}) == nil {
		return fmt.Errorf("%w: ACME client identifier replay was accepted", errOperation)
	}
	foreign, err := attesttest.NewCA()
	if err != nil {
		return wrapError(err)
	}
	for _, fault := range []simulator.ACMEFaults{{WrongKey: true}, {StaleFreshness: true}, {NoAttestation: true}, {ForeignCA: foreign}} {
		if _, _, err = e.acmeDevice(ctx, fault); err == nil {
			return fmt.Errorf("%w: invalid managed device attestation was accepted", errOperation)
		}
	}
	return nil
}

func deviceAttestation(ctx context.Context, e *Environment, _ string) error {
	d, _, err := e.acmeDevice(ctx, simulator.ACMEFaults{})
	if err != nil {
		return wrapError(err)
	}
	if err = d.Enroll(ctx); err != nil {
		return wrapError(err)
	}
	authority, err := e.attestation()
	if err != nil {
		return wrapError(err)
	}
	nonce := []byte("bench-attestation-freshness")
	chain, err := d.DevicePropertiesAttestation(nonce)
	if err != nil {
		return wrapError(err)
	}
	parsed, err := attest.ParseChain(chain)
	if err != nil {
		return wrapError(err)
	}
	if err = parsed.Verify(
		attest.VerifyOptions{Anchors: authority.Anchors(), Freshness: nonce},
	); err != nil {
		return wrapError(err)
	}
	if parsed.Properties.UDID != d.UDID {
		return fmt.Errorf("%w: attestation device binding differs", errOperation)
	}
	cached, err := d.DevicePropertiesAttestation([]byte("different"))
	if err != nil {
		return wrapError(err)
	}
	if !bytes.Equal(cached[0], chain[0]) {
		return fmt.Errorf("%w: device attestation cache was not used", errOperation)
	}
	tampered := [][]byte{append([]byte(nil), chain[0]...), chain[1]}
	tampered[0][len(tampered[0])-1] ^= 1
	bad, err := attest.ParseChain(tampered)
	if err == nil &&
		bad.Verify(attest.VerifyOptions{Anchors: authority.Anchors(), Freshness: nonce}) == nil {
		return fmt.Errorf("%w: tampered attestation verified", errOperation)
	}
	return nil
}

func otaEnroll(ctx context.Context, e *Environment, _ string) error {
	d, err := e.factoryDevice()
	if err != nil {
		return wrapError(err)
	}
	identity := d.Identity
	if err = d.OTAEnroll(
		ctx,
		e.URL+"/ota",
		"wrong-challenge",
		identity,
		profile.ParseOptions{},
	); err == nil {
		return fmt.Errorf("%w: OTA invalid challenge accepted", errOperation)
	}
	return wrapError(d.OTAEnroll(ctx, e.URL+"/ota", "bench-ota", identity, profile.ParseOptions{}))
}

func returnEnabled(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	token := []byte("simulated-bootstrap-token")
	if err = d.SetBootstrapToken(ctx, token); err != nil {
		return wrapError(err)
	}
	r, err := d.ReturnToService(ctx)
	if err != nil {
		return wrapError(err)
	}
	if !r.ReturnToService.Enabled || !bytes.Equal(r.ReturnToService.BootstrapToken, token) {
		return fmt.Errorf("%w: enabled return-to-service lost escrowed token", errOperation)
	}
	return nil
}

func authenticateUser(ctx context.Context, u *simulator.User) error {
	b, err := u.Authenticate(ctx, "")
	if err != nil {
		return wrapError(err)
	}
	var c struct{ DigestChallenge string }
	if err = plist.Unmarshal(b, &c); err != nil {
		return wrapError(err)
	}
	response, err := simulator.DigestResponse(
		c.DigestChallenge,
		u.UserID,
		"bench-password",
		"/mdm",
		nil,
	)
	if err != nil {
		return wrapError(err)
	}
	if _, err = u.Authenticate(ctx, response); err != nil {
		return wrapError(err)
	}
	return wrapError(u.TokenUpdate(ctx))
}

func userChannels(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	alice, bob := d.User("alice", "alice", "Alice"), d.User("bob", "bob", "Bob")
	if alice.TokenUpdate(ctx) == nil {
		return fmt.Errorf("%w: unauthenticated user TokenUpdate accepted", errOperation)
	}
	if err = authenticateUser(ctx, alice); err != nil {
		return wrapError(err)
	}
	if err = authenticateUser(ctx, bob); err != nil {
		return wrapError(err)
	}
	path := "/enrollments/user/" + url.PathEscape(d.UDID+":alice")
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		return wrapError(err)
	}
	var result struct{ Queued int }
	if err = e.api(
		ctx,
		"POST",
		path+"/commands?parent="+url.QueryEscape(d.UDID),
		cmd.Raw,
		&result,
	); err != nil {
		return wrapError(err)
	}
	if result.Queued != 1 {
		return fmt.Errorf("%w: user command not queued", errOperation)
	}
	got, err := alice.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 1 {
		return fmt.Errorf("%w: user command not delivered", errOperation)
	}
	got, err = bob.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 0 {
		return fmt.Errorf("%w: user command leaked to another user", errOperation)
	}
	dc, err := mdm.NewCommand(&commands.DeviceConfigured{})
	if err != nil {
		return wrapError(err)
	}
	if err = e.api(
		ctx,
		"POST",
		path+"/commands?parent="+url.QueryEscape(d.UDID),
		dc.Raw,
		&result,
	); err != nil {
		return wrapError(err)
	}
	if result.Queued != 0 {
		return fmt.Errorf("%w: device-only command accepted on user channel", errOperation)
	}
	return wrapError(alice.CheckOut(ctx))
}

func sharedIPad(ctx context.Context, e *Environment, _ string) error {
	d, err := e.device(ctx)
	if err != nil {
		return wrapError(err)
	}
	// Authenticate again with the same identity to report the iPad inventory.
	d.Model, d.ModelName, d.ProductName, d.OSVersion = "iPad", "iPad Pro", "iPad14,1", "18.4"
	if err = d.Enroll(ctx); err != nil {
		return wrapError(err)
	}
	user := d.SharedIPadUser("student1", "Student One")
	if err = user.TokenUpdate(ctx); err != nil {
		return wrapError(err)
	}
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		return wrapError(err)
	}
	var res struct{ Queued int }
	path := "/enrollments/" + mdm.ChannelSharedIPadUser.String() + "/" + url.PathEscape(
		d.UDID+":student1",
	)
	if err = e.api(
		ctx,
		"POST",
		path+"/commands?parent="+url.QueryEscape(d.UDID),
		cmd.Raw,
		&res,
	); err != nil {
		return wrapError(err)
	}
	if res.Queued != 1 {
		return fmt.Errorf("%w: shared iPad user command not queued", errOperation)
	}
	if _, err = enqueue(ctx, e, d, &commands.UserList{}); err != nil {
		return wrapError(err)
	}
	got, err := d.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 1 || got[0].RequestType != "UserList" {
		return fmt.Errorf("%w: shared iPad device routing differs", errOperation)
	}
	got, err = user.Connect(ctx)
	if err != nil {
		return wrapError(err)
	}
	if len(got) != 1 || got[0].RequestType != "ProfileList" {
		return fmt.Errorf("%w: shared iPad user routing differs", errOperation)
	}
	return nil
}
