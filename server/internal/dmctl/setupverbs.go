package dmctl

import (
	"context"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

//nolint:gocyclo // Keep the ordered workflow transitions and their failure handling together.
func runSetup(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"%w: setup needs init, status, check, vendor, push, https, issuer, workflow or profile",
			ErrUsage,
		)
	}
	group, operation := args[0], ""
	rest := args[1:]
	if group == "vendor" || group == "push" || group == "https" || group == "issuer" ||
		group == "workflow" ||
		group == "profile" {
		if len(rest) == 0 {
			return fmt.Errorf("%w: setup %s needs an operation", ErrUsage, group)
		}
		operation, rest = rest[0], rest[1:]
	}
	fs := e.verbFlags("setup " + group + " " + operation)
	setupFile := fs.String(
		"setup-file",
		e.getenv("DM_SETUP_FILE"),
		"local setup file for bootstrap access",
	)
	dir := fs.String("dir", "test-lab/local/certs", "bootstrap directory")
	benchDirectory := fs.String(
		"from-bench",
		"",
		"adopt an existing live bench including database and identity keys",
	)
	adminTokenFile := fs.String(
		"admin-token-file",
		"",
		"existing admin token for database adoption",
	)
	issuanceKeyFile := fs.String("issuance-key-file", "", "existing SCEP identifier HMAC key")
	acmeKeyFile := fs.String("acme-key-file", "", "existing ACME identifier HMAC key")
	cursor := fs.String("cursor", "", "migration page cursor")
	pageSize := fs.Int("page-size", 100, "migration page size")
	storageKeyFile := fs.String(
		"storage-key-file",
		"",
		"existing encryption key file when adopting a database",
	)
	storageKeyName := fs.String(
		"storage-key-name",
		"storage",
		"encryption key ID; retain its original name when adopting",
	)
	role := fs.String("role", "customer", "vendor, customer or combined")
	storage := fs.String("storage", "sqlite", "sqlite, postgres or mysql")
	dsnEnv := fs.String("dsn-env", "DM_DSN", "environment variable holding a database DSN")
	publicURL := fs.String("public-url", "https://127.0.0.1:8443", "public enrollment URL")
	listen := fs.String("listen", "127.0.0.1:8443", "HTTPS listen address")
	id := fs.String("id", "", "identity ID (defaults to configured role identity)")
	force := fs.Bool("force", false, "prepare renewal before the scheduled renewal window")
	rev := fs.String("revision", "", "pending certificate revision")
	cn := fs.String("cn", "", "certificate subject common name")
	org := fs.String("organization", "go-apple-dm", "certificate organization")
	hosts := fs.String("hosts", "", "comma-separated HTTPS hostnames or IP addresses")
	account := fs.String("account", "", "Apple account identifier for renewal guidance")
	certFile := fs.String("cert", "", "returned certificate file, PEM or DER")
	keyFile := fs.String("key", "", "existing private key file, for adopt only")
	csrFile := fs.String("csr", "", "public customer CSR to sign")
	signedFile := fs.String("signed-request", "", "vendor-signed portal request")
	vendor := fs.String("vendor", "", "configured vendor identity ID")
	artifact := fs.String("artifact", "csr", "csr, signed-request or certificate")
	out := fs.String("out", "", "public artifact output file (never overwritten)")
	directory := fs.String("directory", lifecycle.ProductionDirectory, "public ACME directory URL")
	contact := fs.String("contact", "", "public ACME account contact email")
	acceptTerms := fs.Bool("accept-terms", false, "accept the public ACME CA terms")
	http01Listen := fs.String("http01-listen", "", "local HTTP-01 challenge listen address")
	kindFlag := fs.String("kind", "", "certificate kind for setup adopt")
	validity := fs.Int("validity-days", 0, "issuer validity (default ten years)")
	device := fs.String("device-id", "", "hardware UUID or device identifier")
	serial := fs.String("serial", "", "hardware serial number")
	product := fs.String("product", "", "device product identifier")
	osVersion := fs.String("os-version", "", "device OS version")
	hardware := fs.String("hardware", "", "Mac hardware: apple-silicon, t2 or unknown")
	identity := fs.String("identity", "acme", "device identity method: acme or scep")
	rights := fs.Int("access-rights", 19, "enrollment access rights")
	scope := fs.String(
		"scope",
		"",
		"installation scope: User or System (manual macOS defaults to User)",
	)
	pos, err := e.parseVerb(fs, rest)
	if err != nil {
		return wrapError(err)
	}
	if len(pos) > 0 {
		return fmt.Errorf("%w: unexpected setup arguments", ErrUsage)
	}
	if *benchDirectory != "" {
		if group != "adopt" {
			return fmt.Errorf("%w: -from-bench requires setup adopt", ErrUsage)
		}
		return runSetupBenchAdoption(ctx, e, *benchDirectory, *dir, *role)
	}
	if group == "vendor" && operation == "sign" && *out == "" {
		return fmt.Errorf("%w: signing requires -out", ErrUsage)
	}
	if group == "init" {
		path, err := app.InitSetupFile(
			app.SetupInitOptions{
				Directory:       *dir,
				Role:            *role,
				Storage:         *storage,
				DSN:             e.getenv(*dsnEnv),
				PublicURL:       *publicURL,
				Listen:          *listen,
				Organization:    *org,
				StorageKeyFile:  *storageKeyFile,
				StorageKeyName:  *storageKeyName,
				AdminTokenFile:  *adminTokenFile,
				IssuanceKeyFile: *issuanceKeyFile,
				ACMEKeyFile:     *acmeKeyFile,
				HTTP01Listen:    *http01Listen,
			},
		)
		if err != nil {
			return wrapError(err)
		}
		cfg, err := app.LoadSetupFile(path, e.getenv)
		if err != nil {
			return wrapError(err)
		}
		a, err := app.OpenSetup(ctx, cfg)
		if err != nil {
			return wrapError(err)
		}
		defer a.Close() //nolint:contextcheck // Close owns a bounded drain after cancellation.
		return wrapError(
			json.NewEncoder(e.stdout).
				Encode(map[string]string{"setupFile": path, "nextAction": "create or import HTTPS and enrollment identities; request the Apple certificates"}),
		)
	}

	if group == "adopt" {
		group, operation = *kindFlag, "adopt"
	}
	var local *app.App
	if *setupFile != "" {
		cfg, err := app.LoadSetupFile(*setupFile, e.getenv)
		if err != nil {
			return wrapError(err)
		}
		local, err = app.OpenSetup(ctx, cfg)
		if err != nil {
			return wrapError(err)
		}
		defer local.Close() //nolint:contextcheck // Close owns a bounded drain after cancellation.
	}
	if group == "status" || group == "check" {
		if local != nil {
			status, err := local.CertificateSetupStatus(ctx)
			if err != nil {
				return wrapError(err)
			}
			if group == "check" && status.Ready {
				cfg, err := app.LoadSetupFile(*setupFile, e.getenv)
				if err != nil {
					return wrapError(err)
				}
				ready, err := app.Build(ctx, cfg)
				if err != nil {
					return wrapError(err)
				}
				if _, err = ready.LoadTLSCertificate(ctx); err != nil {
					_ = ready.Close() //nolint:contextcheck // Close owns a bounded drain after cancellation.
					return wrapError(err)
				}
				_ = ready.Close() //nolint:contextcheck // Close owns a bounded drain after cancellation.
			}
			if err := json.NewEncoder(e.stdout).Encode(status); err != nil {
				return wrapError(err)
			}
			if group == "check" && !status.Ready {
				return fmt.Errorf("%w: certificate setup is incomplete", ErrPartial)
			}
			return nil
		}
		c, err := e.client()
		if err != nil {
			return wrapError(err)
		}
		resp, err := c.Do(ctx, "GET", "/setup", nil, nil)
		if err != nil {
			return wrapError(err)
		}
		if err := e.emit(resp, nil); err != nil {
			return wrapError(err)
		}
		if group == "check" {
			var status app.SetupStatus
			if err := json.Unmarshal(resp.Body, &status); err != nil {
				return wrapError(err)
			}
			if !status.Ready {
				return fmt.Errorf("%w: certificate setup is incomplete", ErrPartial)
			}
		}
		return nil
	}
	if group == "profile" {
		if (operation != "export" && operation != "trust") || *out == "" {
			return fmt.Errorf("%w: profile export or trust requires -out", ErrUsage)
		}
		req := app.EnrollmentProfileRequest{
			DeviceID:     *device,
			Serial:       *serial,
			Product:      *product,
			OSVersion:    *osVersion,
			MacHardware:  enroll.MacHardware(*hardware),
			Identity:     *identity,
			AccessRights: enroll.AccessRights(*rights),
			Scope:        *scope,
		}
		var data []byte
		if local != nil {
			if operation == "trust" {
				data, err = local.SetupTrustProfile(ctx)
			} else {
				// Profile creation needs the enrollment services, but starts no workers or listeners.
				cfg, e2 := app.LoadSetupFile(*setupFile, e.getenv)
				if e2 != nil {
					return wrapError(e2)
				}
				full, e2 := app.Build(ctx, cfg)
				if e2 != nil {
					return wrapError(e2)
				}
				defer full.Close() //nolint:contextcheck // Close owns a bounded drain after cancellation.
				data, err = full.ExportEnrollmentProfile(ctx, req)
			}
		} else {
			c, e2 := e.client()
			if e2 != nil {
				return wrapError(e2)
			}
			method, path := http.MethodGet, "/setup/trust"
			var body []byte
			if operation == "export" {
				method, path = http.MethodPost, "/enrollment-profiles"
				body, err = json.Marshal(req)
				if err != nil {
					return wrapError(err)
				}
			}
			resp, e2 := c.Do(ctx, method, path, nil, body)
			err = e2
			if err == nil {
				data = resp.Body
			}
		}
		if err != nil {
			return wrapError(err)
		}
		return writeSetupArtifact(*out, data)
	}
	if group == "workflow" {
		if *id == "" {
			return fmt.Errorf("%w: workflow needs -id", ErrUsage)
		}
		if operation == "history" {
			if local != nil {
				entries, next, err := local.Certificates.History(ctx, *id, *cursor, *pageSize)
				if err != nil {
					return wrapError(err)
				}
				return wrapError(
					json.NewEncoder(e.stdout).
						Encode(app.SetupResult{History: entries, NextCursor: next}),
				)
			}
			c, err := e.client()
			if err != nil {
				return wrapError(err)
			}
			resp, err := c.Do(
				ctx,
				"GET",
				"/setup/workflow/"+url.PathEscape(*id)+"/history",
				url.Values{"cursor": {*cursor}},
				nil,
			)
			if err != nil {
				return wrapError(err)
			}
			return e.emit(resp, nil)
		}
		if operation == "show" {
			if local != nil {
				item, err := local.Certificates.Get(ctx, *id)
				if err != nil {
					return wrapError(err)
				}
				return wrapError(json.NewEncoder(e.stdout).Encode(item))
			}
			c, err := e.client()
			if err != nil {
				return wrapError(err)
			}
			resp, err := c.Do(ctx, "GET", "/setup/workflow/"+url.PathEscape(*id), nil, nil)
			if err != nil {
				return wrapError(err)
			}
			return e.emit(resp, nil)
		}
		if operation == "export" {
			if *out == "" {
				return fmt.Errorf("%w: export requires -out", ErrUsage)
			}
			var data []byte
			if local != nil {
				data, err = local.Certificates.Export(ctx, *id, *rev, *artifact)
			} else {
				c, e2 := e.client()
				if e2 != nil {
					return wrapError(e2)
				}
				resp, e2 := c.Do(
					ctx,
					"GET",
					"/setup/workflow/"+url.PathEscape(*id)+"/export",
					url.Values{"revision": {*rev}, "artifact": {*artifact}},
					nil,
				)
				err = e2
				if err == nil {
					data = resp.Body
				}
			}
			if err != nil {
				return wrapError(err)
			}
			return writeSetupArtifact(*out, data)
		}
		if operation == "cancel" {
			var item lifecycle.Identity
			if local != nil {
				item, err = local.Certificates.Get(ctx, *id)
			} else {
				c, e2 := e.client()
				if e2 != nil {
					return wrapError(e2)
				}
				resp, e2 := c.Do(ctx, "GET", "/setup/workflow/"+url.PathEscape(*id), nil, nil)
				err = e2
				if err == nil {
					err = json.Unmarshal(resp.Body, &item)
				}
			}
			if err != nil {
				return wrapError(err)
			}
			group = string(item.Kind)
		} else {
			return fmt.Errorf("%w: workflow operation", ErrUsage)
		}
	}
	req := app.SetupRequest{
		Force: *force,
		Request: lifecycle.Request{
			ID:      *id,
			Kind:    lifecycle.Kind(group),
			Subject: pkix.Name{CommonName: *cn, Organization: []string{*org}},
			Account: *account,
		},
		Device:       *device,
		Cursor:       *cursor,
		Limit:        *pageSize,
		Revision:     *rev,
		Vendor:       *vendor,
		Artifact:     *artifact,
		ValidityDays: *validity,
	}
	if *hosts != "" {
		req.DNSNames = strings.Split(*hosts, ",")
	}
	for _, source := range []struct {
		file        string
		destination *[]byte
	}{{*certFile, &req.Certificate}, {*keyFile, &req.Key}, {*csrFile, &req.CSR}, {*signedFile, &req.SignedRequest}} {
		file, destination := source.file, source.destination
		if file != "" {
			// #nosec G304 -- Local certificate/key/CSR file selected by a CLI flag.
			data, err := os.ReadFile(file)
			if err != nil {
				return wrapError(err)
			}
			*destination = data
		}
	}
	if operation == "acme" {
		req.PublicACME = &lifecycle.PublicACMEOptions{
			Directory:   *directory,
			Contact:     *contact,
			AcceptTerms: *acceptTerms,
		}
	}
	var result app.SetupResult
	if local != nil {
		result, err = local.ExecuteSetup(ctx, lifecycle.Kind(group), operation, req)
		if err == nil && operation == "acme" {
			address := *http01Listen
			if address == "" {
				address = ":80"
			}
			listener, e2 := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
			if e2 != nil {
				return wrapError(e2)
			}
			server := &http.Server{
				Handler:           local.Certificates.HTTP01Handler(),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       10 * time.Second,
				WriteTimeout:      10 * time.Second,
			}
			defer server.Close() //nolint:contextcheck // Close owns a bounded drain after cancellation.
			go func() { _ = server.Serve(listener) }()
			item, e2 := local.Certificates.RunPublicACME(ctx, result.Identity.ID, nil)
			err = e2
			result.Identity = &item
		}
	} else {
		c, e2 := e.client()
		if e2 != nil {
			return wrapError(e2)
		}
		body, e2 := json.Marshal(req)
		if e2 != nil {
			return wrapError(e2)
		}
		resp, e2 := c.Do(
			ctx,
			"POST",
			"/setup/"+url.PathEscape(group)+"/"+url.PathEscape(operation),
			nil,
			body,
		)
		err = e2
		if err == nil {
			err = json.Unmarshal(resp.Body, &result)
		}
	}
	if err != nil {
		return wrapError(err)
	}
	if len(result.Data) > 0 {
		if *out == "" {
			return fmt.Errorf("%w: signing requires -out", ErrUsage)
		}
		return writeSetupArtifact(*out, result.Data)
	}
	return wrapError(json.NewEncoder(e.stdout).Encode(result))
}

func writeSetupArtifact(path string, data []byte) error {
	// #nosec G304 -- Explicit CLI output path; exclusive creation prevents replacing an existing file.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrapError(err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return wrapError(err)
	}
	return wrapError(closeErr)
}
