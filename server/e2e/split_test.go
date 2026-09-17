//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// splitEnv is the pair of containers sharing a database, started by testdb.sh.
type splitEnv struct {
	url, mdmURL, sendKey, recvKey, token string
	client                               *http.Client
	ca                                   *testpki.CA
}

func splitEnvFromOS(t *testing.T) splitEnv {
	t.Helper()
	e := splitEnv{
		url:     os.Getenv("TEST_DDM_URL"),
		mdmURL:  os.Getenv("TEST_MDM_URL"),
		sendKey: os.Getenv("TEST_DDM_SEND_KEY"),
		recvKey: os.Getenv("TEST_DDM_RECV_KEY"),
		token:   os.Getenv("TEST_DDM_ADMIN_TOKEN"),
	}
	if e.url == "" && e.mdmURL == "" {
		t.Skip("split containers not configured (make testdb-ddm-up prints their environment)")
	}
	if e.url == "" || e.mdmURL == "" || e.sendKey == "" || e.recvKey == "" || e.token == "" || os.Getenv("TEST_SPLIT_CA_KEY_FILE") == "" {
		t.Fatal("split fixture must include both roles and its test issuer; run testdb.sh ddm-up")
	}
	backend := os.Getenv("E2E_STORE")
	if backend == "" {
		backend = "sqlite"
	}
	if os.Getenv("TEST_SPLIT_BACKEND") != backend {
		t.Fatalf("split backend %q differs from E2E_STORE=%q", os.Getenv("TEST_SPLIT_BACKEND"), backend)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal(err)
	}
	if file := os.Getenv("TEST_DDM_CA_FILE"); file != "" {
		// #nosec G304 G703 -- The test controls this fixture path within its private workspace.
		pem, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			t.Fatal("invalid TEST_DDM_CA_FILE")
		}
	}
	transport := requireType[*http.Transport](t, http.DefaultTransport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	e.client = &http.Client{
		Transport:     transport,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(transport.CloseIdleConnections)
	// Only fixture material is read; the issuer key is never mounted in a server.
	// #nosec G703 -- The operator supplies the disposable fixture CA file.
	certPEM, err := os.ReadFile(os.Getenv("TEST_DDM_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatal("missing fixture CA certificate")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- The operator supplies the private disposable fixture issuer file.
	keyPEM, err := os.ReadFile(os.Getenv("TEST_SPLIT_CA_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("missing fixture issuer key")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	e.ca = new(testpki.CA)
	e.ca.Cert, e.ca.Key = cert, requireType[crypto.Signer](t, key)
	e.refreshEndpoints(t)
	return e
}

// Docker may assign different ephemeral host ports when a container restarts.
func (e *splitEnv) refreshEndpoints(t *testing.T) {
	t.Helper()
	name := os.Getenv("TEST_SPLIT_NAME")
	if !strings.HasPrefix(name, "dm-test-split-") {
		t.Fatal("missing disposable split fixture name")
	}
	for _, role := range []string{"mdm", "ddm"} {
		// #nosec G204 G702 -- The fixture container is passed as argv, without a shell.
		cmd := exec.CommandContext(t.Context(), "docker", "inspect", "--format", `{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}`, name+"-"+role)
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("inspect %s fixture port: %v", role, err)
		}
		port, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 16)
		if err != nil || port == 0 {
			t.Fatalf("invalid fixture port: %q", output)
		}
		base := fmt.Sprintf("https://127.0.0.1:%d", port)
		if role == "mdm" {
			e.mdmURL = base
		} else {
			e.url = base
		}
	}
}

func (e splitEnv) admin(t *testing.T, method, path string, body []byte) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		method,
		e.url+"/admin/v1"+path,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func(body io.Closer) { _ = body.Close() }(res.Body)
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func (e splitEnv) device(t *testing.T, udid string, opts ...simulator.Option) *simulator.Device {
	t.Helper()
	id, err := e.ca.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	dev := simulator.New(udid, simulator.WithURLs(e.mdmURL+"/mdm", e.mdmURL+"/mdm"),
		simulator.WithClient(e.client), simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}),
		simulator.WithDDM(nil))
	for _, option := range opts {
		option(dev)
	}
	if err := dev.Enroll(t.Context()); err != nil {
		t.Fatal(err)
	}
	return dev
}

func (e splitEnv) observeInventory(t *testing.T, dev *simulator.Device, version string, supervised bool) {
	t.Helper()
	udid := dev.UDID
	mdmAdmin := e
	mdmAdmin.url = e.mdmURL
	cmd, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion", "IsSupervised"}})
	if err != nil {
		t.Fatal(err)
	}
	dev.Responder = func(command *mdm.Command) simulator.Reply {
		reply := simulator.AcknowledgeAll(command)
		if command.UUID == cmd.UUID {
			reply.Payload = &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{
				OSVersion: new(version), IsSupervised: new(supervised),
			}}
		}
		return reply
	}
	if code, body := mdmAdmin.admin(t, "POST", "/enrollments/device/"+udid+"/commands", cmd.Raw); code != http.StatusOK || !strings.Contains(string(body), `"Queued":1`) {
		t.Fatalf("enqueue inventory: %d %s", code, body)
	}
	processed, err := dev.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, command := range processed {
		seen = seen || command.UUID == cmd.UUID
	}
	if !seen {
		t.Fatal("tracked inventory command was not delivered")
	}
}

