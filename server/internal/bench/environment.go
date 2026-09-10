package bench

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/push/pushtest"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/webauth/webauthtest"
	"github.com/deploymenttheory/go-apple-dm/pki/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-dm/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	serverruntime "github.com/deploymenttheory/go-apple-dm/server/internal/runtime"
	"github.com/deploymenttheory/go-apple-dm/simulator"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

// Instance describes the running workspace. The control credential stays private.
//
//nolint:tagliatelle // Private bench documents use the same PascalCase convention as admin responses.
type Instance struct {
	Binary       string `json:"Binary"`
	URL          string `json:"URL"`
	DDMURL       string `json:"DDMURL"`
	ControlURL   string `json:"ControlURL"`
	ControlToken string `json:"ControlToken"`
	Mode         string `json:"Mode"`
	Topology     string `json:"Topology"`
}

// Environment owns fixture services and the ordinary server runtime(s).
type Environment struct {
	DEP       *deptest.Server
	ABM       *axmtest.Server
	Provider  *webauthtest.Provider
	Authority *testpki.CA
	Instance
	Client    *http.Client
	Token     string
	APNS      *pushtest.Server
	Workspace *Workspace
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	errs      chan error
}

func randomID() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func address(ctx context.Context, listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", wrapError(err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", fmt.Errorf("%w: bench listener must be loopback", errOperation)
	}
	if port != "0" {
		return listen, nil
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return "", wrapError(err)
	}
	addr := l.Addr().String()
	err = l.Close()
	return addr, wrapError(err)
}

