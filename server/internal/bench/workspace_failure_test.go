package bench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceRejectsIncompleteOrInvalidState(t *testing.T) {
	t.Parallel()
	for _, args := range [][3]string{{"unknown", "inmem", "all"}, {"live", "unknown", "all"}, {"live", "inmem", "unknown"}} {
		if err := Init(t.TempDir(), args[0], args[1], args[2], "127.0.0.1:0"); err == nil {
			t.Fatalf("invalid workspace accepted: %v", args)
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	writeFixture(t, file, []byte("preserve"))
	if err := Init(file, "live", "inmem", "all", "127.0.0.1:0"); err == nil {
		t.Fatal("file replaced by workspace")
	}
	if err := initialize(file); err == nil {
		t.Fatal("file replaced by identities")
	}
	partial := t.TempDir()
	if err := os.Mkdir(filepath.Join(partial, "mdm"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(partial, "mdm", "ca.pem"), []byte("preserve"))
	if err := Init(partial, "live", "inmem", "all", "127.0.0.1:0"); err == nil {
		t.Fatal("partial identity accepted")
	}
	if err := initialize(filepath.Join(partial, "mdm")); err == nil {
		t.Fatal("identity overwritten")
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("absent workspace loaded")
	}
	for _, data := range []string{"{", `{"Version":2}`, `{"Version":1,"Mode":"invalid","Topology":"all"}`} {
		writeFixture(t, filepath.Join(dir, "bench.json"), []byte(data))
		if _, err := Load(dir); err == nil {
			t.Fatal("invalid config loaded")
		}
	}
	for _, addr := range []string{"invalid", "0.0.0.0:0", "example.com:0"} {
		if _, err := address(t.Context(), addr); err == nil {
			t.Fatalf("invalid listener accepted: %s", addr)
		}
	}
	w := testWorkspace(t, "live")
	for _, name := range []string{"ca.pem", "admin-token"} {
		path := w.path("mdm", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, err := Start(t.Context(), w, "", io.Discard); err == nil {
			t.Fatal("missing credential accepted")
		}
		if len(w.Doctor()["Missing"].([]string)) == 0 {
			t.Fatal("doctor omitted missing credential")
		}
		writeFixture(t, path, data)
	}
	writeFixture(t, w.path("mdm", "ca.pem"), []byte("bad CA"))
	if _, err := w.client(); err == nil {
		t.Fatal("invalid roots accepted")
	}
}

func TestStartFailureCleansUp(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing DSN", "missing storage key", "bad config", "missing binary", "split memory", "bad push certificate", "unreadable push certificate", "missing push key", "split binary", "split bad config"} {
		t.Run(name, func(t *testing.T) {
			w := testWorkspace(t, "live")
			binary := ""
			switch name {
			case "missing DSN":
				w.Storage = "postgres"
			case "missing storage key":
				if err := os.Remove(w.path("mdm", "storage-key")); err != nil {
					t.Fatal(err)
				}
			case "bad config":
				w.Settings = map[string]string{"DM_PUSH_COALESCE": "invalid"}
			case "missing binary":
				binary = w.path("absent")
			case "split memory":
				w.Topology = "split"
			case "bad push certificate":
				writeFixture(t, w.path("mdm", "push.pem"), []byte("bad"))
			case "unreadable push certificate":
				if err := os.Mkdir(w.path("mdm", "push.pem"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "missing push key":
				w.Settings = map[string]string{
					"DM_ADMIN_TOKEN": "",
				} // absent operator authentication prevents seeding.
			case "split binary":
				w.Storage = "sqlite"
				w.Topology = "split"
				binary = w.path("absent")
			case "split bad config":
				w.Storage = "sqlite"
				w.Topology = "split"
				w.Settings = map[string]string{"DM_PUSH_COALESCE": "invalid"}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if e, err := Start(ctx, w, binary, io.Discard); err == nil {
				e.Close()
				t.Fatal("invalid startup succeeded")
			}
		})
	}
}

func TestBenchReportsRetainFailureAndBlockedStatus(t *testing.T) {
	t.Parallel()
	e := &Environment{Instance: Instance{Mode: "live"}}
	results := []Result{}
	for _, err := range []error{errors.New("unavailable"), ErrBlocked} {
		result := Run(
			t.Context(),
			e,
			Scenario{
				ID:    "test",
				Modes: []string{"live"},
				Run:   func(context.Context, *Environment, string) error { return err },
			},
			"test",
			"revision",
			"",
		)
		if result.Status == "passed" || result.Detail == "" {
			t.Fatal("failure lost")
		}
		results = append(results, result)
	}
	dir := t.TempDir()
	if err := WriteReports(dir, results); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "junit.xml"))
	if err != nil || !strings.Contains(string(b), `failures="1"`) ||
		!strings.Contains(string(b), `skipped="1"`) {
		t.Fatalf("JUnit: %s %v", b, err)
	}
	for _, path := range []string{"results.json", "junit.xml"} {
		d := t.TempDir()
		if err := os.Mkdir(filepath.Join(d, path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := WriteReports(d, results); err == nil {
			t.Fatal("report write error ignored")
		}
	}
	file := filepath.Join(dir, "file")
	writeFixture(t, file, nil)
	if err := WriteReports(file, results); err == nil {
		t.Fatal("invalid report directory accepted")
	}
	if err := WriteReports(
		t.TempDir(),
		[]Result{{Started: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}},
	); err == nil {
		t.Fatal("invalid timestamp accepted")
	}
	if _, err := Select("absent"); err == nil {
		t.Fatal("unknown selection accepted")
	}
	selected, err := SelectMode("live", "all")
	if err != nil || len(selected) != 3 {
		t.Fatalf("live selection: %v %v", selected, err)
	}
	selected, err = Select("apns")
	if err != nil || len(selected) == 0 {
		t.Fatal("APNs family missing")
	}
}

type failingRead struct{}

func (failingRead) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingRead) Close() error             { return nil }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBenchHTTPAndControlFailures(t *testing.T) {
	t.Parallel()
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: failingRead{}, Header: make(http.Header)}, nil
	})}
	if _, _, err := HTTP(
		t.Context(),
		c,
		"http://local",
		"token",
		"GET",
		"",
		nil,
	); !errors.Is(
		err,
		io.ErrUnexpectedEOF,
	) {
		t.Fatalf("truncated response: %v", err)
	}
	if _, _, err := HTTP(t.Context(), c, "://", "token", "GET", "", nil); err == nil {
		t.Fatal("bad URL accepted")
	}
	e := &Environment{Instance: Instance{ControlURL: "://"}, Client: c}
	if _, err := e.Control(t.Context(), "GET", "", nil); err == nil {
		t.Fatal("bad control URL accepted")
	}
	if err := e.api(t.Context(), "POST", "", make(chan int), nil); err == nil {
		t.Fatal("unsupported JSON accepted")
	}
	srv := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); w.Write([]byte("secret")) },
		),
	)
	defer srv.Close()
	e.ControlURL = srv.URL
	if _, err := e.Control(
		t.Context(),
		"GET",
		"",
		nil,
	); err == nil ||
		strings.Contains(err.Error(), "secret") {
		t.Fatalf("control failure leaked secret: %v", err)
	}
	srv.Close()
	if _, err := e.Control(t.Context(), "GET", "", nil); err == nil {
		t.Fatal("dead supervisor accepted")
	}
	w := testWorkspace(t, "live")
	writeFixture(t, w.path("running.json"), []byte("{"))
	if _, err := Attach(w); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	data, err := json.Marshal(Instance{})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.path("running.json"), data)
	for _, name := range []string{"admin-token", "ca.pem"} {
		if err := os.Remove(w.path("mdm", name)); err != nil {
			t.Fatal(err)
		}
		if _, err := Attach(w); err == nil {
			t.Fatal("missing credential accepted")
		}
	}
}

func TestInitPreservesFileAtIdentityDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "mdm"), []byte("existing user file"))
	if err := Init(dir, "live", "inmem", "all", "127.0.0.1:0"); err == nil {
		t.Fatal("identity initialization replaced existing file")
	}
	data, err := os.ReadFile(filepath.Join(dir, "mdm"))
	if err != nil || string(data) != "existing user file" {
		t.Fatal("existing file changed")
	}
}
