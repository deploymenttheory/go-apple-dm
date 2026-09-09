package bench

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Scenario is the maintained inventory; implementations never depend on testing.T.
// Regression names retain the original detailed assertions during migration.
//
//nolint:tagliatelle // Catalogue JSON uses the administration PascalCase convention.
type Scenario struct {
	Settings      map[string]string                                 `json:"Settings,omitempty"`
	Topology      string                                            `json:"Topology,omitempty"`
	ID            string                                            `json:"ID"`
	Name          string                                            `json:"Name"`
	Family        string                                            `json:"Family"`
	Regression    string                                            `json:"Regression"`
	Modes         []string                                          `json:"Modes"`
	Prerequisites []string                                          `json:"Prerequisites"`
	Run           func(context.Context, *Environment, string) error `json:"-"`
}

// Catalogue returns a fresh inventory in stable ID order.
func Catalogue() []Scenario {
	return []Scenario{
		{
			ID:         "E2E-001",
			Name:       "Pre-issued identity enrols: Authenticate, TokenUpdate, Idle, no commands",
			Family:     "mdm",
			Regression: "TestE2E_EnrollIdle",
			Modes:      []string{"simulated"},
			Run:        enrollIdle,
		},
		{
			ID:         "E2E-002",
			Name:       "Three commands queued, delivered in order, acknowledged with typed responses",
			Family:     "mdm",
			Regression: "TestE2E_CommandsInOrder",
			Modes:      []string{"simulated"},
			Run:        commandsInOrder,
		},
		{
			ID:         "E2E-003",
			Name:       "Device answers NotNow, command is retried after backoff",
			Family:     "mdm",
			Regression: "TestE2E_NotNowBackoff",
			Modes:      []string{"simulated"},
			Run:        notNow,
		},
		{
			ID:         "E2E-004",
			Name:       "Device ErrorChain is available through the command result API",
			Family:     "mdm",
			Regression: "TestE2E_CommandError",
			Modes:      []string{"simulated"},
			Run:        commandError,
		},
		{
			ID:         "E2E-005",
			Settings:   map[string]string{"DM_ALLOW_REENROLL": "true"},
			Name:       "Re-enrollment with a new identity clears the pending command queue",
			Family:     "mdm",
			Regression: "TestE2E_Reenroll",
			Modes:      []string{"simulated"},
			Run:        reenroll,
		},
		{
			ID:         "E2E-006",
			Name:       "SCEP enrollment from an unsigned profile, push, command",
			Family:     "enrollment",
			Regression: "TestE2E_SCEPEnrollPush",
			Modes:      []string{"simulated"},
			Run:        scepPush,
		},
		{
			ID:         "E2E-007",
			Name:       "APNs 410 is returned as an invalid-token outcome",
			Family:     "apns",
			Regression: "TestE2E_PushInvalidToken",
			Modes:      []string{"simulated"},
			Run:        invalidToken,
		},
		{
			ID:         "E2E-008",
			Name:       "Assigned declarations reach the device and its DDM status is persisted",
			Family:     "ddm",
			Regression: "TestE2E_DDMRoundTrip",
			Modes:      []string{"simulated"},
			Run:        ddmRoundTrip,
		},
		{
			ID:         "E2E-009",
			Name:       "An activation predicate produces inactive declaration status",
			Family:     "ddm",
			Regression: "TestE2E_DDMPredicate",
			Modes:      []string{"simulated"},
			Run:        ddmPredicate,
		},
		{
			ID:         "E2E-010",
			Topology:   "split",
			Name:       "DDM declaration and status round trip across separate MDM and DDM roles with an authenticated proxy hop",
			Family:     "split",
			Regression: "TestE2E_DDMSplitDeployment",
			Modes:      []string{"simulated"},
			Run:        splitRoundTrip,
		},
		{
			ID:         "E2E-011",
			Name:       "DEP token import, profile configuration and device synchronization through the admin API",
			Family:     "dep",
			Regression: "TestE2E_DEPAssign",
			Modes:      []string{"simulated"},
			Run:        depAssign,
		},
		{
			ID:         "E2E-012",
			Name:       "Mac and iPhone service discovery and account-driven enrollment with apple-as-web",
			Family:     "accountdriven",
			Regression: "TestE2E_ServiceDiscovery",
			Modes:      []string{"simulated"},
			Run:        accountDriven,
		},
		{
			ID:         "E2E-013",
			Settings:   map[string]string{"DM_REQUIRE_USER_AUTH": "true"},
			Name:       "Digest user authentication, command isolation for two users, and device-only command rejection",
			Family:     "user",
			Regression: "TestE2E_UserChannel",
			Modes:      []string{"simulated"},
			Run:        userChannels,
		},
		{
			ID:         "E2E-014",
			Settings:   map[string]string{"DM_IDENTITY": "acme"},
			Name:       "ACME attested enrollment with replay, wrong-key, stale, missing and foreign-attestation rejection",
			Family:     "acme",
			Regression: "TestE2E_ACMEAttest",
			Modes:      []string{"simulated"},
			Run:        acmeEnroll,
		},
		{
			ID:         "E2E-016",
			Name:       "OTA profile service: phase 1 signed by the device certificate with the challenge, SCEP, phase 2 signed by the new identity, final profile enrolls",
			Family:     "ota",
			Regression: "TestE2E_OTAProfileService",
			Modes:      []string{"simulated"},
			Run:        otaEnroll,
		},
		{
			ID:         "E2E-017",
			Name:       "CheckOut and re-enrollment clear persisted DDM status",
			Family:     "ddm",
			Regression: "TestE2E_DDMCheckOutClears",
			Modes:      []string{"simulated"},
			Run:        ddmCheckout,
		},
		{
			ID:         "E2E-018",
			Name:       "ADE enrollment through the configured reference-server endpoint",
			Family:     "ade",
			Regression: "TestE2E_ADEWebViewAuth",
			Modes:      []string{"simulated"},
			Run:        adeEnroll,
		},
		{
			ID:         "E2E-019",
			Settings:   map[string]string{"DM_ACCOUNT_DRIVEN_METHOD": "apple-oauth2"},
			Name:       "Mac and iPhone account-driven enrollment with apple-oauth2 authorization code flow",
			Family:     "accountdriven",
			Regression: "TestE2E_AccountDrivenOAuth2",
			Modes:      []string{"simulated"},
			Run:        accountDriven,
		},
		{
			ID:         "E2E-020",
			Name:       "Shared iPad device and user commands reach their respective channels",
			Family:     "sharedipad",
			Regression: "TestE2E_SharedIPad",
			Modes:      []string{"simulated"},
			Run:        sharedIPad,
		},
		{
			ID:         "E2E-021",
			Name:       "ABM server listing, bounded device listing, assignment completion and unassignment",
			Family:     "abm",
			Regression: "TestE2E_ABMAssignDevices",
			Modes:      []string{"simulated"},
			Run:        abmAssign,
		},
		{
			ID:         "E2E-023",
			Settings:   map[string]string{"DM_IDENTITY": "acme"},
			Name:       "`DeviceInformation` with `DeviceAttestationNonce`: the returned `DevicePropertiesAttestation` chain verifies and its properties are read; the device returns its cached attestation for a repeated nonce; a tampered chain is refused",
			Family:     "attestation",
			Regression: "TestE2E_DeviceAttestation",
			Modes:      []string{"simulated"},
			Run:        deviceAttestation,
		},
		{
			ID:         "E2E-024",
			Name:       "Admin route discovery includes authorization actions and rejects an invalid credential",
			Family:     "admin",
			Regression: "TestE2E_AdminCLI",
			Modes:      []string{"simulated"},
			Run:        adminRoutes,
		},
		{
			ID:         "E2E-025",
			Settings:   map[string]string{"DM_RETURN_TO_SERVICE": "true"},
			Name:       "Return to service: a device escrows a bootstrap token, asks for its return-to-service configuration, and the response carries the escrowed token for the device to use during Return to Service",
			Family:     "returntoservice",
			Regression: "TestE2E_ReturnToService",
			Modes:      []string{"simulated"},
			Run:        returnEnabled,
		},
		{
			ID:         "E2E-026",
			Name:       "An unconfigured return-to-service policy reports Enabled false",
			Family:     "returntoservice",
			Regression: "TestE2E_ReturnToServiceDisabledByDefault",
			Modes:      []string{"simulated"},
			Run:        returnDisabled,
		},
		{
			ID:     "APP-001",
			Name:   "App alert accepted with certificate authentication",
			Family: "apppush",
			Modes:  []string{"simulated", "live"},
			Run:    appAlert,
		},
		{
			ID:     "APP-002",
			Name:   "App background push accepted with certificate authentication",
			Family: "apppush",
			Modes:  []string{"simulated", "live"},
			Run:    appBackground,
		},
		{
			ID:     "APP-003",
			Name:   "A renewed app certificate advances the credential version and supports another send",
			Family: "apppush",
			Modes:  []string{"simulated"},
			Run:    appRenewal,
		},
		{
			ID:     "LIVE-001",
			Name:   "Enrolled device acknowledges DeviceInformation after an APNs wake",
			Family: "mdm",
			Modes:  []string{"live"},
			Prerequisites: []string{
				"device ID",
				"completed enrollment and TokenUpdate",
				"MDM push identity",
			},
			Run: liveMDM,
		},
	}
}