// Start uses the same configuration and runtime in both adapters. A nonempty
// binary launches dmserver; an empty binary embeds runtime.Serve for E2E tests.
func Start(ctx context.Context, w *Workspace, binary string, out io.Writer) (*Environment, error) {
	addr, err := address(ctx, w.Listen)
	if err != nil {
		return nil, wrapError(err)
	}
	client, err := w.client()
	if err != nil {
		return nil, wrapError(err)
	}
	token, err := w.token()
	if err != nil {
		return nil, wrapError(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	e := &Environment{
		Instance: Instance{
			Binary:   binary,
			URL:      "https://" + addr,
			Mode:     w.Mode,
			Topology: w.Topology,
		},
		Client:    client,
		Token:     token,
		Workspace: w,
		cancel:    cancel,
		errs:      make(chan error, 2),
	}
	good := false
	defer func() {
		if !good {
			e.Close()
		}
	}()
	env := map[string]string{
		"DM_LISTEN":        addr,
		"DM_STORAGE":       w.Storage,
		"DM_DSN":           w.path("mdm", "bench-"+w.Mode+".sqlite"),
		"DM_ROLE":          "all",
		"DM_TLS_CERT_FILE": w.path("mdm", "tls.pem"),
		"DM_TLS_KEY_FILE":  w.path("mdm", "tls.key"),
		"DM_CA_FILE": w.path(
			"mdm",
			"ca.pem",
		),
		"DM_ENROLL_CA_CERT_FILE":    w.path("mdm", "ca.pem"),
		"DM_ENROLL_TLS_ANCHOR_FILE": w.path("mdm", "ca.pem"),
		"DM_ENROLL_CA_KEY_FILE":     w.path("mdm", "ca.key"),
		"DM_ADMIN_TOKEN":            token,
		"DM_STORAGE_KEYS":           "bench,lab",
		"DM_PUBLIC_URL":             e.URL,
		"DM_PUSH_SOURCE":            "store",
		"DM_PUSH_COALESCE":          "-1s",
		"DM_AUDIT_STORE":            "true",
		"DM_DISCOVERY":              "Mac=mdm-adde,iPhone=mdm-byod",
		"DM_ADMIN_STORE":            "true",
	}
	for k, v := range w.Settings {
		env[k] = v
	}
	if w.Mode == "live" {
		env["DM_DSN"] = w.path("mdm", "mdm.sqlite")
	}
	if w.DSN != "" {
		env["DM_DSN"] = w.DSN
	}
	if (w.Storage == "postgres" || w.Storage == "mysql") && w.DSN == "" {
		return nil, fmt.Errorf("%w: SQL workspace requires DSN in private bench.json", errOperation)
	}
	for file, key := range map[string]string{"storage-key": "DM_STORAGE_KEY_BENCH", "scep-challenge": "DM_SCEP_CHALLENGE"} {
		b, err := os.ReadFile(w.path("mdm", file))
		if err != nil {
			return nil, wrapError(err)
		}
		env[key] = strings.TrimSpace(string(b))
	}
	env["DM_STORAGE_KEY_LAB"] = env["DM_STORAGE_KEY_BENCH"]
	configureACMEIdentifierKey(env)
	var cert, key []byte
	if w.Mode == "simulated" {
		cert, key, err = e.providerFixtures(env)
		if err != nil {
			return nil, err
		}
		for _, setup := range []func(map[string]string) error{e.oidcFixture, e.identityFixtures, e.depFixture, e.abmFixture, e.admissionFixture} {
			if err = setup(env); err != nil {
				return nil, err
			}
		}

	} else {
		cert, err = os.ReadFile(w.path("mdm", "push.pem"))
		if errors.Is(err, os.ErrNotExist) {
			env["DM_PUSH_SOURCE"] = "off"
			env["DM_PUSH_TOPIC"] = ""
		} else {
			if err != nil {
				return nil, wrapError(err)
			}
			info, err := pushcert.Inspect(cert)
			if err != nil || !info.MDM {
				return nil, fmt.Errorf(
					"%w: live MDM requires an MDM push certificate",
					errOperation,
				)
			}
			env["DM_PUSH_TOPIC"] = info.Topic
		}

	}
	if w.Topology == "split" {
		if w.Storage == "inmem" {
			return nil, fmt.Errorf(
				"%w: split topology requires shared persistent storage",
				errOperation,
			)
		}
		ddmAddr, err := address(ctx, "127.0.0.1:0")
		if err != nil {
			return nil, wrapError(err)
		}
		e.DDMURL = "https://" + ddmAddr
		// Both roles use the workspace TLS identity and trust anchor.
		ddmEnv := map[string]string{}
		for k, v := range env {
			ddmEnv[k] = v
		}
		ddmEnv["DM_ROLE"] = "ddm"
		ddmEnv["DM_LISTEN"] = ddmAddr
		send, recv := randomID(), randomID()
		ddmEnv["DM_DDM_RECV_KEY"] = send
		ddmEnv["DM_DDM_SEND_KEY"] = recv
		if err = e.launch(ctx, ddmEnv, binary, out); err != nil {
			return nil, wrapError(err)
		}
		if err = e.ready(ctx, e.DDMURL); err != nil {
			return nil, wrapError(err)
		}
		env["DM_ROLE"] = "mdm"
		env["DM_DDM_URL"] = e.DDMURL + app.PathDDM
		env["DM_DDM_ROOT_CA_FILE"] = w.path("mdm", "ca.pem")
		env["DM_DDM_SEND_KEY"] = send
		env["DM_DDM_RECV_KEY"] = recv
	}
	if err = e.launch(ctx, env, binary, out); err != nil {
		return nil, wrapError(err)
	}
	if err = e.ready(ctx, e.URL); err != nil {
		return nil, wrapError(err)
	}
	if err = e.seed(ctx, env["DM_PUSH_TOPIC"], cert, key); err != nil {
		return nil, err
	}
	good = true
	return e, nil
}

func (e *Environment) upload(ctx context.Context, path, topic string, cert, key []byte) error {
	b, err := json.Marshal(
		map[string]string{"Topic": topic, "CertPEM": string(cert), "KeyPEM": string(key)},
	)
	if err != nil {
		return wrapError(err)
	}
	_, _, err = HTTP(ctx, e.Client, e.URL, e.Token, "PUT", path, bytes.NewReader(b))
	return wrapError(err)
}

func (e *Environment) launch(
	ctx context.Context,
	env map[string]string,
	binary string,
	out io.Writer,
) error {
	if binary == "" {
		cfg, err := app.ParseEnv(func(k string) string { return env[k] })
		if err != nil {
			return wrapError(err)
		}
		cfg.Logger = slog.New(slog.NewJSONHandler(out, nil))
		cfg.Secrets = secrets.Static{
			"bench": []byte(env["DM_STORAGE_KEY_BENCH"]),
			"lab":   []byte(env["DM_STORAGE_KEY_LAB"]),
		}
		e.wg.Add(1)
		go func() { defer e.wg.Done(); e.errs <- serverruntime.Serve(ctx, cfg) }()
		return nil
	}
	cmd := exec.CommandContext(ctx, binary)
	// Do not inherit DM_* settings into an isolated bench.
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "DM_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 12 * time.Second
	if err := cmd.Start(); err != nil {
		return wrapError(err)
	}
	e.wg.Add(1)
	go func() { defer e.wg.Done(); e.errs <- cmd.Wait() }()
	return nil
}

func (e *Environment) ready(ctx context.Context, base string) error {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return wrapError(ctx.Err())
		case err := <-e.errs:
			if err == nil {
				err = fmt.Errorf("%w: server exited before readiness", errOperation)
			}
			return wrapError(err)
		case <-deadline.C:
			return fmt.Errorf("%w: server readiness timed out", errOperation)
		case <-tick.C:
			req, err := http.NewRequestWithContext(ctx, "GET", base+"/readyz", nil)
			if err != nil {
				return wrapError(err)
			}
			resp, err := e.Client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					return nil
				}
			}
		}
	}
}

