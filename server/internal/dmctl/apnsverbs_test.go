package dmctl_test

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestOfflineAPNSCommands(t *testing.T) {
	t.Parallel()
	env := noConfig(t)
	for _, args := range [][]string{{"apns", "inspect", "-h"}, {"apns", "check", "-h"}, {"apns", "send", "-h"}, {"pushcerts", "csr", "-h"}, {"pushcerts", "sign", "-h"}} {
		if _, _, err := run(t, env, args...); err != nil {
			t.Fatalf("help %v: %v", args, err)
		}
	}
	dir := t.TempDir()
	ca, _ := testpki.NewCA("CLI")
	id, _ := ca.IssuePush("com.example.app", time.Now().Add(-time.Hour))
	tmpl := *id.Cert
	tmpl.Subject.ExtraNames = tmpl.Subject.Names
	tmpl.ExtraExtensions = []pkix.Extension{
		{Id: asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 3, 2}, Value: []byte{5, 0}},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, ca.Cert, id.Key.Public(), ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "app.der"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, der, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"inspect", "check"} {
		out, _, err := run(t, env, "apns", sub, "-cert", certPath, "-key", keyPath)
		if err != nil || !strings.Contains(out, "com.example.app") ||
			strings.Contains(out, "PRIVATE KEY") {
			t.Fatalf("%s: %s %v", sub, out, err)
		}
	}
	canonical := filepath.Join(dir, "app.pem")
	if _, _, err := run(
		t,
		env,
		"apns",
		"inspect",
		"-cert",
		certPath,
		"-cert-out",
		canonical,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(
		t,
		env,
		"apns",
		"inspect",
		"-cert",
		certPath,
		"-cert-out",
		canonical,
	); err == nil {
		t.Fatal("overwrote certificate")
	}
	for _, args := range [][]string{
		{"apns", "check", "-cert", certPath},
		{"apns", "check", "-kind", "mdm", "-cert", certPath, "-key", keyPath},
		{"apns", "check", "-topic", "com.other", "-cert", certPath, "-key", keyPath},
		{"apns", "send", "-cert", certPath, "-key", keyPath, "-token-file", "missing", "-payload-file", "missing"},
	} {
		if _, _, err := run(t, env, args...); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	registration := filepath.Join(dir, "registration.json")
	if err := os.WriteFile(
		registration,
		[]byte(`{"token":"ff","topic":"com.example.app","environment":"production"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(
		t,
		env,
		"apns",
		"send",
		"-cert",
		certPath,
		"-key",
		keyPath,
		"-environment",
		"development",
		"-token-file",
		registration,
		"-payload-file",
		"missing",
	); !errors.Is(
		err,
		dmctl.ErrUsage,
	) {
		t.Fatalf("environment mismatch: %v", err)
	}
}

func TestCSRCommandPreservesKeys(t *testing.T) {
	t.Parallel()
	env := noConfig(t)
	dir := t.TempDir()
	key, csr := filepath.Join(dir, "key.pem"), filepath.Join(dir, "request.csr")
	args := []string{"pushcerts", "csr", "-cn", "customer", "-key-out", key, "-csr-out", csr}
	out, _, err := run(t, env, args...)
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]string
	if err := json.Unmarshal([]byte(out), &paths); err != nil || paths["keyFile"] != key {
		t.Fatal("output paths")
	}
	before, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, args...); err == nil {
		t.Fatal("overwrote key")
	}
	after, err := os.ReadFile(key)
	if err != nil || string(before) != string(after) {
		t.Fatal("key changed")
	}
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("key permissions")
	}
}
