package bench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveAppEvidence(t *testing.T) {
	for _, name := range []string{"missing registration", "partial registration", "missing credential", "wrong receipt", "matching receipt"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			appDir := filepath.Join(dir, "app")
			if err := os.Mkdir(appDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if name != "missing registration" {
				registration := `{"token":"aabb","topic":"com.example.app","environment":"development"}`
				if name == "partial registration" {
					registration = `{"topic":"com.example.app"}`
				}
				if err := os.WriteFile(
					filepath.Join(appDir, "registration.json"),
					[]byte(registration),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "GET" {
						if name == "missing credential" {
							_, _ = w.Write([]byte(`{"Items":[]}`))
						} else {
							_, _ = w.Write([]byte(`{"Items":[{"Topic":"com.example.app"}]}`))
						}
						return
					}
					if name != "wrong receipt" && name != "matching receipt" {
						t.Error("missing prerequisite reached send")
					}
					var request map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					var payload map[string]any
					if err := json.Unmarshal(request["Payload"], &payload); err != nil {
						t.Error(err)
						return
					}
					correlation := payload["labCorrelationID"]
					if name == "wrong receipt" {
						correlation = "a-different-request"
					}
					receipt, err := json.Marshal(
						map[string]any{
							"labCorrelationID": correlation,
							"topic":            "com.example.app",
							"environment":      "development",
						},
					)
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.WriteFile(
						filepath.Join(appDir, "receipt-test.json"),
						receipt,
						0o600,
					); err != nil {
						t.Error(err)
						return
					}
					_, _ = w.Write([]byte(`{"Accepted":true}`))
				}),
			)
			defer srv.Close()
			e := &Environment{
				Instance:  Instance{Mode: "live", URL: srv.URL},
				Client:    srv.Client(),
				Workspace: &Workspace{Directory: dir},
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := appAlert(ctx, e, "")
			switch name {
			case "matching receipt":
				if err != nil {
					t.Fatal(err)
				}
			case "wrong receipt":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("APNs acceptance alone passed: %v", err)
				}
			default:
				if !errors.Is(err, ErrBlocked) {
					t.Fatalf("prerequisite was not blocked: %v", err)
				}
			}
		})
	}
}
