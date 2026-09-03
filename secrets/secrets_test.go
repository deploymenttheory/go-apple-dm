package secrets_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/secrets"
)

func TestSecretsRedacted(t *testing.T) {
	t.Parallel()
	s := secrets.New([]byte("hunter2"))
	for name, got := range map[string]string{
		"String":   s.String(),
		"Sprint":   fmt.Sprint(s),
		"Sprintf":  fmt.Sprintf("%v %s %q %x %+v %#v", s, s, s, s, s, s),
		"GoString": s.GoString(),
		"struct":   fmt.Sprintf("%+v", struct{ Key secrets.Secret }{s}),
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("%s leaked: %s", name, got)
		}
	}
	j, err := json.Marshal(map[string]any{"key": s, "nested": struct{ S secrets.Secret }{s}})
	if err != nil || strings.Contains(string(j), "hunter2") || !strings.Contains(string(j), secrets.Redacted) {
		t.Fatalf("json: %s %v", j, err)
	}
	txt, _ := s.MarshalText()
	if string(txt) != secrets.Redacted {
		t.Fatal("text")
	}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("push", "key", s, "any", any(s))
	if strings.Contains(sb.String(), "hunter2") {
		t.Fatalf("slog leaked: %s", sb.String())
	}
	if string(s.Bytes()) != "hunter2" || s.IsZero() || !secrets.New(nil).IsZero() {
		t.Fatal("Bytes/IsZero")
	}
	b := s.Bytes()
	b[0] = 'X'
	if string(s.Bytes()) != "hunter2" {
		t.Fatal("Bytes aliased the value")
	}
	if !s.Equal(secrets.New([]byte("hunter2"))) || s.Equal(secrets.New([]byte("hunter3"))) {
		t.Fatal("Equal")
	}
}

func TestProviders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := secrets.Static{"apns.key": []byte("k")}
	if v, err := st.Get(ctx, "apns.key"); err != nil || string(v.Bytes()) != "k" {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "missing"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("static missing")
	}

	env := secrets.Env{Prefix: "MDM_", Lookup: func(k string) (string, bool) {
		return map[string]string{"MDM_APNS_KEY": "e", "MDM_EMPTY": ""}[k], k == "MDM_APNS_KEY" || k == "MDM_EMPTY"
	}}
	if env.Key("apns.key") != "MDM_APNS_KEY" || env.Key("dep-token/x") != "MDM_DEP_TOKEN_X" {
		t.Fatal(env.Key("apns.key"))
	}
	if v, err := env.Get(ctx, "apns.key"); err != nil || string(v.Bytes()) != "e" {
		t.Fatal(err)
	}
	for _, name := range []string{"empty", "missing"} {
		if _, err := env.Get(ctx, name); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("env %s: %v", name, err)
		}
	}
	if _, err := (secrets.Env{Prefix: "GO_APPLE_MDM_TEST_"}).Get(ctx, "definitely.unset"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("os env default lookup: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "apns.key"), []byte("file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big"), make([]byte, 1<<20+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := secrets.NewDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := d.Get(ctx, "apns.key"); err != nil || string(v.Bytes()) != "file" {
		t.Fatalf("dir: %v", err)
	}
	if _, err := d.Get(ctx, "missing"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("dir missing")
	}
	if _, err := d.Get(ctx, "big"); err == nil || errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("big: %v", err)
	}
	if _, err := d.Get(ctx, "sub"); err == nil || errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("directory as secret: %v", err)
	}
	for _, name := range []string{"", ".", "..", "../etc/passwd", `a\b`} {
		if _, err := d.Get(ctx, name); !errors.Is(err, secrets.ErrName) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get(ctx, "apns.key"); err == nil || errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("closed dir: %v", err)
	}
	if _, err := secrets.NewDir(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing dir")
	}

	chain := secrets.Chain{st, env, secrets.Static{"only.chain": []byte("c")}}
	if v, err := chain.Get(ctx, "only.chain"); err != nil || string(v.Bytes()) != "c" {
		t.Fatal(err)
	}
	if v, err := chain.Get(ctx, "apns.key"); err != nil || string(v.Bytes()) != "k" {
		t.Fatal("chain order")
	}
	if _, err := chain.Get(ctx, "missing"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("chain missing")
	}
	broken := secrets.Chain{failing{}, st}
	if _, err := broken.Get(ctx, "apns.key"); err == nil || errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("chain must stop on hard errors: %v", err)
	}
}

type failing struct{}

func (failing) Get(context.Context, string) (secrets.Secret, error) {
	return secrets.Secret{}, errors.New("vault down")
}
