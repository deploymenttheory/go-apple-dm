//go:build e2e

package e2e

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/v3/internal/app"
	"github.com/deploymenttheory/go-apple-dm/v3/internal/dmctl"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	adminsql "github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
	auditinmem "github.com/deploymenttheory/go-apple-dm/v3/server/audit/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

// adminHarness is the reference server with a real principal store on its own
// database, driven the way an operator drives it: through dmctl.
type adminHarness struct {
	app     *app.App
	url     string
	store   adminauth.Store
	manager *adminauth.Manager
	trail   audit.Store
	woken   *countingPusher
}

// countingPusher stands in for APNs and records who was woken.
type countingPusher struct{ woke []mdm.EnrollmentID }

func (p *countingPusher) Push(_ context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	out := make(map[mdm.EnrollmentID]push.Result, len(targets))
	for _, tgt := range targets {
		p.woke = append(p.woke, tgt.ID)
		out[tgt.ID] = push.Result{Outcome: push.OutcomeSent}
	}
	return out, nil
}

func newAdminHarness(t *testing.T) *adminHarness {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "admin.db")
	trail := auditinmem.New()
	pusher := &countingPusher{}
	a, err := app.Build(context.Background(), app.Config{
		Role: app.RoleAll, Storage: "sqlite", DSN: dsn,
		StorageKeys:       []string{"e2e"},
		Secrets:           secrets.Static{"e2e": []byte("0123456789abcdef0123456789abcdef")},
		AdminStoreEnabled: true,
		AdminToken:        "break-glass",
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Push:              app.PushConfig{Pusher: pusher, Coalesce: -1},
		Sinks:             app.SinkConfig{AuditStore: trail},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)

	// Reach the same rows the server opened for itself.
	db, err := sqlite.Open(context.Background(), dsn, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := adminsql.Open(context.Background(), db.DB(), sqlite.Dialect, adminsql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	return &adminHarness{app: a, url: srv.URL, store: st, manager: m, trail: trail, woken: pusher}
}

// ctl runs dmctl against the harness as the given token, the way an operator
// would.
func (h *adminHarness) ctl(t *testing.T, token, stdin string, args ...string) (string, error) {
	t.Helper()
	env := map[string]string{
		"DMCTL_CONFIG": filepath.Join(t.TempDir(), "absent.json"),
		"DMCTL_SERVER": h.url,
		"DMCTL_TOKEN":  token,
	}
	var out, errOut strings.Builder
	err := dmctl.Run(context.Background(), args,
		func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errOut)
	if err != nil {
		return out.String(), fmt.Errorf("dmctl %v: %w (stderr: %s)", args, err, errOut.String())
	}
	return out.String(), nil
}

func (h *adminHarness) mint(t *testing.T, p adminauth.Principal) string {
	t.Helper()
	_, tok, err := h.manager.CreatePrincipal(context.Background(), adminauth.Root, p, time.Time{})
	if err != nil {
		t.Fatalf("CreatePrincipal %s: %v", p.Name, err)
	}
	return string(tok)
}

// E2E-024: dmctl drives every admin route the server serves, a read-only
// principal is refused and the refusal is audited, a rotated token is
// rejected, and the server wakes a device.
//
// The route walk is the part that keeps working: it reads GET /routes and
// asserts the CLI can reach each one, so a route added later with no CLI path
// fails here rather than being discovered by an operator.
func TestE2E_AdminCLI(t *testing.T) {
	h := newAdminHarness(t)

	// Bootstrap with the break-glass credential, which is the documented way
	// in: an empty principal store authenticates nobody.
	root := h.mint(t, adminauth.Principal{Name: "ops", Root: true})
	if _, err := h.manager.PutPolicy(context.Background(), adminauth.Root, adminauth.Policy{
		Name:   "ops",
		Source: `permit (principal == MDM::Principal::"ops", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}
	reader := h.mint(t, adminauth.Principal{Name: "reader"})

	// An enrollment to act on.
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-E2E"}
	err := h.app.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{
		Enrollment: storage.Enrollment{
			ID: dev, Enabled: true,
			Push:   mdm.Push{Topic: "com.apple.mgmt.External.simulator", Token: []byte("tok"), Magic: "magic"},
			Device: storage.DeviceInfo{SerialNumber: "S-E2E", ProductName: "Mac15,3", OSVersion: "26.0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("DrivesEveryAdminRoute", func(t *testing.T) {
		out, err := h.ctl(t, root, "", "-output", "json", "routes")
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Routes []struct{ Method, Pattern, Action, Family string }
		}
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Routes) == 0 {
			t.Fatal("the server advertised no routes")
		}

		// Scratch targets absorb the destructive routes. The principal
		// routes include revoke and delete, so walking them as the caller's
		// own identity would revoke the credential doing the walking; the
		// enrollment routes include disable, which would silently break the
		// push assertion in a later subtest.
		h.mint(t, adminauth.Principal{Name: scratchPrincipal})
		scratch := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: scratchEnrollment}
		err = h.app.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{
			Enrollment: storage.Enrollment{
				ID: scratch, Enabled: true,
				Push: mdm.Push{Topic: "com.apple.mgmt.External.simulator", Token: []byte("tok"), Magic: "magic"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		for _, rt := range body.Routes {
			path := concretePath(rt.Pattern)
			method := rt.Method
			if method == "" {
				method = "GET"
			}
			// The route table is generated from the mounted mux, so every
			// entry is served by construction. What is being asserted is
			// that the CLI can form and send a request for each one: a
			// route the CLI cannot express fails with a usage error, which
			// is the regression this guards. A status from the server means
			// the route was reached, whatever it says.
			//
			// The walk runs as break-glass so that revoking or deleting the
			// scratch principal cannot invalidate the walker's credential.
			_, err := h.ctl(t, "break-glass", "{}", "api", method, path)
			if err != nil && errors.Is(err, dmctl.ErrUsage) {
				t.Errorf("%s %s cannot be expressed by the CLI: %v", method, rt.Pattern, err)
			}
			if err != nil && strings.Contains(err.Error(), "connection refused") {
				t.Errorf("%s %s was never sent: %v", method, rt.Pattern, err)
			}
		}
	})

	t.Run("TypedVerbsCoverTheModelledFamilies", func(t *testing.T) {
		for _, args := range [][]string{
			{"status"}, {"routes"}, {"actions"},
			{"principals", "list"}, {"policies", "list"},
			{"enrollments", "list"},
			{"enrollments", "get", "device", "UDID-E2E"},
			{"commands", "list", "device", "UDID-E2E"},
			{"pushcerts", "list"},
			{"audit", "list"},
			{"export"},
		} {
			if _, err := h.ctl(t, root, "", args...); err != nil {
				t.Errorf("%v: %v", args, err)
			}
		}
	})

	// A rotated credential invalidates the previous value immediately, which
	// is the property a single shared secret cannot offer at all.
	t.Run("RotatedTokenIsRejected", func(t *testing.T) {
		rotating := h.mint(t, adminauth.Principal{Name: "rotating", Root: true})
		if _, err := h.ctl(t, rotating, "", "status"); err != nil {
			t.Fatalf("the fresh token was refused: %v", err)
		}
		if _, _, err := h.manager.Rotate(context.Background(), adminauth.Root, "rotating", time.Time{}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.ctl(t, rotating, "", "status"); err == nil {
			t.Fatal("the superseded token still works")
		}
	})

	// The reference server wakes a device: dmctl asks, APNs is told.
	t.Run("WakesADevice", func(t *testing.T) {
		before := len(h.woken.woke)
		if _, err := h.ctl(t, root, "", "push", "device", "UDID-E2E"); err != nil {
			t.Fatalf("push: %v", err)
		}
		if len(h.woken.woke) == before {
			t.Fatal("the push route woke nobody")
		}
		if h.woken.woke[len(h.woken.woke)-1].ID != "UDID-E2E" {
			t.Fatalf("woke %v", h.woken.woke)
		}
	})

	t.Run("ReadOnlyPrincipalIsRefusedAndAudited", func(t *testing.T) {
		if _, err := h.ctl(t, reader, "", "enrollments", "disable", "device", "UDID-E2E"); err == nil {
			t.Fatal("a principal with no policy was allowed to disable an enrollment")
		}
		if err := h.app.Close(); err != nil {
			t.Fatal(err)
		}
		res, err := h.trail.List(context.Background(), audit.Query{Type: "admin-denied"}, audit.Page{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, rec := range res.Items {
			if rec.Actor == "reader" && rec.Fields["Action"] == app.ActionDisableEnrollment {
				found = true
			}
		}
		if !found {
			t.Fatalf("the refusal was not audited: %+v", res.Items)
		}
	})
}

// scratchPrincipal is the name the principal routes are exercised against,
// so the walk never targets the credential it is using.
const (
	scratchPrincipal  = "route-walk-scratch"
	scratchEnrollment = "UDID-ROUTE-WALK"
)

// concretePath turns a mux pattern into a path that can actually be called,
// filling the wildcards with the enrollment under test.
func concretePath(pattern string) string {
	path := pattern
	for from, to := range map[string]string{
		"{channel}": "device",
		"{id}":      scratchEnrollment,
		"{set}":     "e2e-set",
		"{name}":    scratchPrincipal,
	} {
		path = strings.ReplaceAll(path, from, to)
	}
	// A trailing subtree pattern is called at its root.
	return strings.TrimSuffix(path, "/")
}
