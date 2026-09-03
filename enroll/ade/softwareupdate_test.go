package ade_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/enroll/adetest"
	"github.com/deploymenttheory/go-apple-dm/gdmf"
	"github.com/deploymenttheory/go-apple-dm/gdmf/gdmftest"
	"github.com/deploymenttheory/go-apple-dm/plist"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/schema/errors"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

func parsedInfo(serial string) *ade.Parsed {
	info := adetest.Info(serial)
	return &ade.Parsed{MachineInfo: info, Origin: ade.OriginBody, Verified: true, Platform: ade.PlatformFromProduct(info.PRODUCT)}
}

func requireVersion(v string) ade.Policy {
	return ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
		return ade.Target{OSVersion: v}, true, nil
	})
}

var errPolicy = errors.New("policy broke")

type pssoPolicy struct {
	ade.Policy
	details  *schemaerrors.CodePlatformSSORequiredDetails
	required bool
	err      error
}

func (p pssoPolicy) PlatformSSO(context.Context, *ade.Parsed) (*schemaerrors.CodePlatformSSORequiredDetails, bool, error) {
	return p.details, p.required, p.err
}

func TestSoftwareUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))

	t.Run("Skipped", func(t *testing.T) {
		t.Parallel()
		// No policy.
		d, err := ade.Gate(ctx, parsedInfo("s"), nil, nil, quiet)
		if err != nil || d.Action != ade.Proceed {
			t.Fatalf("%+v %v", d, err)
		}
		// The device cannot take an update request: the policy is never asked.
		p := parsedInfo("s")
		p.MDMCANREQUESTSOFTWAREUPDATE = nil
		asked := false
		policy := ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
			asked = true
			return ade.Target{OSVersion: "99"}, true, nil
		})
		d, err = ade.Gate(ctx, p, policy, nil, quiet)
		if err != nil || d.Action != ade.Proceed || asked {
			t.Fatalf("%+v %v asked=%v", d, err, asked)
		}
		p.MDMCANREQUESTSOFTWAREUPDATE = new(false)
		if d, _ := ade.Gate(ctx, p, policy, nil, quiet); d.Action != ade.Proceed || asked {
			t.Fatal("false flag")
		}
		// Policy says no.
		no := ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) { return ade.Target{}, false, nil })
		if d, _ := ade.Gate(ctx, parsedInfo("s"), no, nil, quiet); d.Action != ade.Proceed {
			t.Fatal("not required")
		}
		// Already at or above the target.
		for _, v := range []string{"17.5.1", "17.5", "17.0"} {
			if d, _ := ade.Gate(ctx, parsedInfo("s"), requireVersion(v), nil, quiet); d.Action != ade.Proceed {
				t.Fatalf("at %s: %+v", v, d)
			}
		}
		// Policy error surfaces.
		bad := ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) { return ade.Target{}, false, errPolicy })
		if _, err := ade.Gate(ctx, parsedInfo("s"), bad, nil, quiet); !errors.Is(err, ade.ErrGate) || !errors.Is(err, errPolicy) {
			t.Fatalf("%v", err)
		}
		// Writing a Proceed decision is an error.
		if err := (&ade.Decision{Action: ade.Proceed}).Write(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody)); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("write proceed: %v", err)
		}
		if ade.Action(9).String() != "Action(9)" || ade.Proceed.String() != "proceed" || ade.PSSORequired.String() != "psso-required" || ade.SoftwareUpdateRequired.String() != "software-update-required" {
			t.Fatal("String")
		}
	})
	t.Run("RequiredJSON", func(t *testing.T) {
		t.Parallel()
		d, err := ade.Gate(ctx, parsedInfo("s"), requireVersion("18.0"), nil, quiet)
		if err != nil || d.Action != ade.SoftwareUpdateRequired || d.SoftwareUpdate.Details.OSVersion != "18.0" {
			t.Fatalf("%+v %v", d, err)
		}
		for _, accept := range []string{"", "*/*", "application/json", "text/html;q=0.9, application/json", "application/xml;q=0.1"} {
			req := httptest.NewRequest(http.MethodPost, "/enroll", http.NoBody)
			if accept != "" {
				req.Header.Set("Accept", accept)
			}
			rec := httptest.NewRecorder()
			if err := d.Write(rec, req); err != nil {
				t.Fatal(err)
			}
			wantJSON := !strings.Contains(accept, "xml") || strings.Contains(accept, "json")
			if rec.Code != http.StatusForbidden || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%q: %d %v", accept, rec.Code, rec.Header())
			}
			if !wantJSON {
				continue
			}
			if rec.Header().Get("Content-Type") != ade.ContentTypeJSON {
				t.Fatalf("%q: %s", accept, rec.Header().Get("Content-Type"))
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != "com.apple.softwareupdate.required" || body["details"].(map[string]any)["OSVersion"] != "18.0" {
				t.Fatalf("%q: %s", accept, rec.Body.String())
			}
		}
	})
	t.Run("RequiredPlist", func(t *testing.T) {
		t.Parallel()
		d, _ := ade.Gate(ctx, parsedInfo("s"), ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
			return ade.Target{OSVersion: "18.0", BuildVersion: "22A3354"}, true, nil
		}), nil, quiet)
		for _, accept := range []string{"application/xml", "text/xml", "application/x-plist", "application/xml, application/json"} {
			req := httptest.NewRequest(http.MethodPost, "/enroll", http.NoBody)
			req.Header.Set("Accept", accept)
			rec := httptest.NewRecorder()
			if err := d.Write(rec, req); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") != ade.ContentTypePlist {
				t.Fatalf("%q: %d %s", accept, rec.Code, rec.Header().Get("Content-Type"))
			}
			var body schemaerrors.CodeSoftwareUpdateRequired
			if err := plist.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != schemaerrors.ErrorCodeCodeSoftwareUpdateRequired || body.Details.OSVersion != "18.0" || *body.Details.BuildVersion != "22A3354" {
				t.Fatalf("%+v", body)
			}
		}
	})
	t.Run("SchemaConformance", func(t *testing.T) {
		t.Parallel()
		d, err := ade.Gate(ctx, parsedInfo("s"), ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
			return ade.Target{OSVersion: "18.1", BuildVersion: "22B5007p", RequireBetaProgram: &ade.BetaProgram{Description: "iOS 18.1 beta", Token: "seed-token"}}, true, nil
		}), nil, quiet)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SoftwareUpdate.Validate(support.Target{}); err != nil {
			t.Fatal(err)
		}
		if err := d.SoftwareUpdate.Validate(support.Target{OS: support.IOS, Version: support.V(17, 5, 0)}); err != nil {
			t.Fatalf("iOS 17.5 target: %v", err)
		}
		if d.SoftwareUpdate.Details.RequireBetaProgram.Token != "seed-token" {
			t.Fatalf("%+v", d.SoftwareUpdate.Details)
		}
		// A beta program without its token fails the schema and the gate.
		_, err = ade.Gate(ctx, parsedInfo("s"), ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
			return ade.Target{OSVersion: "18.1", RequireBetaProgram: &ade.BetaProgram{Description: "beta"}}, true, nil
		}), nil, quiet)
		if !errors.Is(err, ade.ErrGate) {
			t.Fatalf("invalid body: %v", err)
		}
		// A lookup answer with an empty version cannot be validated either.
		empty := &gdmftest.Fake{Assets: map[string]*gdmf.Asset{"iPhone15,2": {}}}
		p := parsedInfo("s")
		p.OSVERSION = ""
		if _, err := ade.Gate(ctx, p, requireVersion(""), empty, quiet); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("empty asset: %v", err)
		}
	})
	t.Run("GDMFFailureProceeds", func(t *testing.T) {
		t.Parallel()
		var log bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&log, nil))
		failing := &gdmftest.Fake{Err: gdmf.ErrRequest}
		d, err := ade.Gate(ctx, parsedInfo("s"), requireVersion(""), failing, logger)
		if err != nil || d.Action != ade.Proceed || failing.Calls.Load() != 1 || !strings.Contains(log.String(), "lookup failed") {
			t.Fatalf("%+v %v %s", d, err, log.String())
		}
		// No lookup configured for a "latest" target also proceeds, logged.
		log.Reset()
		if d, err := ade.Gate(ctx, parsedInfo("s"), requireVersion(""), nil, logger); err != nil || d.Action != ade.Proceed || !strings.Contains(log.String(), "no lookup") {
			t.Fatalf("%+v %v %s", d, err, log.String())
		}
		// A working lookup resolves "latest": 17.5.1 device, 18.0 published.
		fake := gdmftest.NewFake("iPhone15,2")
		d, err = ade.Gate(ctx, parsedInfo("s"), requireVersion(""), fake, logger)
		if err != nil || d.Action != ade.SoftwareUpdateRequired || d.SoftwareUpdate.Details.OSVersion != "18.0" || *d.SoftwareUpdate.Details.BuildVersion != "22A3354" {
			t.Fatalf("%+v %v", d, err)
		}
		// The device at the latest proceeds; SOFTWARE_UPDATE_DEVICE_ID is preferred over PRODUCT for the lookup.
		p := parsedInfo("s")
		p.OSVERSION = "18.0"
		if d, _ := ade.Gate(ctx, p, requireVersion(""), fake, logger); d.Action != ade.Proceed {
			t.Fatalf("%+v", d)
		}
		p.PRODUCT, p.SOFTWAREUPDATEDEVICEID = "Mac14,7", new("iPhone15,2")
		if d, _ := ade.Gate(ctx, p, requireVersion(""), fake, logger); d.Action != ade.Proceed {
			t.Fatalf("device id: %+v", d)
		}
		p.SOFTWAREUPDATEDEVICEID = nil
		if d, _ := ade.Gate(ctx, p, requireVersion(""), fake, logger); d.Action != ade.Proceed || !strings.Contains(d.Reason, "Mac14,7") {
			t.Fatalf("product fallback: %+v", d)
		}
		// A nil logger is tolerated.
		if d, err := ade.Gate(ctx, parsedInfo("s"), requireVersion(""), failing, nil); err != nil || d.Action != ade.Proceed {
			t.Fatal("nil logger")
		}
	})
	t.Run("PSSORequired", func(t *testing.T) {
		t.Parallel()
		details := &schemaerrors.CodePlatformSSORequiredDetails{
			ProfileURL: "https://mdm.example.com/psso.mobileconfig", AuthURL: "https://mdm.example.com/psso/auth",
			Package: schemaerrors.CodePlatformSSORequiredDetailsPackage{ManifestURL: "https://mdm.example.com/psso/manifest.plist"},
		}
		policy := pssoPolicy{Policy: requireVersion("18.0"), details: details, required: true}
		mac := parsedInfo("s")
		mac.PRODUCT, mac.OSVERSION, mac.MDMCANREQUESTPSSOCONFIG = "Mac16,1", "26.0", new(true)
		d, err := ade.Gate(ctx, mac, policy, nil, quiet)
		if err != nil || d.Action != ade.PSSORequired || d.PSSO.Code != schemaerrors.ErrorCodeCodePlatformSSORequired || d.PSSO.Details.AuthURL != details.AuthURL {
			t.Fatalf("%+v %v", d, err)
		}
		if err := d.PSSO.Validate(support.Target{OS: support.MacOS, Version: support.V(26, 0, 0)}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/enroll", http.NoBody)
		rec := httptest.NewRecorder()
		if err := d.Write(rec, req); err != nil || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "com.apple.psso.required") {
			t.Fatalf("%v %d %s", err, rec.Code, rec.Body.String())
		}
		// Without MDM_CAN_REQUEST_PSSO_CONFIG the PSSO policy is not consulted and the update check runs.
		mac.MDMCANREQUESTPSSOCONFIG = nil
		if d, _ := ade.Gate(ctx, mac, policy, nil, quiet); d.Action != ade.Proceed {
			t.Fatalf("no psso flag: %+v", d)
		}
		mac.MDMCANREQUESTPSSOCONFIG, mac.OSVERSION = new(true), "15.0"
		if d, _ := ade.Gate(ctx, mac, pssoPolicy{Policy: requireVersion("18.0"), required: false}, nil, quiet); d.Action != ade.SoftwareUpdateRequired {
			t.Fatalf("psso not required falls through: %+v", d)
		}
		// Policy errors and missing details are gate errors.
		if _, err := ade.Gate(ctx, mac, pssoPolicy{Policy: policy.Policy, err: errPolicy}, nil, quiet); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("psso error: %v", err)
		}
		if _, err := ade.Gate(ctx, mac, pssoPolicy{Policy: policy.Policy, required: true}, nil, quiet); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("no details: %v", err)
		}
		if _, err := ade.Gate(ctx, mac, pssoPolicy{Policy: policy.Policy, required: true, details: &schemaerrors.CodePlatformSSORequiredDetails{}}, nil, quiet); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("invalid details: %v", err)
		}
	})
	t.Run("WriteErrorEncoding", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		if err := ade.WriteError(httptest.NewRecorder(), req, http.StatusForbidden, make(chan int)); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("json: %v", err)
		}
		req.Header.Set("Accept", "application/xml")
		if err := ade.WriteError(httptest.NewRecorder(), req, http.StatusForbidden, make(chan int)); !errors.Is(err, ade.ErrGate) {
			t.Fatalf("plist: %v", err)
		}
	})
}