// Result distinguishes unavailable prerequisites from an assertion failure.
//
//nolint:tagliatelle // Evidence JSON uses the administration PascalCase convention.
type Result struct {
	ID       string        `json:"ID"`
	Name     string        `json:"Name"`
	Mode     string        `json:"Mode"`
	Adapter  string        `json:"Adapter"`
	Revision string        `json:"Revision"`
	Status   string        `json:"Status"`
	Detail   string        `json:"Detail"`
	Started  time.Time     `json:"Started"`
	Duration time.Duration `json:"Duration"`
}

var ErrBlocked = errors.New("scenario prerequisites unavailable")

func Run(ctx context.Context, e *Environment, s Scenario, adapter, revision, device string) Result {
	r := Result{
		ID:       s.ID,
		Name:     s.Name,
		Mode:     e.Mode,
		Adapter:  adapter,
		Revision: revision,
		Started:  time.Now().UTC(),
		Status:   "passed",
	}
	supported := false
	for _, m := range s.Modes {
		if m == e.Mode {
			supported = true
		}
	}
	if s.Run == nil || !supported {
		r.Status = "unsupported"
		r.Detail = "scenario has no adapter for this mode; retained regression: " + s.Regression
		return r
	}
	var err error
	if len(s.Settings) > 0 || (s.Topology != "" && s.Topology != e.Topology) {
		dir, merr := os.MkdirTemp("", "dm-bench-scenario-")
		if merr != nil {
			err = merr
		} else {
			defer os.RemoveAll(dir)
			topology := s.Topology
			if topology == "" {
				topology = e.Topology
			}
			err = Init(dir, "simulated", "sqlite", topology, "127.0.0.1:0")
			if err == nil {
				var w *Workspace
				w, err = Load(dir)
				if err == nil {
					w.Settings = s.Settings
					var nested *Environment
					nested, err = Start(ctx, w, e.Binary, io.Discard)
					if err == nil {
						defer nested.Close()
						err = s.Run(ctx, nested, device)
					}
				}
			}
		}
	} else {
		err = s.Run(ctx, e, device)
	}
	r.Duration = time.Since(r.Started)
	if err != nil {
		r.Status = "failed"
		if errors.Is(err, ErrBlocked) {
			r.Status = "blocked"
		}
		r.Detail = err.Error()
	}
	return r
}

