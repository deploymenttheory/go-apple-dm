package bench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
)

func TestLiveMDMRequiresDeviceEvidence(t *testing.T) {
	for _, name := range []string{"missing device", "missing enrollment", "incomplete enrollment", "queue unavailable", "queue refused", "push unavailable", "push rejected", "cancelled", "result unavailable", "pending then acknowledged", "malformed result", "device error", "invalid plist", "missing inventory", "timeout"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var polls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/result"):
						count := polls.Add(1)
						switch name {
						case "result unavailable":
							w.WriteHeader(503)
						case "malformed result":
							w.Write([]byte("{"))
						case "device error":
							w.Write([]byte(`{"Status":"Error"}`))
						case "timeout":
							w.WriteHeader(204)
						default:
							if count == 1 {
								w.WriteHeader(204)
								return
							}
							if count == 2 {
								w.Write([]byte(`{"Status":"NotNow"}`))
								return
							}
							answer, err := plist.Marshal(
								map[string]any{
									"QueryResponses": map[string]any{
										"OSVersion":    "26.0",
										"BuildVersion": "25A123",
									},
								},
							)
							if err != nil {
								t.Error(err)
								return
							}
							if name == "invalid plist" {
								answer = []byte("broken")
							}
							if name == "missing inventory" {
								answer, err = plist.Marshal(
									map[string]any{
										"QueryResponses": map[string]any{"OSVersion": "26.0"},
									},
								)
								if err != nil {
									t.Error(err)
									return
								}
							}
							json.NewEncoder(w).
								Encode(map[string]any{"Status": "Acknowledged", "Response": answer})
						}
					case strings.HasSuffix(r.URL.Path, "/commands"):
						if name == "queue unavailable" {
							w.WriteHeader(503)
							return
						}
						if name == "queue refused" {
							w.Write([]byte(`{"Queued":0}`))
							return
						}
						w.Write([]byte(`{"Queued":1}`))
					case strings.HasSuffix(r.URL.Path, "/push"):
						if name == "push unavailable" {
							w.WriteHeader(503)
							return
						}
						if name == "push rejected" {
							w.Write([]byte(`{"Sent":false}`))
							return
						}
						w.Write([]byte(`{"Sent":true}`))
						if name == "cancelled" {
							cancel()
						}
					default:
						if name == "missing enrollment" {
							w.WriteHeader(404)
							return
						}
						if name == "incomplete enrollment" {
							w.Write([]byte(`{"Enabled":true}`))
							return
						}
						json.NewEncoder(w).
							Encode(map[string]any{"Enabled": true, "TokenUpdatedAt": time.Now()})
					}
				}),
			)
			defer srv.Close()
			e := &Environment{Instance: Instance{Mode: "live", URL: srv.URL}, Client: srv.Client()}
			device := "test-device"
			if name == "missing device" {
				device = ""
			}
			err := liveMDM(ctx, e, device)
			if name == "pending then acknowledged" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unproven device delivery passed")
			}
			if strings.HasPrefix(name, "missing e") || name == "incomplete enrollment" ||
				name == "missing device" {
				if !errors.Is(err, ErrBlocked) {
					t.Fatalf("prerequisite: %v", err)
				}
			}
		})
	}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error { err := b.ReadCloser.Close(); b.cancel(); return err }

func TestLiveMDMCancellationWhileAwaitingAcknowledgement(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		payload := `{"Enabled":true,"TokenUpdatedAt":"2026-09-09T12:00:00Z"}`
		status := 200
		switch {
		case strings.HasSuffix(r.URL.Path, "/commands"):
			payload = `{"Queued":1}`
		case strings.HasSuffix(r.URL.Path, "/push"):
			payload = `{"Sent":true}`
		case strings.HasSuffix(r.URL.Path, "/result"):
			payload = ""
			status = 204
		}
		var body io.ReadCloser = io.NopCloser(strings.NewReader(payload))
		if status == 204 {
			body = cancelOnClose{ReadCloser: body, cancel: cancel}
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       body,
			Request:    r,
		}, nil
	})}
	e := &Environment{Client: c, Instance: Instance{Mode: "live", URL: "http://fixture"}}
	if err := liveMDM(ctx, e, "test-device"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acknowledgement wait: %v", err)
	}
}
