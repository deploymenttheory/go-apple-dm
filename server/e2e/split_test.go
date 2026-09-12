//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// splitEnv is the container started by scripts/testdb.sh ddm-up.
type splitEnv struct {
	url, sendKey, recvKey, token string
	client                       *http.Client
}

func splitEnvFromOS(t *testing.T) splitEnv {
	t.Helper()
	e := splitEnv{
		url:     os.Getenv("TEST_DDM_URL"),
		sendKey: os.Getenv("TEST_DDM_SEND_KEY"),
		recvKey: os.Getenv("TEST_DDM_RECV_KEY"),
		token:   os.Getenv("TEST_DDM_ADMIN_TOKEN"),
	}
	if e.url == "" || e.sendKey == "" || e.recvKey == "" || e.token == "" {
		t.Skip(
			"TEST_DDM_URL, TEST_DDM_SEND_KEY, TEST_DDM_RECV_KEY, and TEST_DDM_ADMIN_TOKEN not set (make testdb-ddm-up prints them)",
		)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal(err)
	}
	if file := os.Getenv("TEST_DDM_CA_FILE"); file != "" {
		pem, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			t.Fatal("invalid TEST_DDM_CA_FILE")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	e.client = &http.Client{
		Transport:     transport,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(transport.CloseIdleConnections)
	return e
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
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// mdmRole runs the mdm role in-process with proxyclient pointed at the
// container.
func mdmRole(t *testing.T, e splitEnv, sendKey, recvKey string) *harness {
	t.Helper()
	dm, err := proxyclient.Handler(
		proxyclient.Config{
			Client:  e.client,
			URL:     e.url + "/ddm",
			SendKey: []byte(sendKey),
			RecvKey: []byte(recvKey),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return newHarness(t, service.Config{DeclarativeManagement: dm})
}

// TestE2E_DDMSplitDeployment covers E2E-010: an in-process mdm role forwards
// declarative check-ins to the project's dmserver container running the ddm
// role.
func TestE2E_DDMSplitDeployment(t *testing.T) {
	ctx := context.Background()
	e := splitEnvFromOS(t)
	udid := fmt.Sprintf("UDID-SPLIT-%d", time.Now().UnixNano())
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

	h := mdmRole(t, e, e.sendKey, e.recvKey)
	dev := h.ddmDevice(udid, map[string]any{})
	sync, err := dev.SyncDDM(ctx)
	if err != nil {
		t.Fatalf("sync through the hop: %v", err)
	}
	// The container's default synthesized subscription adds a declaration to the
	// assigned fixture.
	fetched := map[string]bool{}
	for _, k := range sync.Fetched {
		fetched[k] = true
	}
	if !fetched["management/"+udid+".props"] ||
		!fetched["configuration/"+ddm.SubscriptionIdentifier] ||
		len(sync.Fetched) != 2 ||
		len(sync.Token) != 64 {
		t.Fatalf("sync = %+v", sync)
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

	t.Run("WrongSendKey", func(t *testing.T) {
		bad := mdmRole(t, e, "not-the-key-but-at-least-32-bytes!!", e.recvKey)
		d := bad.ddmDevice(udid+"-A", nil)
		if _, err := d.DeclarativeManagement(
			ctx,
			"tokens",
			nil,
		); !errors.As(err, &herr) ||
			herr.Status != http.StatusInternalServerError {
			t.Fatalf("wrong send key: %v, want 500 (server answered 401)", err)
		}
	})
	t.Run("WrongRecvKey", func(t *testing.T) {
		bad := mdmRole(t, e, e.sendKey, "not-the-key-but-at-least-32-bytes!!")
		d := bad.ddmDevice(udid+"-B", nil)
		if _, err := d.DeclarativeManagement(
			ctx,
			"tokens",
			nil,
		); !errors.As(err, &herr) ||
			herr.Status != http.StatusInternalServerError {
			t.Fatalf("wrong recv key: %v, want 500 (client rejected the response)", err)
		}
	})
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
		res.Body.Close()
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
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("admin without token = %d", res.StatusCode)
		}
	})
}
