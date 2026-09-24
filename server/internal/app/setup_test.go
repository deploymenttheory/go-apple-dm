package app

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// managedRolloverApp creates a replacement-security application with observed macOS product and
// version data.
func managedRolloverApp(t *testing.T) (*App, mdm.EnrollmentID) {
	t.Helper()
	a, id := replacementSecurityApp(t)
	e, err := a.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	e.Device = storage.DeviceInfo{ProductName: "Mac15,3", OSVersion: "26.0"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	return a, id
}

// TestManagedIssuerRolloverKeepsOfflineDeviceAndRetiresLegacyRoute checks that managed issuer
// rollover keeps offline device and retires legacy route.
func TestManagedIssuerRolloverKeepsOfflineDeviceAndRetiresLegacyRoute(t *testing.T) {
	ctx := t.Context()
	a, id := managedRolloverApp(t)
	a.cfg.Setup = &SetupConfig{Role: "combined", IssuerID: "enrollment-ca"}
	a.Certificates = &lifecycle.Manager{Store: a.protocol}
	key, err := x509.MarshalPKCS8PrivateKey(a.enroll.caKey)
	if err != nil {
		t.Fatal(err)
	}
	req := lifecycle.Request{
		ID:      "enrollment-ca",
		Kind:    lifecycle.EnrollmentCA,
		Subject: pkix.Name{CommonName: "managed enrollment CA"},
	}
	if _, err = a.Certificates.Adopt(
		ctx,
		req,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.enroll.caCert.Raw}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	); err != nil {
		t.Fatal(err)
	}
	binding := acme.Binding{MDMUDID: id.ID, CommonName: id.ID}
	original, err := a.enroll.profile(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	original.AccessRights = enroll.RightInstallProfiles
	if err = a.enroll.recordProfile(ctx, binding, original); err != nil {
		t.Fatal(err)
	}
	evidence := identityEvidence{
		Issuer:   cms.Fingerprint(a.enroll.caCert),
		Method:   "scep",
		NotAfter: time.Now().Add(365 * 24 * time.Hour),
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.protocol.Update(
		ctx,
		[]string{"issued-identity:original-pin"},
		func(tx state.Tx) error {
			return tx.Put(ctx, state.Record{Key: "issued-identity:original-pin", Value: data})
		},
	); err != nil {
		t.Fatal(err)
	}
	pending, err := a.Certificates.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Certificates.CreateIssuer(
		ctx,
		req.ID,
		pending.Pending,
		20*365*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	job, err := a.startIssuerRollover(ctx, req.ID, pending.Pending)
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.managedIssuer(ctx, job.To)
	if err != nil {
		t.Fatal(err)
	}
	// The ACME completion hook must register under the successor CA too;
	// using the startup registry here would reject every new ACME identity.
	template := &x509.Certificate{
		SerialNumber: big.NewInt(123456),
		Subject:      pkix.Name{CommonName: "new ACME device"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		target.enrollment.caCert,
		a.enroll.caKey.Public(),
		target.enrollment.caKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.acmeRevocations(target.enrollment).
		Register(ctx, cms.Fingerprint(target.enrollment.caCert), leaf, revocation.Provenance{Source: "acme"}); err != nil {
		t.Fatal("successor ACME registration used the original issuer registry", err)
	}
	if target.enrollment.scepPath() != "/scep/issuers/2" ||
		target.enrollment.acmePath() != "/acme/issuers/2" {
		t.Fatal("issuer routes not isolated")
	}
	if err := a.advanceRollover(ctx, job); err != nil {
		t.Fatal(err)
	}
	rows, _, err := a.Certificates.Migrations(ctx, req.ID, job.To, "", 100)
	if err != nil || len(rows) != 1 || rows[0].Phase == "confirmed" ||
		rows[0].Reason != "waiting for device check-in" {
		t.Fatal("offline device lost", rows, err)
	}
	if _, err = a.retireManagedIssuer(
		ctx,
		req.ID,
		job.From,
	); !errors.Is(
		err,
		lifecycle.ErrConflict,
	) {
		t.Fatal("retired issuer with offline device", err)
	}
	// Recreate service caches as after restart; all progress remains in state.
	a.issuerServices = nil
	signer, _, err := a.profileSigningIdentity(a.enroll)(ctx)
	if err != nil || !signer.Equal(target.enrollment.caCert) {
		t.Fatal("profile signer did not follow activation", err)
	}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.LastSeenAt = time.Now()
	if err = a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	migration := rows[0]
	migration.NextAttempt = time.Time{}
	if err = a.Certificates.SaveMigration(ctx, req.ID, job.To, migration); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceRollover(ctx, job); err != nil {
		t.Fatal(err)
	}
	rows, _, err = a.Certificates.Migrations(ctx, req.ID, job.To, "", 100)
	if err != nil || rows[0].Phase != "trust-pending" || rows[0].TrustCommand == "" {
		t.Fatal("trust was not queued first", rows, err)
	}
	replacement, err := a.replacementStore().
		TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "read", At: time.Now()})
	if err != nil || replacement != nil {
		t.Fatal("identity issued before trust acknowledgement", replacement, err)
	}
	// A pinned successor represents the replacement store's completed handshake.
	evidence.Issuer = cms.Fingerprint(target.enrollment.caCert)
	data, _ = json.Marshal(evidence)
	if err = a.protocol.Update(
		ctx,
		[]string{"issued-identity:successor-pin"},
		func(tx state.Tx) error {
			return tx.Put(ctx, state.Record{Key: "issued-identity:successor-pin", Value: data})
		},
	); err != nil {
		t.Fatal(err)
	}
	e.CertHash = "successor-pin"
	if err = a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	migration = rows[0]
	migration.NextAttempt = time.Time{}
	if err = a.Certificates.SaveMigration(ctx, req.ID, job.To, migration); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceRollover(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceRollover(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err = a.retireManagedIssuer(ctx, req.ID, job.From); err != nil {
		t.Fatal(err)
	}
	if _, err = a.managedIssuer(ctx, job.From); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Fatal("retired issuing service remains available", err)
	}
	called := false
	h := a.legacyIssuance(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }),
	)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "GET", "/scep", nil))
	if response.Code != http.StatusGone || called {
		t.Fatal("legacy issuer still serves issuance")
	}
	pairs, err := a.managedIssuerPairs(ctx)
	if err != nil || len(pairs) != 1 {
		t.Fatal("retired CA still trusted", err)
	}
	all, err := a.managedPairs(ctx, true)
	if err != nil || len(all) != 2 {
		t.Fatal("retirement lost certificate-status material", err)
	}
	if _, err = a.managedRegistry(ctx); err != nil {
		t.Fatal("retirement broke CRL/OCSP registry", err)
	}
}