// Close stops all runtimes before releasing fixture services.
func (e *Environment) Close() {
	e.cancel()
	e.wg.Wait()
	if e.APNS != nil {
		e.APNS.Close()
	}
	if e.DEP != nil {
		e.DEP.Close()
	}
	if e.ABM != nil {
		e.ABM.Close()
	}
	if e.Provider != nil {
		e.Provider.Server.Close()
	}
	e.Client.CloseIdleConnections()
}

// Up supervises processes in the foreground. A separate dmctl bench down uses
// the authenticated loopback control endpoint, never a possibly recycled PID.
func Up(ctx context.Context, w *Workspace, binary string, out io.Writer) error {
	lock, err := os.OpenFile(w.path("bench.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return wrapError(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("%w: workspace already running", errOperation)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e, err := Start(ctx, w, binary, out)
	if err != nil {
		return wrapError(err)
	}
	defer e.Close()
	control, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return wrapError(err)
	}
	defer control.Close()
	e.ControlURL = "http://" + control.Addr().String()
	e.ControlToken = randomID()
	mux := http.NewServeMux()
	mux.HandleFunc(
		"POST /stop",
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204); cancel() },
	)
	mux.HandleFunc(
		"GET /status",
		func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(e.Instance) },
	)
	mux.HandleFunc("POST /apns/script", func(w http.ResponseWriter, r *http.Request) {
		if e.APNS == nil {
			http.Error(w, "live environment", http.StatusConflict)
			return
		}
		var in struct {
			Token  []byte
			Script pushtest.Script
		}
		//nolint:musttag // Private control schema embeds the existing pushtest.Script type.
		err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		e.APNS.ScriptToken(in.Token, in.Script)
		w.WriteHeader(204)
	})
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+e.ControlToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			mux.ServeHTTP(w, r)
		}),
	}
	defer srv.Close()
	go func() { _ = srv.Serve(control) }()
	b, err := json.MarshalIndent(e.Instance, "", "  ")
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(w.path("running.json"), b, 0o600); err != nil {
		return wrapError(err)
	}
	defer os.Remove(w.path("running.json"))
	fmt.Fprintln(out, "Bench ready:", e.URL, "mode="+w.Mode, "topology="+w.Topology)
	select {
	case <-ctx.Done():
		return nil
	case err := <-e.errs:
		if err == nil {
			return fmt.Errorf("%w: server exited unexpectedly", errOperation)
		}
		return wrapError(err)
	}
}

func Attach(w *Workspace) (*Environment, error) {
	b, err := os.ReadFile(w.path("running.json"))
	if err != nil {
		return nil, wrapError(err)
	}
	var in Instance
	if err = json.Unmarshal(b, &in); err != nil {
		return nil, wrapError(err)
	}
	c, err := w.client()
	if err != nil {
		return nil, wrapError(err)
	}
	t, err := w.token()
	if err != nil {
		return nil, wrapError(err)
	}
	return &Environment{Instance: in, Client: c, Token: t, Workspace: w}, nil
}

func (e *Environment) Control(
	ctx context.Context,
	method, path string,
	body io.Reader,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.ControlURL+path, body)
	if err != nil {
		return nil, wrapError(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.ControlToken)
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: bench supervisor is unavailable", errOperation)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: control returned HTTP %d", errOperation, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return b, wrapError(err)
}