// inventoryChanges proves that the DDM resolver reads the enrollment updated by
// tracked MDM responses, including withholding before inventory and after a
// capability is withdrawn. No test imports or copies inventory between roles.
func (e *splitEnv) inventoryChanges(t *testing.T) {
	t.Helper()
	udid := fmt.Sprintf("UDID-SPLIT-INVENTORY-%d", time.Now().UnixNano())
	dev := e.device(t, udid, func(d *simulator.Device) { d.OSVersion = "" })
	identifier := udid + ".siri"
	decl := fmt.Sprintf(`{"Type":"com.apple.configuration.siri.settings","Identifier":%q,"Payload":{"AllowSiriAI":false}}`, identifier)
	if code, body := e.admin(t, "PUT", "/declarations", []byte(decl)); code != http.StatusOK {
		t.Fatalf("put inventory fixture: %d %s", code, body)
	}
	t.Cleanup(func() { e.admin(t, "DELETE", "/declarations/"+identifier, nil) })
	if code, body := e.admin(t, "PUT", "/sets/"+udid+"/declarations/"+identifier, nil); code != http.StatusOK {
		t.Fatalf("inventory fixture membership: %d %s", code, body)
	}
	if code, body := e.admin(t, "PUT", "/enrollments/device/"+udid+"/sets/"+udid, nil); code != http.StatusOK {
		t.Fatalf("assign inventory fixture: %d %s", code, body)
	}
	assertDelivery := func(t *testing.T, want bool) {
		t.Helper()
		if _, err := dev.SyncDDM(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := dev.DDM().Declarations["configuration/"+identifier] != nil; got != want {
			t.Fatalf("declaration delivered=%v, want %v", got, want)
		}
		_, err := dev.DeclarativeManagement(t.Context(), "declaration/configuration/"+identifier, nil)
		var httpErr *simulator.HTTPError
		if want && err != nil {
			t.Fatal(err)
		}
		if !want && (!errors.As(err, &httpErr) || httpErr.Status != http.StatusNotFound) {
			t.Fatalf("withheld direct fetch: %v", err)
		}
	}
	assertDelivery(t, false)
	for _, tc := range []struct {
		version               string
		supervised, delivered bool
	}{
		{"26.6.2", true, false},
		{"27.0", true, true},
		{"27.0", false, false},
		{"27.0", true, true},
	} {
		t.Run(fmt.Sprintf("%s-supervised-%t", tc.version, tc.supervised), func(t *testing.T) {
			e.observeInventory(t, dev, tc.version, tc.supervised)
			assertDelivery(t, tc.delivered)
		})
	}
	t.Run("RestartPersistence", func(t *testing.T) {
		name := os.Getenv("TEST_SPLIT_NAME")
		if !strings.HasPrefix(name, "dm-test-split-") {
			t.Fatal("missing disposable split fixture name")
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		// The explicit fixture name selects only this test's two containers.
		// #nosec G204 G702 -- Prefixed fixture names are Docker argv entries; no shell is invoked.
		cmd := exec.CommandContext(ctx, "docker", "restart", name+"-mdm", name+"-ddm")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("restart: %s: %v", output, err)
		}
		e.refreshEndpoints(t)
		dev.CheckinURL, dev.ServerURL = e.mdmURL+"/mdm", e.mdmURL+"/mdm"
		for _, base := range []string{e.mdmURL, e.url} {
			for {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := e.client.Do(req)
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						break
					}
				}
				select {
				case <-ctx.Done():
					t.Fatal("split role did not recover after restart")
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		assertDelivery(t, true)
	})
}

// TestE2E_DDMSplitDeployment covers E2E-010 using both shipped roles with the
// same persistent database. The simulator never writes server storage directly.
func TestE2E_DDMSplitDeployment(t *testing.T) {
	ctx := context.Background()
	e := splitEnvFromOS(t)
	udid := fmt.Sprintf("UDID-SPLIT-%d", time.Now().UnixNano())
	dev := e.device(t, udid)
	e.observeInventory(t, dev, "26.0", true)
	decl := fmt.Sprintf(
		`{"Type":"com.apple.management.properties","Identifier":"%s.props","Payload":{"shard":3}}`,
		udid,
	)
	if code, body := e.admin(t, "PUT", "/declarations", []byte(decl)); code != http.StatusOK {
		t.Fatalf("put declaration: %d %s", code, body)
	}
	t.Cleanup(func() { e.admin(t, "DELETE", "/declarations/"+udid+".props", nil) })
	if code, body := e.admin(
		t,
		"PUT",
		"/sets/"+udid+"/declarations/"+udid+".props",
		nil,
	); code != http.StatusOK {
		t.Fatalf("add to set: %d %s", code, body)
	}
	if code, body := e.admin(
		t,
		"PUT",
		"/enrollments/device/"+udid+"/sets/"+udid,
		nil,
	); code != http.StatusOK {
		t.Fatalf("assign: %d %s", code, body)
	}

	if code, body := e.admin(t, "GET", "/enrollments/device/"+udid, nil); code != http.StatusOK || !strings.Contains(string(body), "26.0") {
		t.Fatalf("DDM role cannot read MDM enrollment: %d %s", code, body)
	}
	sync, err := dev.SyncDDM(ctx)
	if err != nil {
		t.Fatalf("sync through the hop: %v", err)
	}
	// Inventory polling can consume a queued DDM notification before this
	// explicit sync. Assert the complete client state, not the latest delta.
	declarations := dev.DDM().Declarations
	if declarations["management/"+udid+".props"] == nil ||
		declarations["configuration/"+ddm.SubscriptionIdentifier] == nil ||
		declarations["activation/"+ddm.SubscriptionActivationIdentifier] == nil ||
		len(declarations) != 3 || len(sync.Token) != 64 {
		t.Fatalf("sync = %+v; declarations = %+v", sync, declarations)
	}
	if got := dev.DDM().Declarations["management/"+udid+".props"]; got == nil ||
		got.Payload["shard"] != float64(3) {
		t.Fatalf("declaration on device = %+v", got)
	}
	// The served declaration is what the admin API holds, token included.
	code, body := e.admin(t, "GET", "/declarations/"+udid+".props", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"shard":3`) {
		t.Fatalf("admin get: %d %s", code, body)
	}
	// Status posted through the hop lands on the ddm role.
	if err := dev.PostDDMStatus(ctx, true); err != nil {
		t.Fatalf("status: %v", err)
	}
	if st := dev.DDM(); st.Properties["shard"] != float64(3) {
		t.Fatalf("properties after grading = %v", st.Properties)
	}
	code, body = e.admin(t, "GET", "/enrollments/device/"+udid+"/status", nil)
	if code != http.StatusOK || !strings.Contains(string(body), udid+".props") {
		t.Fatalf("status rows: %d %s", code, body)
	}
	// Apple's 404 relays unchanged: the device removes the declaration.
	if code, _ := e.admin(
		t,
		"DELETE",
		"/declarations/"+udid+".props",
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	var herr *simulator.HTTPError
	if _, err := dev.DeclarativeManagement(
		ctx,
		"declaration/management/"+udid+".props",
		nil,
	); !errors.As(err, &herr) ||
		herr.Status != http.StatusNotFound {
		t.Fatalf("after delete: %v, want 404", err)
	}
	sync, err = dev.SyncDDM(ctx)
	if err != nil || len(sync.Removed) != 1 {
		t.Fatalf("sync after delete = %+v, %v", sync, err)
	}
	// A malformed endpoint is Apple's 400, relayed.
	if _, err := dev.DeclarativeManagement(
		ctx,
		"nope/../x",
		nil,
	); !errors.As(err, &herr) ||
		herr.Status != http.StatusBadRequest {
		t.Fatalf("bad endpoint: %v, want 400", err)
	}

	// Exercise malformed hop credentials against the container without creating
	// another enrollment store. The authenticated enrollment already exists.
	for _, name := range []string{"WrongSendKey", "WrongRecvKey"} {
		t.Run(name, func(t *testing.T) {
			send, recv := e.sendKey, e.recvKey
			if name == "WrongSendKey" {
				send = "not-the-key-but-at-least-32-bytes!!"
			} else {
				recv = "not-the-key-but-at-least-32-bytes!!"
			}
			handler, err := proxyclient.Handler(proxyclient.Config{Client: e.client, URL: e.url + "/ddm", SendKey: []byte(send), RecvKey: []byte(recv)})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := plist.Marshal(map[string]any{"MessageType": "DeclarativeManagement", "UDID": udid, "Endpoint": "tokens"})
			if err != nil {
				t.Fatal(err)
			}
			ck, err := mdm.DecodeCheckin(raw)
			if err != nil {
				t.Fatal(err)
			}
			_, err = handler(ctx, nil, ck, requireType[*checkin.DeclarativeManagement](t, ck.Message))
			var serviceErr *service.Error
			if !errors.As(err, &serviceErr) || serviceErr.Code != service.CodeInternal {
				t.Fatalf("bad hop key: %v", err)
			}
		})
	}
	t.Run("InventoryChanges", func(t *testing.T) { e.inventoryChanges(t) })
	t.Run("OversizedBody", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		big := bytes.Repeat([]byte("x"), 2<<20)
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			e.url+"/ddm/v1/declarative-management",
			bytes.NewReader(big),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-apple-aspen-mdm-checkin")
		res, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized = %d, want 413", res.StatusCode)
		}
	})
	t.Run("AdminAuth", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			e.url+"/admin/v1/declarations/x",
			nil,
		)
		res, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("admin without token = %d", res.StatusCode)
		}
	})
}
