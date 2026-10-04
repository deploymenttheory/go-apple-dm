package dmctl_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplicationPackageCLI checks every package operation's method, endpoint,
// optimistic revision, streamed body and explicit MDM wake outcome.
func TestApplicationPackageCLI(t *testing.T) {
	dir := t.TempDir()
	metadata := filepath.Join(dir, "metadata.json")
	installer := filepath.Join(dir, "bench.pkg")
	bulk := filepath.Join(dir, "delete.json")
	for path, data := range map[string]string{metadata: `{"packageName":"Bench","fileName":"bench.pkg"}`, installer: "installer bytes", bulk: `{"packages":[{"id":"pkg","revision":"rev"}]}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name               string
		args               []string
		method, path, body string
		status             int
	}{
		{"create", []string{"create", "-file", metadata}, "POST", "/application-packages", "packageName", 201},
		{"update", []string{"update", "pkg", "-revision", "rev", "-file", metadata}, "PUT", "/application-packages/pkg", "packageName", 200},
		{"get", []string{"get", "pkg"}, "GET", "/application-packages/pkg", "", 200},
		{"list", []string{"list"}, "GET", "/application-packages", "", 200},
		{"delete", []string{"delete", "pkg", "-revision", "rev"}, "DELETE", "/application-packages/pkg", "", 204},
		{"bulk", []string{"delete-multiple", "-file", bulk}, "POST", "/application-packages/delete-multiple", "packages", 200},
		{"upload", []string{"upload", "pkg", "-revision", "rev", "-backend", "local", "-file", installer}, "POST", "/application-packages/pkg/upload", "installer bytes", 200},
		{"file source", []string{"import", "pkg", "-revision", "rev", "-backend", "aws", "-kind", "file", "-source", "bench.pkg"}, "POST", "/application-packages/pkg/source", "bench.pkg", 200},
		{"https source", []string{"import", "pkg", "-revision", "rev", "-backend", "azure", "-kind", "https", "-source", "https://example.test/pkg"}, "POST", "/application-packages/pkg/source", "https://example.test/pkg", 200},
		{"manifest", []string{"manifest-set", "pkg", "-revision", "rev", "-file", installer}, "POST", "/application-packages/pkg/manifest", "installer bytes", 200},
		{"remove manifest", []string{"manifest-delete", "pkg", "-revision", "rev"}, "DELETE", "/application-packages/pkg/manifest", "", 200},
		{"note", []string{"note", "pkg", "-revision", "rev", "-note", "reviewed"}, "POST", "/application-packages/pkg/history", "reviewed", 200},
		{"history", []string{"history", "pkg"}, "GET", "/application-packages/pkg/history", "", 200},
		{"export", []string{"export", "-format", "csv", "-fields", "id,packageName"}, "GET", "/application-packages/export", "", 200},
		{"history export", []string{"history-export", "pkg", "-cursor", "page2"}, "GET", "/application-packages/pkg/history/export", "", 200},
		{"download", []string{"download", "pkg", "-content-revision", "content"}, "GET", "/application-packages/pkg/revisions/content/content", "", 200},
		{"mdm", []string{"deliver", "pkg", "device", "-content-revision", "content"}, "POST", "/enrollments/device/device/application-packages", "mdm", 202},
		{"ddm", []string{"deliver", "pkg", "device", "-content-revision", "content", "-method", "ddm"}, "POST", "/enrollments/device/device/application-packages", "ddm", 202},
		{"revoke", []string{"revoke", "grant", "device"}, "DELETE", "/enrollments/device/device/application-packages/grants/grant", "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer operator" {
					t.Error("missing authentication")
				}
				if strings.HasSuffix(r.URL.Path, "/push") {
					if tc.name != "mdm" || r.Method != "POST" {
						t.Error("unexpected wake")
					}
					_, _ = io.WriteString(w, `{"Sent":true,"Outcome":"accepted"}`)
					return
				}
				if r.Method != tc.method || r.URL.Path != "/admin/v1"+tc.path {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if !strings.Contains(string(body), tc.body) {
					t.Error("incorrect request body")
				}
				for i, arg := range tc.args {
					if arg == "-revision" && r.Header.Get("If-Match") != `"`+tc.args[i+1]+`"` {
						t.Error("missing revision precondition")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.name == "export" || tc.name == "history export" {
					w.Header().Set("X-Next-Cursor", "page3")
				}
				w.WriteHeader(tc.status)
				if tc.status != 204 {
					if tc.name == "list" || tc.name == "history" {
						_, _ = io.WriteString(w, `{"Items":[{"id":"pkg","sequence":1,"action":"create"}]}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"pkg"}`)
					}
				}
			}))
			defer srv.Close()
			args := append([]string{"-server", srv.URL, "-token", "operator", "application-packages"}, tc.args...)
			_, stderr, err := run(t, noConfig(t), args...)
			if err != nil {
				t.Fatal(err, stderr)
			}
			want := 1
			if tc.name == "mdm" {
				want = 2
			}
			if calls != want {
				t.Fatal("wrong request count", calls)
			}
			if (tc.name == "export" || tc.name == "history export") && !strings.Contains(stderr, "page3") {
				t.Fatal("lost export pagination")
			}
		})
	}
}

// TestApplicationPackageCLIRejectsInvalidInput ensures malformed local requests
// never reach the API, including accidental unversioned destructive operations.
func TestApplicationPackageCLIRejectsInvalidInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached API") }))
	defer srv.Close()
	for _, args := range [][]string{
		{},
		{"unknown"},
		{"list", "-unknown"},
		{"create", "extra"},
		{"list", "extra"},
		{"get"},
		{"get", "a", "b"},
		{"delete", "pkg"},
		{"delete", "pkg", "-revision", "bad/rev"},
		{"create", "-file", "/missing/metadata"},
		{"upload", "pkg", "-revision", "rev"},
		{"upload", "pkg", "-revision", "rev", "-backend", "local", "-sha256", "invalid"},
		{"upload", "pkg", "-revision", "rev", "-backend", "local", "-file", "/missing/installer"},
		{"manifest-set", "pkg", "-revision", "rev"},
		{"import", "pkg", "-revision", "rev"},
		{"note", "pkg", "-revision", "rev"},
		{"download", "pkg"},
		{"deliver", "pkg", "device"},
		{"revoke", "grant"},
		{"export", "-format", "xml"},
		{"delete-multiple", "extra"},
		{"delete-multiple", "-file", "/missing/delete"},
	} {
		full := append([]string{"-server", srv.URL, "-token", "operator", "application-packages"}, args...)
		if _, _, err := runWithStdin(t, noConfig(t), "invalid", full...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

// TestApplicationPackageWakeFailure distinguishes queued work from accepted APNs delivery.
func TestApplicationPackageWakeFailure(t *testing.T) {
	for _, response := range []string{`{"Sent":false,"Outcome":"skipped"}`, `broken`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/push") {
				_, _ = io.WriteString(w, response)
				return
			}
			w.WriteHeader(202)
			_, _ = io.WriteString(w, `{"Status":"queued"}`)
		}))
		_, _, err := run(t, noConfig(t), "-server", srv.URL, "-token", "operator", "application-packages", "deliver", "pkg", "device", "-content-revision", "content")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "package queued;") {
			t.Fatal("wake failure concealed", err)
		}
	}
}

// TestApplicationPackageBulkInputAndAPIFailures preserves local validation and
// surfaces remote rejection without claiming metadata or delivery succeeded.
func TestApplicationPackageBulkInputAndAPIFailures(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"packages":[{"id":"pkg","revision":"rev"}]} {}`, `{"packages":[{"id":"bad/id","revision":"rev"}]}`, strings.Repeat(" ", (1<<20)+1)} {
		_, _, err := runWithStdin(t, noConfig(t), body, "application-packages", "delete-multiple")
		if err == nil {
			t.Fatal("invalid deletion body accepted")
		}
	}
	for _, args := range [][]string{{"unknown", "pkg"}, {"upload", "pkg", "extra", "-revision", "rev", "-backend", "local"}, {"update", "pkg", "-revision", "rev"}, {"list", "-limit", "bad"}} {
		if _, _, err := runWithStdin(t, noConfig(t), "invalid", append([]string{"application-packages"}, args...)...); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/application-packages") && strings.Contains(r.URL.Path, "/enrollments/") {
			w.WriteHeader(202)
			_, _ = io.WriteString(w, `{"Status":"queued"}`)
			return
		}
		http.Error(w, "rejected", 403)
	}))
	defer srv.Close()
	for _, args := range [][]string{{"get", "pkg"}, {"deliver", "pkg", "device", "-content-revision", "content"}} {
		base := []string{"-server", srv.URL, "-token", "operator", "application-packages"}
		if _, _, err := run(t, noConfig(t), append(base, args...)...); err == nil {
			t.Fatal("API failure concealed")
		}
	}
	if _, _, err := run(t, noConfig(t), "-server", "://invalid", "-token", "operator", "application-packages", "get", "pkg"); err == nil {
		t.Fatal("invalid server accepted")
	}
}