func (e *Environment) providerFixtures(env map[string]string) (cert, key []byte, err error) {
	w := e.Workspace
	authority, err := testpki.NewCA("bench provider fixture")
	if err != nil {
		return nil, nil, wrapError(err)
	}
	e.Authority = authority
	e.APNS = pushtest.NewServerWithClientCAs(authority.Pool())
	trust := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: e.APNS.Certificate().Raw},
	)
	if err = os.MkdirAll(w.path("fixtures"), 0o700); err != nil {
		return nil, nil, wrapError(err)
	}
	if err = os.WriteFile(w.path("fixtures", "apns-root.pem"), trust, 0o600); err != nil {
		return nil, nil, wrapError(err)
	}
	env["DM_PUSH_HOST"] = e.APNS.URL
	env["DM_PUSH_ROOT_CA_FILE"] = w.path("fixtures", "apns-root.pem")
	env["DM_APP_PUSH_DEVELOPMENT_HOST"] = e.APNS.URL
	env["DM_APP_PUSH_PRODUCTION_HOST"] = e.APNS.URL
	env["DM_APP_PUSH_ROOT_CA_FILE"] = env["DM_PUSH_ROOT_CA_FILE"]
	for _, topic := range []string{"com.apple.mgmt.External.simulator", "com.weaveplatform.deviceweave"} {
		issue := authority.IssuePush
		if topic == "com.weaveplatform.deviceweave" {
			issue = authority.IssueApp
		}
		identity, err := issue(topic, time.Now().Add(-time.Minute))
		if err != nil {
			return nil, nil, wrapError(err)
		}
		c, k, err := identity.PEM()
		if err != nil {
			return nil, nil, wrapError(err)
		}
		if topic == "com.apple.mgmt.External.simulator" {
			cert, key = c, k
		} else {
			if err = os.WriteFile(w.path("fixtures", "app.pem"), c, 0o600); err != nil {
				return nil, nil, wrapError(err)
			}
			if err = os.WriteFile(w.path("fixtures", "app.key"), k, 0o600); err != nil {
				return nil, nil, wrapError(err)
			}
		}
	}
	env["DM_PUSH_TOPIC"] = "com.apple.mgmt.External.simulator"
	return cert, key, nil
}

func (e *Environment) oidcFixture(env map[string]string) error {
	w := e.Workspace
	var err error
	e.Provider, err = webauthtest.Start()
	if err != nil {
		return wrapError(err)
	}
	e.Provider.Set(func(o *webauthtest.Options) {
		o.ClientID = "bench-enroll"
		o.RedirectURI = e.URL + app.PathOIDCCallback
	})
	providerCert := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: e.Provider.Certificate().Raw},
	)
	if err = os.WriteFile(
		w.path("fixtures", "oidc-root.pem"),
		providerCert,
		0o600,
	); err != nil {
		return wrapError(err)
	}
	env["DM_OIDC_ISSUER"] = e.Provider.Issuer()
	env["DM_OIDC_CLIENT_ID"] = "bench-enroll"
	env["DM_OIDC_ROOT_CA_FILE"] = w.path("fixtures", "oidc-root.pem")
	e.Client.Transport.(*http.Transport).TLSClientConfig.RootCAs.AddCert(
		e.Provider.Certificate(),
	)
	return nil
}

func (e *Environment) identityFixtures(env map[string]string) error {
	w := e.Workspace
	var err error
	ac, ak, err := e.Authority.PEM()
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(w.path("fixtures", "device-root.pem"), ac, 0o600); err != nil {
		return wrapError(err)
	}
	env["DM_ADE_ANCHOR_FILE"] = w.path("fixtures", "device-root.pem")
	env["DM_OTA_ANCHOR_FILE"] = env["DM_ADE_ANCHOR_FILE"]
	env["DM_OTA_CHALLENGE"] = "bench-ota"
	users, _ := json.Marshal(
		map[string]string{
			"alice": simulator.HA1("alice", "mdm", "bench-password"),
			"bob":   simulator.HA1("bob", "mdm", "bench-password"),
		},
	)
	if err = os.WriteFile(w.path("fixtures", "user-ha1.json"), users, 0o600); err != nil {
		return wrapError(err)
	}
	env["DM_USER_AUTH_HA1_FILE"] = w.path("fixtures", "user-ha1.json")
	att, err := attesttest.NewCA()
	if err != nil {
		return wrapError(err)
	}
	private, err := att.MarshalPrivate()
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(w.path("fixtures", "attestation.json"), private, 0o600); err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(
		w.path("fixtures", "attestation-root.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: att.Root.Raw}),
		0o600,
	); err != nil {
		return wrapError(err)
	}
	env["DM_ACME_ANCHOR_FILE"] = w.path("fixtures", "attestation-root.pem")
	if env["DM_IDENTITY"] == "acme" {
		delete(env, "DM_OTA_ANCHOR_FILE")
	}

	if err = os.WriteFile(w.path("fixtures", "device-root.key"), ak, 0o600); err != nil {
		return wrapError(err)
	}
	return nil
}

func (e *Environment) depFixture(env map[string]string) error {
	w := e.Workspace
	e.DEP = deptest.NewServer(deptest.Options{})
	env["DM_DEP_BASE_URL"] = e.DEP.URL()
	e.DEP.AddDevices(
		dep.Device{
			SerialNumber: "BENCH-DEP-1",
			Model:        "MacBook Pro",
			DeviceFamily: "Mac",
			OS:           "OSX",
		},
	)
	dt, err := json.Marshal(
		e.DEP.Tokens(),
	) // #nosec G117 -- generated fixture tokens are written only to a private 0600 file
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(w.path("fixtures", "dep-tokens.json"), dt, 0o600); err != nil {
		return wrapError(err)
	}
	return nil
}

func (e *Environment) abmFixture(env map[string]string) error {
	w := e.Workspace
	var err error
	e.ABM = axmtest.NewServer()
	abmKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return wrapError(err)
	}
	e.ABM.RegisterKey("BUSINESSAPI.bench", "bench-key", &abmKey.PublicKey)
	e.ABM.AddMDMServer("bench", nil)
	for _, serial := range []string{"BENCH-ABM-1", "BENCH-ABM-2", "BENCH-ABM-3"} {
		e.ABM.AddOrgDevice(serial, nil)
	}
	e.ABM.AutoAdvance(10 * time.Millisecond)
	abmDER, err := x509.MarshalPKCS8PrivateKey(abmKey)
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(
		w.path("fixtures", "abm.key"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: abmDER}),
		0o600,
	); err != nil {
		return wrapError(err)
	}
	env["DM_AXM_CLIENT_ID"] = "BUSINESSAPI.bench"
	env["DM_AXM_KEY_ID"] = "bench-key"
	env["DM_AXM_KEY_FILE"] = w.path("fixtures", "abm.key")
	env["DM_AXM_BASE_URL"] = e.ABM.URL
	env["DM_AXM_TOKEN_URL"] = e.ABM.TokenURL
	return nil
}

