package bench

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveAppFailuresAndCredentialPagination(t *testing.T) {
	for _, name := range []string{"invalid registration", "credential unavailable", "paginated credential", "receipt timeout", "unreadable receipt", "invalid receipt glob"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if name == "invalid receipt glob" {
				dir = filepath.Join(dir, "[")
			}
			if err := os.MkdirAll(filepath.Join(dir, "app"), 0o700); err != nil {
				t.Fatal(err)
			}
			registration := `{"token":"aabb","topic":"com.example.app","environment":"development"}`
			if name == "invalid registration" {
				registration = "{"
			}
			writeFixture(t, filepath.Join(dir, "app", "registration.json"), []byte(registration))
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			if name == "receipt timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 40*time.Second)
			}
			defer cancel()
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						if name == "credential unavailable" {
							w.WriteHeader(503)
							return
						}
						if name == "paginated credential" && r.URL.RawQuery == "" {
							io.WriteString(
								w,
								`{"Items":[{"Topic":"com.other"}],"NextCursor":"next/page"}`,
							)
							return
						}
						io.WriteString(w, `{"Items":[{"Topic":"com.example.app"}]}`)
						return
					}
					if name == "unreadable receipt" {
						if err := os.Mkdir(
							filepath.Join(dir, "app", "receipt-directory.json"),
							0o700,
						); err != nil {
							t.Error(err)
						}
					}
					if name == "paginated credential" {
						var req struct{ Payload map[string]any }
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
							return
						}
						receipt, err := json.Marshal(
							map[string]any{
								"labCorrelationID": req.Payload["labCorrelationID"],
								"topic":            "com.example.app",
								"environment":      "development",
							},
						)
						if err != nil {
							t.Error(err)
							return
						}
						if err := os.WriteFile(
							filepath.Join(dir, "app", "receipt-test.json"),
							receipt,
							0o600,
						); err != nil {
							t.Error(err)
						}
					}
					io.WriteString(w, `{"Accepted":true}`)
				}),
			)
			defer srv.Close()
			e := &Environment{
				Instance:  Instance{Mode: "live", URL: srv.URL},
				Client:    srv.Client(),
				Workspace: &Workspace{Directory: dir},
			}
			err := appBackground(ctx, e, "")
			if name == "paginated credential" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unverified app delivery passed")
			}
			if name == "receipt timeout" && !strings.Contains(err.Error(), "unverified") {
				t.Fatalf("missing receipt outcome: %v", err)
			}
		})
	}
}
