// Package pushtest provides a scripted Pusher and an in-process APNs server
// so push behaviour is testable without Apple.
package pushtest

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
)

// Fake records pushes and returns scripted results.
type Fake struct {
	mu sync.Mutex
	// Results by enrollment id; targets not listed are reported as Sent.
	Results map[mdm.EnrollmentID]push.Result
	// Err, when set, fails the whole batch.
	Err   error
	Calls [][]push.Target
}

// Push implements push.Pusher.
func (f *Fake) Push(_ context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, append([]push.Target(nil), targets...))
	if f.Err != nil {
		return nil, f.Err
	}
	out := map[mdm.EnrollmentID]push.Result{}
	for _, t := range targets {
		if r, ok := f.Results[t.ID]; ok {
			out[t.ID] = r
			continue
		}
		out[t.ID] = push.Result{Sent: true, Status: 200, APNSID: "fake-" + t.ID.ID}
	}
	return out, nil
}

// Pushed returns every target pushed so far.
func (f *Fake) Pushed() []push.Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []push.Target
	for _, c := range f.Calls {
		out = append(out, c...)
	}
	return out
}

// Request is one notification the Server received.
type Request struct {
	Token    string
	Topic    string
	PushType string
	Priority string
	Magic    string
	Headers  http.Header
}

// Script tells the Server how to answer a token.
type Script struct {
	Status     int
	Reason     string
	RetryAfter int
}

// Server is an in-process APNs endpoint. Tokens without a script get 200.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []Request
	scripts  map[string]Script
	// TLSClientCert, when set, requires clients to present it.
}

// NewServer starts a TLS server. Use ClientCertificate to require mutual TLS.
func NewServer() *Server {
	s := &Server{scripts: map[string]Script{}}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	s.Server.EnableHTTP2 = true
	s.Server.StartTLS()
	return s
}

// ScriptToken sets the response for a device token (hex).
func (s *Server) ScriptToken(token []byte, sc Script) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts[hex.EncodeToString(token)] = sc
}

// Requests returns the notifications received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/3/device/") {
		http.Error(w, `{"reason":"BadPath"}`, http.StatusNotFound)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/3/device/")
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	req := Request{Token: token, Topic: r.Header.Get("apns-topic"), PushType: r.Header.Get("apns-push-type"), Priority: r.Header.Get("apns-priority"), Magic: body["mdm"], Headers: r.Header.Clone()}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	sc, ok := s.scripts[token]
	s.mu.Unlock()
	w.Header().Set("apns-id", "id-"+token[:min(8, len(token))])
	if !ok {
		w.WriteHeader(http.StatusOK)
		return
	}
	if sc.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(sc.RetryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(sc.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"reason": sc.Reason, "timestamp": 0})
}

// ClientCertStore returns a push.CertStore whose certificate the Server
// trusts for mutual TLS, plus an option-compatible HTTP client factory.
func ClientCertStore(topic string, cert tls.Certificate) push.StaticCertStore {
	return push.StaticCertStore{topic: cert}
}

// ErrScripted is a convenience error for Fake results.
var ErrScripted = errors.New("pushtest: scripted failure")
