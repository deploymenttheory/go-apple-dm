package bench

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
)

func TestScenarioRejectsIncorrectDeviceBehavior(t *testing.T) {
	for _, name := range []string{"command missing", "wake without command", "user command missing", "shared device command missing", "shared user command missing", "unauthenticated TokenUpdate accepted", "malformed challenge", "invalid digest challenge", "default erasure enabled", "escrow lost"} {
		t.Run(name, func(t *testing.T) {
			id := "E2E-002"
			switch name {
			case "wake without command":
				id = "E2E-006"
			case "user command missing",
				"unauthenticated TokenUpdate accepted",
				"malformed challenge",
				"invalid digest challenge":
				id = "E2E-013"
			case "shared device command missing", "shared user command missing":
				id = "E2E-020"
			case "default erasure enabled":
				id = "E2E-026"
			case "escrow lost":
				id = "E2E-025"
			}
			selected, err := Select(id)
			if err != nil {
				t.Fatal(err)
			}
			s := selected[0]
			w := testWorkspace(t, "simulated")
			w.Settings = s.Settings
			e, err := Start(t.Context(), w, "", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			base := e.Client.Transport
			hit := false
			e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == "PUT" && r.URL.Path == "/mdm" {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					r.Body.Close()
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var message map[string]any
					if err := plist.Unmarshal(raw, &message); err != nil {
						return nil, err
					}
					user, _ := message["UserID"].(string)
					kind, _ := message["MessageType"].(string)
					status, _ := message["Status"].(string)
					body := []byte(nil)
					match := false
					switch name {
					case "command missing", "wake without command":
						match = status == "Idle"
					case "user command missing", "shared user command missing":
						match = status == "Idle" && user != ""
					case "shared device command missing":
						match = status == "Idle" && user == ""
					case "unauthenticated TokenUpdate accepted":
						match = kind == "TokenUpdate" && user != ""
					case "malformed challenge":
						match = kind == "UserAuthenticate"
						body = []byte("malformed")
					case "invalid digest challenge":
						match = kind == "UserAuthenticate"
						body = []byte(
							`<?xml version="1.0"?><plist version="1.0"><dict><key>DigestChallenge</key><string>invalid</string></dict></plist>`,
						)
					case "default erasure enabled", "escrow lost":
						match = kind == "ReturnToService"
						body = []byte(
							`<?xml version="1.0"?><plist version="1.0"><dict><key>ReturnToService</key><dict><key>Enabled</key><true/></dict></dict></plist>`,
						)
					}
					if match {
						hit = true
						return &http.Response{
							StatusCode: 200,
							Header:     make(http.Header),
							Body:       io.NopCloser(bytes.NewReader(body)),
							Request:    r,
						}, nil
					}
				}
				return base.RoundTrip(r)
			})
			if err := s.Run(t.Context(), e, ""); err == nil {
				t.Fatal("incorrect device behavior passed")
			}
			if !hit {
				t.Fatal("device assertion was not reached")
			}
			e.Client.Transport = base
		})
	}
}

func TestBenchFailsWhenReadinessNeverArrives(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }),
	)
	defer srv.Close()
	e := &Environment{
		Client:   srv.Client(),
		errs:     make(chan error),
		Instance: Instance{URL: srv.URL},
	}
	if err := e.ready(
		t.Context(),
		srv.URL,
	); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Fatalf("readiness deadline: %v", err)
	}
	destination := t.TempDir() + "/profile.mobileconfig"
	if err := Profile(t.Context(), e, "test", destination); err == nil {
		t.Fatal("failed profile request accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed request wrote profile")
	}
}

func TestBenchRejectsMissingScenarioScratchDirectory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/absent")
	result := Run(
		t.Context(),
		&Environment{Instance: Instance{Mode: "simulated", Topology: "all"}},
		Scenario{
			Modes:    []string{"simulated"},
			Settings: map[string]string{"DM_ALLOW_REENROLL": "true"},
			Run:      enrollIdle,
		},
		"test",
		"revision",
		"",
	)
	if result.Status != "failed" {
		t.Fatalf("unavailable scratch directory: %+v", result)
	}
}

func TestFixedListenerAndUnavailableScriptControl(t *testing.T) {
	t.Parallel()
	if addr, err := address(t.Context(), "127.0.0.1:8443"); err != nil || addr != "127.0.0.1:8443" {
		t.Fatalf("fixed listener: %s %v", addr, err)
	}
	w := testWorkspace(t, "simulated")
	e, err := Start(t.Context(), w, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	attached := &Environment{Instance: e.Instance, Workspace: w, Client: e.Client, Token: e.Token}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := invalidToken(ctx, attached, ""); err == nil {
		t.Fatal("scenario passed without scripting APNs outcome")
	}
}