func (e *Environment) seed(ctx context.Context, topic string, cert, key []byte) error {
	w := e.Workspace
	// Seed through ordinary admin routes. Live renewals remain authoritative.
	existing, _, err := HTTP(ctx, e.Client, e.URL, e.Token, "GET", "/pushcerts", nil)
	if err != nil {
		return wrapError(err)
	}
	//nolint:tagliatelle // Administration wire names.
	var listed struct {
		Items []struct {
			Topic string `json:"Topic"`
		} `json:"Items"`
	}
	if err = json.Unmarshal(existing, &listed); err != nil {
		return wrapError(err)
	}
	found := false
	for _, v := range listed.Items {
		if v.Topic == topic {
			found = true
		}
	}
	if topic != "" && (w.Mode == "simulated" || !found) {
		if w.Mode == "live" {
			key, err = os.ReadFile(w.path("mdm", "push.key"))
			if err != nil {
				return wrapError(err)
			}
		}
		if err = e.upload(ctx, "/pushcerts", topic, cert, key); err != nil {
			return wrapError(err)
		}
	}
	if w.Mode == "simulated" {
		c, err := os.ReadFile(w.path("fixtures", "app.pem"))
		if err != nil {
			return wrapError(err)
		}
		k, err := os.ReadFile(w.path("fixtures", "app.key"))
		if err != nil {
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
	}
	return nil
}

func configureACMEIdentifierKey(env map[string]string) {
	if env["DM_ACME_HMAC_KEY"] == "" {
		// A separate derivation keeps issued identifiers usable after a bench restart.
		key := sha256.Sum256([]byte("bench ACME identifiers\x00" + env["DM_STORAGE_KEY_BENCH"]))
		env["DM_ACME_HMAC_KEY"] = hex.EncodeToString(key[:])
	}
}

// Simulated fixtures admit only the fixed synthetic serial and local provider
// subject. Live workspaces require their own explicit admission policy.
func (e *Environment) admissionFixture(env map[string]string) error {
	policy := app.AdmissionPolicy{
		Devices: []app.DeviceAdmissionRule{{Serial: benchSerial}},
		Accounts: []app.AccountAdmissionRule{
			{
				Issuer:              e.Provider.Issuer(),
				Subject:             "user-1",
				ManagedAppleAccount: "user@example.com",
			},
		},
	}
	b, err := json.Marshal(policy)
	if err != nil {
		return wrapError(err)
	}
	file := e.Workspace.path("fixtures", "admission.json")
	if err = os.WriteFile(file, b, 0o600); err != nil {
		return wrapError(err)
	}
	env["DM_ENROLLMENT_POLICY_FILE"] = file
	return nil
}

const benchSerial = "BENCH-APPROVED-SYNTHETIC"