func Select(selector string) ([]Scenario, error) {
	var out []Scenario
	for _, s := range Catalogue() {
		if selector == "all" || selector == s.ID || selector == s.Family {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: unknown scenario or family %q", errOperation, selector)
	}
	return out, nil
}

// WriteReports writes private JSON evidence and a JUnit projection. Non-passes
// never become successes merely because the selected execution mode lacks a device.
func WriteReports(dir string, results []Result) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wrapError(err)
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "results.json"), append(b, '\n'), 0o600); err != nil {
		return wrapError(err)
	}
	type issue struct {
		Message string `xml:"message,attr"`
	}
	type testcase struct {
		Name    string `xml:"name,attr"`
		Class   string `xml:"classname,attr"`
		Time    string `xml:"time,attr"`
		Failure *issue `xml:"failure,omitempty"`
		Skipped *issue `xml:"skipped,omitempty"`
	}
	suite := struct {
		XMLName  xml.Name   `xml:"testsuite"`
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Skipped  int        `xml:"skipped,attr"`
		Cases    []testcase `xml:"testcase"`
	}{Tests: len(results)}
	for _, r := range results {
		c := testcase{
			Name:  r.ID + " " + r.Name,
			Class: r.Adapter + "." + r.Mode,
			Time:  fmt.Sprintf("%.3f", r.Duration.Seconds()),
		}
		switch r.Status {
		case "failed":
			c.Failure = &issue{r.Detail}
			suite.Failures++
		case "blocked", "unsupported":
			c.Skipped = &issue{r.Detail}
			suite.Skipped++
		}
		suite.Cases = append(suite.Cases, c)
	}
	b, err = xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return wrapError(err)
	}
	return wrapError(os.WriteFile(filepath.Join(dir, "junit.xml"), b, 0o600))
}

// Markdown is generated directly from executable metadata.
func Markdown() string {
	var b strings.Builder
	b.WriteString(
		"# Bench scenario catalogue\n\nGenerated by `dmctl bench list -format markdown`. Reserved IDs E2E-015 and E2E-022 are not implemented coverage. Names describe the shared workflow assertions; linked regressions retain their detailed component checks.\n\n| ID | Family | Modes | Scenario | Retained regression |\n|---|---|---|---|---|\n",
	)
	for _, s := range Catalogue() {
		fmt.Fprintf(
			&b,
			"| %s | %s | %s | %s | %s |\n",
			s.ID,
			s.Family,
			strings.Join(s.Modes, ", "),
			s.Name,
			s.Regression,
		)
	}
	return b.String()
}

// SelectMode makes all select the applicable scenarios; explicit IDs still
// report unsupported modes rather than being silently ignored.
func SelectMode(mode, selector string) ([]Scenario, error) {
	all, err := Select(selector)
	if err != nil || selector != "all" {
		return all, wrapError(err)
	}
	var out []Scenario
	for _, s := range all {
		for _, m := range s.Modes {
			if m == mode {
				out = append(out, s)
				break
			}
		}
	}
	return out, nil
}
