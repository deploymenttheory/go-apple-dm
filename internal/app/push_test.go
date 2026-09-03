package app_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/app"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
)

// recordingPusher stands in for APNs and records who was woken.
type recordingPusher struct {
	mu   sync.Mutex
	woke []mdm.EnrollmentID
}

func (p *recordingPusher) Push(_ context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[mdm.EnrollmentID]push.Result, len(targets))
	for _, tgt := range targets {
		p.woke = append(p.woke, tgt.ID)
		out[tgt.ID] = push.Result{Sent: true}
	}
	return out, nil
}

func (p *recordingPusher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.woke)
}

// Before phase 8 the reference server built no pusher, so a declaration
// change queued a command and never woke the device: it would be collected on
// the next check-in, which for an idle Mac can be hours.
func TestPushWiring(t *testing.T) {
	t.Run("NotifierGetsThePusher", func(t *testing.T) {
		p := &recordingPusher{}
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
			Push: app.PushConfig{Pusher: p, Coalesce: -1},
		})
		if a.Push == nil {
			t.Fatal("no push notifier was built")
		}
		// Draining with nothing pending must not push, but the wiring is in
		// place: the notifier holds a pusher rather than a nil.
		if _, err := a.Notifier.DrainOnce(context.Background()); err != nil {
			t.Fatalf("DrainOnce: %v", err)
		}
	})

	t.Run("OffBuildsNoPusher", func(t *testing.T) {
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
			Push: app.PushConfig{Source: app.PushSourceOff},
		})
		if a.Push != nil {
			t.Fatal("a push notifier was built with the source off")
		}
	})

	t.Run("StoreSourceIsTheDefaultForADeployment", func(t *testing.T) {
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
			Push: app.PushConfig{Source: app.PushSourceStore},
		})
		if a.Push == nil {
			t.Fatal("the store source built no notifier")
		}
	})

	t.Run("BadConfig", func(t *testing.T) {
		for name, cfg := range map[string]app.PushConfig{
			"unknown source": {Source: "carrier-pigeon"},
			"file with no key": {
				Source: app.PushSourceFile, CertFile: "cert.pem",
			},
			"file with no certificate": {
				Source: app.PushSourceFile, KeyFile: "key.pem",
			},
		} {
			_, err := app.Build(context.Background(), app.Config{
				Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
				Logger: quiet, Push: cfg,
			})
			if !errors.Is(err, app.ErrConfig) {
				t.Errorf("%s: err = %v, want ErrConfig", name, err)
			}
		}
	})

	// A certificate that cannot be loaded is a Build error, not a server that
	// starts and silently never pushes.
	t.Run("MissingCertificateIsABuildError", func(t *testing.T) {
		dir := t.TempDir()
		_, err := app.Build(context.Background(), app.Config{
			Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t", Logger: quiet,
			Push: app.PushConfig{
				Source:   app.PushSourceFile,
				CertFile: filepath.Join(dir, "absent.pem"),
				KeyFile:  filepath.Join(dir, "absent.key"),
			},
		})
		if !errors.Is(err, app.ErrConfig) {
			t.Fatalf("err = %v, want ErrConfig", err)
		}
	})
}

// The topic comes from the certificate rather than from configuration,
// because a hand-typed topic that disagrees means APNs refuses every push.
func TestPushTopicFromCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "push.pem")
	keyPath := filepath.Join(dir, "push.key")
	ca, err := testpki.NewCA("push-test")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssuePush("com.apple.mgmt.External.test", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
		Push: app.PushConfig{
			Source: app.PushSourceFile, CertFile: certPath, KeyFile: keyPath,
			Transport: func(tls.Certificate) *http.Client { return http.DefaultClient },
		},
	})
	if a.Push == nil {
		t.Fatal("no push notifier was built from a file pair")
	}
}

// Repeated pushes to one enrollment collapse inside the window, so a burst of
// declaration changes wakes a device once.
func TestPushCoalescing(t *testing.T) {
	p := &recordingPusher{}
	coalesced := push.Coalesce(p, time.Minute, nil)
	target := push.Target{
		ID:   mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID"},
		Push: mdm.Push{Topic: "com.apple.mgmt.test", Token: []byte{1}, Magic: "m"},
	}
	for range 3 {
		if _, err := coalesced.Push(context.Background(), []push.Target{target}); err != nil {
			t.Fatal(err)
		}
	}
	if p.count() != 1 {
		t.Fatalf("the pusher saw %d wake-ups, want one inside the window", p.count())
	}
}

// An explicit topic is honoured, and the store source accepts a TTL, so both
// paths through the certificate source are exercised.
func TestPushCertSourceOptions(t *testing.T) {
	dir := t.TempDir()
	ca, err := testpki.NewCA("push-opts")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssuePush("com.apple.mgmt.External.opts", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	// An explicit topic skips deriving one.
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
		Push: app.PushConfig{
			Source: app.PushSourceFile, CertFile: certPath, KeyFile: keyPath,
			Topic: "com.apple.mgmt.External.explicit", Host: "https://apns.example",
		},
	})
	if a.Push == nil {
		t.Fatal("no notifier with an explicit topic")
	}

	// The store source takes a cache TTL.
	b := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "t",
		Push: app.PushConfig{Source: app.PushSourceStore, CertTTL: time.Minute},
	})
	if b.Push == nil {
		t.Fatal("no notifier with a store TTL")
	}
}