// TestSetupAPIAuthRolesAndPublicHistory checks setup API auth roles and public history.
func TestSetupAPIAuthRolesAndPublicHistory(t *testing.T) {
	path, err := InitSetupFile(
		SetupInitOptions{
			Directory: t.TempDir(),
			Role:      "customer",
			Storage:   "sqlite",
			Listen:    "127.0.0.1:8443",
			PublicURL: "https://localhost:8443",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSetupFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := a.admin.Bootstrap(t.Context(), "fixture-root", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.admin.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "fixture", Source: `permit(principal == MDM::Principal::"fixture-root",action,resource);`}); err != nil {
		t.Fatal(err)
	}
	cfg.BootstrapToken = string(token)
	defer func(cleanup func() error) { _ = cleanup() }(a.Close)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		a.Handler.ServeHTTP(response, req)
		return response
	}
	if response := request("GET", "/admin/v1/setup", "", ""); response.Code != 401 {
		t.Fatal(response.Code)
	}
	body := `{"subject":{"CommonName":"test vendor"}}`
	if response := request(
		"POST",
		"/admin/v1/setup/vendor-signing/request",
		body,
		cfg.BootstrapToken,
	); response.Code != 400 {
		t.Fatal(response.Code, response.Body.String())
	}
	body = `{"subject":{"CommonName":"customer"}}`
	response := request("POST", "/admin/v1/setup/mdm-push/request", body, cfg.BootstrapToken)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request("GET", "/admin/v1/setup/workflow/mdm-push/history", "", cfg.BootstrapToken)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "fixture-root") {
		t.Fatal(response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "PRIVATE KEY") ||
		strings.Contains(response.Body.String(), "SignedRequest") {
		t.Fatal("history leaked material")
	}
	response = request("GET", "/admin/v1/setup/workflow/mdm-push/export?artifact=csr", "", cfg.BootstrapToken)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "BEGIN CERTIFICATE REQUEST") {
		t.Fatal("public CSR export failed", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{
		"Content-Type":           "application/octet-stream",
		"Content-Disposition":    `attachment; filename="certificate-artifact.bin"`,
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("unsafe artifact response header %s: got %q, want %q", name, got, want)
		}
	}
	response = request(
		"GET",
		"/admin/v1/setup/workflow/mdm-push/export?artifact=key",
		"",
		cfg.BootstrapToken,
	)
	if response.Code != 400 {
		t.Fatal("private key export allowed", response.Code)
	}
	response = request("GET", "/admin/v1/setup", "", cfg.BootstrapToken)
	var status SetupStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.Ready {
		t.Fatal("incomplete setup reported ready", err)
	}
}

