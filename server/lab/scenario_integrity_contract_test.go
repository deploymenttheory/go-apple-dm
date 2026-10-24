package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
)

// TestScenarioIntegrityGuardsRejectContradictoryEvidence exercises real scenario
// orchestration against the reference server, introducing one semantic violation
// at a protocol boundary. Earlier successful enrollment must not hide wrong
// command identity, cross-user delivery, stale status or failed identity recovery.
func TestScenarioIntegrityGuardsRejectContradictoryEvidence(t *testing.T) {
	cases := []struct {
		name, want string
		run        func(context.Context, *Environment, string) error
	}{
		{"idle received command", "idle enrollment received commands", enrollIdle},
		{"wrong command identity", "command order differs", commandsInOrder},
		{"changed activation predicate", "activation predicate outcome differs", ddmPredicate},
		{"retained checkout status", "DDM status survived checkout", ddmCheckout},
		{"reactivated checkout", "disabled enrollment reactivated", ddmCheckout},
		{"unauthenticated admin", "unauthenticated admin access succeeded", adminRoutes},
		{"cross-user command", "user command leaked to another user", userChannels},
		{"wrong replacement command", "replacement command not delivered", replacementScenario("scep", false)},
		{"incomplete replacement", "replacement did not reach committed", replacementScenario("scep", false)},
		{"wrong restored certificate", "simulator did not restore previous identity", replacementScenario("scep", true)},
		{"lost replacement management", "management did not continue after replacement", replacementScenario("scep", false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := testWorkspace(t, "simulated")
			if tc.name == "cross-user command" {
				w.Settings = map[string]string{"DM_REQUIRE_USER_AUTH": "true"}
			}
			e, err := Start(t.Context(), w, "", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			base := e.Client.Transport
			injected, afterReplacement := false, false
			statusReads, authenticates, tokenUpdates := 0, 0, 0
			e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				var message map[string]any
				if r.Method == http.MethodPut && r.URL.Path == "/mdm" {
					raw, err := readIntegrityBody(r.Body)
					if err != nil {
						return nil, err
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
					if err := plist.Unmarshal(raw, &message); err != nil {
						return nil, err
					}
					kind, _ := message["MessageType"].(string)
					if kind == "Authenticate" {
						authenticates++
					}
					if kind == "TokenUpdate" {
						tokenUpdates++
					}
					if tc.name == "reactivated checkout" && ((kind == "Authenticate" && authenticates == 2) || (kind == "TokenUpdate" && tokenUpdates == 2)) {
						injected = true
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
					}
					status, _ := message["Status"].(string)
					if tc.name == "idle received command" && !injected && status == "Idle" {
						device, _ := message["UDID"].(string)
						if err := queueIntegrityCommand(r.Context(), e, base, "device", device, "", &commands.ProfileList{}); err != nil {
							return nil, err
						}
						injected = true
					}
				}
				if tc.name == "cross-user command" && !injected && r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/user/") && strings.HasSuffix(r.URL.Path, "/commands") {
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/enrollments/user/"), "/commands")
					bob := strings.TrimSuffix(id, ":alice") + ":bob"
					if err := queueIntegrityCommand(r.Context(), e, base, "user", bob, r.URL.Query().Get("parent"), &commands.ProfileList{}); err != nil {
						return nil, err
					}
					injected = true
				}
				if tc.name == "wrong command identity" && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/commands") {
					raw, err := readIntegrityBody(r.Body)
					if err != nil {
						return nil, err
					}
					var body map[string]any
					if err := plist.Unmarshal(raw, &body); err != nil {
						return nil, err
					}
					uuid, _ := body["CommandUUID"].(string)
					body["CommandUUID"] = "unexpected-" + uuid
					rewritten, err := plist.Marshal(body)
					if err != nil {
						return nil, err
					}
					r.Body = io.NopCloser(bytes.NewReader(rewritten))
					r.ContentLength = int64(len(rewritten))
					injected = true
				}
				if tc.name == "changed activation predicate" && r.Method == http.MethodPut && r.URL.Path == "/admin/v1/declarations" {
					raw, err := readIntegrityBody(r.Body)
					if err != nil {
						return nil, err
					}
					var body map[string]any
					if err := json.Unmarshal(raw, &body); err != nil {
						return nil, err
					}
					if body["Type"] == "com.apple.activation.simple" {
						payload, ok := body["Payload"].(map[string]any)
						if !ok {
							t.Error("activation has no payload")
							return nil, io.ErrUnexpectedEOF
						}
						payload["Predicate"] = "TRUEPREDICATE"
						injected = true
						raw, err = json.Marshal(body)
						if err != nil {
							return nil, err
						}
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
					r.ContentLength = int64(len(raw))
				}
				response, err := base.RoundTrip(r)
				if err != nil {
					return nil, err
				}
				rewrite := ""
				switch {
				case tc.name == "retained checkout status" && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status"):
					statusReads++
					if statusReads == 2 {
						rewrite = `[{"Identifier":"stale-after-checkout"}]`
						injected = true
					}
				case tc.name == "unauthenticated admin" && r.Header.Get("Authorization") == "Bearer invalid":
					response.StatusCode = 200
					injected = true
				case tc.name == "wrong replacement command" && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/replacement"):
					rewrite = `{"ID":"unrelated-command"}`
					injected = true
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/replacement"):
					afterReplacement = true
					if tc.name == "incomplete replacement" {
						rewrite = `{"State":"pending","CandidateCertificate":"candidate","OldCertificate":"old"}`
						injected = true
					}
					if tc.name == "wrong restored certificate" {
						rewrite = `{"State":"failed","CandidateCertificate":"candidate","OldCertificate":"foreign"}`
						injected = true
					}
				}
				if tc.name == "lost replacement management" && afterReplacement && message != nil {
					raw, err := readIntegrityBody(response.Body)
					if err != nil {
						return nil, err
					}
					response.Body = io.NopCloser(bytes.NewReader(raw))
					if len(raw) > 0 {
						var command struct{ Command struct{ RequestType string } }
						if err := plist.Unmarshal(raw, &command); err != nil {
							return nil, err
						}
						if command.Command.RequestType == "DeviceInformation" {
							rewrite = "empty"
							injected = true
						}
					}
				}
				if rewrite != "" {
					if _, err := readIntegrityBody(response.Body); err != nil {
						return nil, err
					}
					if rewrite == "empty" {
						rewrite = ""
					}
					response.Body = io.NopCloser(strings.NewReader(rewrite))
					response.ContentLength = int64(len(rewrite))
				}
				return response, nil
			})
			err = tc.run(t.Context(), e, "")
			e.Client.Transport = base
			if !injected || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("semantic violation did not fail at its integrity guard (injected=%v): %v", injected, err)
			}
		})
	}
}

// queueIntegrityCommand injects an ordinary admin-queued command into the chosen
// channel so native acknowledgment succeeds before the scenario rejects its routing.
func queueIntegrityCommand(ctx context.Context, e *Environment, transport http.RoundTripper, channel, id, parent string, payload commands.Command) error {
	command, err := mdm.NewCommand(payload)
	if err != nil {
		return err
	}
	path := e.URL + "/admin/v1/enrollments/" + channel + "/" + url.PathEscape(id) + "/commands"
	if parent != "" {
		path += "?parent=" + url.QueryEscape(parent)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(command.Raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	response, err := transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return &http.ProtocolError{ErrorString: "fixture command could not be queued"}
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}

// readIntegrityBody releases every intercepted body even if fixture decoding fails.
func readIntegrityBody(body io.ReadCloser) ([]byte, error) {
	raw, err := io.ReadAll(body)
	return raw, errors.Join(err, body.Close())
}
