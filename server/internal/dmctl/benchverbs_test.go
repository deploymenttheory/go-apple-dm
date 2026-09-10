package dmctl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchOfflineCommands(t *testing.T) {
	env := noConfig(t)
	dir := t.TempDir()
	if _, _, err := run(
		t,
		env,
		"bench",
		"init",
		"-workspace",
		dir,
		"-listen",
		"127.0.0.1:0",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "bench", "init", "-workspace", dir); err == nil {
		t.Fatal("existing workspace replaced")
	}

	if _, _, err := run(
		t,
		env,
		"bench",
		"enrollment-preflight",
		"-workspace",
		dir,
		"-identity",
		"scep",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(
		t,
		env,
		"bench",
		"enrollment-preflight",
		"-workspace",
		dir,
		"-identity",
		"invalid",
	); err == nil {
		t.Fatal("invalid identity passed preflight")
	}
	if _, _, err := run(t, env, "bench", "trust", "-workspace", dir); err == nil {
		t.Fatal("missing trust destination accepted")
	}
	trust := filepath.Join(dir, "trust.mobileconfig")
	if _, _, err := run(t, env, "bench", "trust", "-workspace", dir, "-file", trust); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "bench", "trust", "-workspace", dir, "-file", trust); err == nil {
		t.Fatal("trust profile overwritten")
	}
	out, _, err := run(t, env, "bench", "doctor", "-workspace", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LivePrerequisites") {
		t.Fatal("prerequisites omitted")
	}
	key, err := os.ReadFile(filepath.Join(dir, "mdm", "storage-key"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, string(key)) {
		t.Fatal("doctor leaked a credential")
	}
	out, _, err = run(t, env, "bench", "list", "-format", "markdown")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "APP-001") {
		t.Fatal("app scenario omitted")
	}
	for _, sub := range []string{"init", "doctor", "list", "up", "run", "profile", "status", "down"} {
		if _, _, err = run(t, env, "bench", sub, "-h"); err != nil {
			t.Errorf("%s help: %v", sub, err)
		}
	}
	for _, sub := range []string{"list", "put", "send"} {
		if _, _, err = run(t, env, "apppush", sub, "-h"); err != nil {
			t.Errorf("%s help: %v", sub, err)
		}
	}
}