// TestBootstrapKeepsImportedSecretsAndRejectsDifferentDatabaseKey checks that bootstrap keeps
// imported secrets and rejects different database key.
func TestBootstrapKeepsImportedSecretsAndRejectsDifferentDatabaseKey(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "old-key")
	material := bytes.Repeat([]byte("a"), 32)
	if err := os.WriteFile(secret, material, 0o600); err != nil {
		t.Fatal(err)
	}
	options := SetupInitOptions{
		Directory:         filepath.Join(dir, "managed"),
		Role:              "customer",
		Storage:           "sqlite",
		StorageKeyName:    "old",
		StorageKeyFile:    secret,
		StorageKeyAliases: []string{"legacy"},
	}
	path, err := InitSetupFile(options)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSetupFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DSN = ":memory:"
	if opened, err := OpenSetup(t.Context(), cfg); err == nil {
		_ = opened.Close()
		t.Fatal("managed setup accepted volatile SQLite state")
	}
	for _, name := range []string{"old", "legacy"} {
		// #nosec G304 -- The test controls this fixture path within its private workspace.
		actual, err := os.ReadFile(filepath.Join(filepath.Dir(path), "secrets", name))
		if err != nil || !bytes.Equal(actual, material) {
			t.Fatal("changed encryption key", name, err)
		}
	}
	db := filepath.Join(dir, "existing.sqlite")
	if err := os.WriteFile(db, []byte("existing state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitSetupFile(
		SetupInitOptions{
			Directory: filepath.Join(dir, "wrong"),
			Role:      "customer",
			Storage:   "sqlite",
			DSN:       db,
		},
	); !errors.Is(
		err,
		ErrConfig,
	) {
		t.Fatal("attached new key to existing database", err)
	}
}

// TestHTTPSCARolloverWaitsForTrustBeforeChangingTLS checks that httpsca rollover waits for trust
// before changing TLS.
func TestHTTPSCARolloverWaitsForTrustBeforeChangingTLS(t *testing.T) {
	ctx := t.Context()
	a, id := managedRolloverApp(t)
	a.cfg.Setup = &SetupConfig{
		Role:      "combined",
		IssuerID:  "enrollment-ca",
		HTTPSCAID: "server-https-ca",
		HTTPSID:   "server-https",
	}
	a.Certificates = &lifecycle.Manager{Store: a.protocol}
	key, err := x509.MarshalPKCS8PrivateKey(a.enroll.caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.enroll.caCert.Raw})
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	for _, name := range []string{"enrollment-ca", "server-https-ca"} {
		if _, err := a.Certificates.Adopt(
			ctx,
			lifecycle.Request{ID: name, Kind: lifecycle.EnrollmentCA},
			cert,
			private,
		); err != nil {
			t.Fatal(err)
		}
	}
	request := lifecycle.Request{
		ID:       "server-https",
		Kind:     lifecycle.ServerHTTPS,
		Subject:  pkix.Name{CommonName: "mdm.example"},
		DNSNames: []string{"mdm.example"},
	}
	first, err := a.ExecuteSetup(ctx, lifecycle.ServerHTTPS, "lab", SetupRequest{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.ExecuteSetup(
		ctx,
		lifecycle.ServerHTTPS,
		"activate",
		SetupRequest{Request: request, Revision: first.Identity.Pending},
	); err != nil {
		t.Fatal(err)
	}
	oldTLS, err := a.LoadTLSCertificate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := a.Certificates.Get(ctx, "server-https-ca")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := a.Certificates.Begin(ctx, ca.Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Certificates.CreateIssuer(
		ctx,
		ca.ID,
		pending.Pending,
		20*365*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	job, err := a.startHTTPSTrustRollover(ctx, ca.ID, pending.Pending)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.advanceHTTPSTrust(ctx, job); err != nil {
		t.Fatal(err)
	}
	current, err := a.Certificates.Get(ctx, ca.ID)
	if err != nil || current.Active != "1" {
		t.Fatal("CA switched before trust acknowledgement", err)
	}
	unchanged, err := a.LoadTLSCertificate(ctx)
	if err != nil || !bytes.Equal(oldTLS.Certificate[0], unchanged.Certificate[0]) {
		t.Fatal("TLS switched before trust acknowledgement", err)
	}
	migrations, _, err := a.Certificates.Migrations(ctx, ca.ID, job.To, "", 100)
	if err != nil || len(migrations) != 1 || migrations[0].Phase != "trust-pending" {
		t.Fatal(migrations, err)
	}
	// A real worker obtains this confirmation from the acknowledged InstallProfile
	// command. Mark the fixture command as acknowledged through the queue API.
	if _, err := a.Store.Next(ctx, id, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.StoreResult(
		ctx,
		id,
		&mdm.Response{CommandUUID: migrations[0].TrustCommand, Status: mdm.StatusAcknowledged},
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	migration := migrations[0]
	migration.NextAttempt = time.Time{}
	if err = a.Certificates.SaveMigration(ctx, ca.ID, job.To, migration); err != nil {
		t.Fatal(err)
	}
	// Ensure the second leaf's second-resolution validity extends the first.
	time.Sleep(1100 * time.Millisecond)
	if err = a.advanceHTTPSTrust(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceHTTPSTrust(ctx, job); err != nil {
		t.Fatal(err)
	}
	current, err = a.Certificates.Get(ctx, ca.ID)
	if err != nil || current.Active != "2" {
		t.Fatal("confirmed trust did not activate", err)
	}
	replacement, err := a.LoadTLSCertificate(ctx)
	if err != nil || bytes.Equal(oldTLS.Certificate[0], replacement.Certificate[0]) {
		t.Fatal("TLS identity did not reload", err)
	}
	if _, err = a.retireHTTPSTrust(ctx, ca.ID, "1"); err != nil {
		t.Fatal(err)
	}
}
