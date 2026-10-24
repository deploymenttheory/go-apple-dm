package lab

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestAuthorizationHandoffFailsClosed checks failed credential reads, refused or
// malformed bootstrap responses and lost credential persistence never install a
// policy or silently replace a retained administrator credential.
func TestAuthorizationHandoffFailsClosed(t *testing.T) {
	for _, failure := range []string{"unreadable credential", "bootstrap refused", "malformed bootstrap", "credential collision"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			w := &Workspace{Directory: t.TempDir()}
			if err := os.MkdirAll(w.path("mdm"), 0o700); err != nil {
				t.Fatal(err)
			}
			const bootstrap = "private-bootstrap-fixture"
			const existing = "private-retained-fixture"
			if err := os.WriteFile(w.path("mdm", "admin-token"), []byte(bootstrap), 0o600); err != nil {
				t.Fatal(err)
			}
			if failure == "unreadable credential" {
				if err := os.Mkdir(w.path("mdm", "admin-credential"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if failure == "credential collision" {
				if err := os.WriteFile(w.path("mdm", "admin-credential"), []byte(existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch r.URL.Path {
				case "/admin/v1/auth/me":
					expected := bootstrap
					if failure == "credential collision" {
						expected = existing
					}
					if r.Header.Get("Authorization") != "Bearer "+expected {
						t.Error("administrator probe used the wrong credential source")
					}
					rw.WriteHeader(http.StatusUnauthorized)
				case "/admin/v1/auth/bootstrap":
					if r.Header.Get("Authorization") != "Bearer "+bootstrap {
						t.Error("bootstrap lost its one-time credential")
					}
					switch failure {
					case "bootstrap refused":
						rw.WriteHeader(http.StatusForbidden)
					case "malformed bootstrap":
						_, _ = io.WriteString(rw, `{"Token":`)
					default:
						_, _ = io.WriteString(rw, `{"Token":"private-new-fixture"}`)
					}
				default:
					t.Errorf("authorization failure still attempted %s", r.URL.Path)
					rw.WriteHeader(500)
				}
			}))
			defer server.Close()
			e := &Environment{Workspace: w, Client: server.Client(), Instance: Instance{URL: server.URL}, Token: bootstrap}
			if err := e.authorizeLab(t.Context()); err == nil {
				t.Fatal("authorization handoff failure was accepted")
			}
			expectedCalls := []string{"GET /admin/v1/auth/me", "POST /admin/v1/auth/bootstrap"}
			if failure == "unreadable credential" {
				expectedCalls = nil
			}
			if !reflect.DeepEqual(calls, expectedCalls) {
				t.Fatalf("authorization crossed its failed prerequisite: %v", calls)
			}
			if failure == "credential collision" {
				data, err := os.ReadFile(w.path("mdm", "admin-credential"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(data, []byte(existing)) {
					t.Fatal("failed bootstrap overwrote the retained administrator identity")
				}
			} else if failure != "unreadable credential" {
				if _, err := os.Stat(w.path("mdm", "admin-credential")); !os.IsNotExist(err) {
					t.Fatal("failed bootstrap persisted a credential")
				}
			}
		})
	}
}

// TestAuthorizationReusesPrivateCredentialAndRequiresPolicy verifies restart uses
// the retained credential, never reboots first-root handoff and reports policy denial.
func TestAuthorizationReusesPrivateCredentialAndRequiresPolicy(t *testing.T) {
	w := &Workspace{Directory: t.TempDir()}
	if err := os.MkdirAll(w.path("mdm"), 0o700); err != nil {
		t.Fatal(err)
	}
	const credential = "private-administrator-fixture"
	if err := os.WriteFile(w.path("mdm", "admin-credential"), []byte(credential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("restart lost the retained credential")
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/auth/me") {
			_, _ = io.WriteString(rw, `{}`)
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/policies/bench-root") {
			rw.WriteHeader(http.StatusForbidden)
			return
		}
		t.Errorf("unexpected restart request %s", r.URL.Path)
		rw.WriteHeader(500)
	}))
	defer server.Close()
	e := &Environment{Workspace: w, Client: server.Client(), Instance: Instance{URL: server.URL}, Token: "unused-bootstrap"}
	if err := e.authorizeLab(t.Context()); err == nil {
		t.Fatal("policy rejection was treated as authorized startup")
	}
	if !reflect.DeepEqual(calls, []string{"GET /admin/v1/auth/me", "PUT /admin/v1/policies/bench-root"}) {
		t.Fatal(calls)
	}
	if e.Token != credential {
		t.Fatal("restart did not select the retained credential")
	}
	info, err := os.Stat(w.path("mdm", "admin-credential"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("credential privacy changed on restart")
	}
}
