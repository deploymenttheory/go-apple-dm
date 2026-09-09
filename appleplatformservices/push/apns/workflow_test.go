package apns_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func appPair(t *testing.T, ca *testpki.CA, topic string) tls.Certificate {
	t.Helper()
	id, err := ca.IssuePush(topic, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := *id.Cert
	tmpl.Subject.ExtraNames = tmpl.Subject.Names
	tmpl.ExtraExtensions = []pkix.Extension{
		{Id: asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 3, 1}, Value: []byte{5, 0}},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, ca.Cert, id.Key.Public(), ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: id.Key}
}

func TestCertificateHTTP2AndRotation(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("client CA")
	if err != nil {
		t.Fatal(err)
	}
	var connections atomic.Int64
	closed := make(chan struct{}, 10)
	seen := make(chan string, 10)
	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 || len(r.TLS.PeerCertificates) != 1 {
				t.Error("missing HTTP/2 mutual TLS")
			}
			if r.Header.Get("apns-topic") != r.TLS.PeerCertificates[0].Subject.CommonName {
				t.Error("wrong topic identity")
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("unexpected bearer authorization")
			}
			if r.Header.Get("apns-push-type") != "alert" || r.Header.Get("apns-priority") != "10" ||
				r.Header.Get("apns-expiration") != "0" ||
				r.URL.Path != "/3/device/0123" {
				t.Error("incorrect headers or token encoding")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"aps":{"alert":"test"}}` {
				t.Error("payload changed")
			}
			seen <- r.TLS.PeerCertificates[0].SerialNumber.String()
			w.Header().Set("apns-id", "accepted-id")
			w.WriteHeader(http.StatusOK)
		}),
	)
	srv.EnableHTTP2 = true
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  ca.Pool(),
	}
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	store := push.StaticCertStore{"com.example.app": appPair(t, ca, "com.example.app")}
	client := apns.NewApp(store, apns.WithHost(srv.URL), apns.WithRootCAs(roots))
	defer client.Close()
	request := apns.AppRequest{
		Topic:    "com.example.app",
		Token:    []byte{1, 35},
		PushType: "alert",
		Payload:  json.RawMessage(`{"aps":{"alert":"test"}}`),
	}
	for range 2 {
		r := client.Send(t.Context(), request)
		if !r.Sent() || r.APNSID != "accepted-id" {
			t.Fatalf("send: %+v", r)
		}
	}
	first, second := <-seen, <-seen
	if first != second || connections.Load() != 1 {
		t.Fatal("connection not reused")
	}
	other := request
	other.Topic = "com.example.second"
	store[other.Topic] = appPair(t, ca, other.Topic)
	if r := client.Send(t.Context(), other); !r.Sent() {
		t.Fatal(r.Err)
	}
	if serial := <-seen; serial == first || connections.Load() != 2 {
		t.Fatal("topics shared identity or connection")
	}
	store[request.Topic] = appPair(t, ca, request.Topic)
	if r := client.Send(t.Context(), request); !r.Sent() {
		t.Fatal(r.Err)
	}
	if third := <-seen; third == first || connections.Load() != 3 {
		t.Fatal("renewal reused old identity")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("renewal retained old connection")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown retained connection")
	}
	if r := client.Send(t.Context(), request); !errors.Is(r.Err, apns.ErrClosed) {
		t.Fatalf("closed sender: %v", r.Err)
	}
	untrusted := apns.NewApp(store, apns.WithHost(srv.URL))
	defer untrusted.Close()
	if r := untrusted.Send(
		t.Context(),
		request,
	); r.Err == nil || r.Sent() ||
		strings.Contains(r.Err.Error(), "0123") {
		t.Fatalf("untrusted server/error redaction: %+v", r)
	}
}

func TestCloseCancelsActiveSend(t *testing.T) {
	t.Parallel()
	ca, _ := testpki.NewCA("active")
	entered := make(chan struct{})
	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			close(entered)
			<-r.Context().Done()
		}),
	)
	closed := make(chan struct{}, 1)
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c := apns.NewApp(
		push.StaticCertStore{"com.example.app": appPair(t, ca, "com.example.app")},
		apns.WithHost(srv.URL),
		apns.WithRootCAs(roots),
	)
	done := make(chan push.Result, 1)
	go func() {
		done <- c.Send(t.Context(), apns.AppRequest{Topic: "com.example.app", Token: []byte{1}, PushType: "alert", Payload: json.RawMessage(`{"aps":{"alert":"x"}}`)})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("send never started")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("cancel: %v", r.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("send not canceled")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("active HTTP/2 connection remained after Close")
	}
}

func TestAppValidationAndBackground(t *testing.T) {
	t.Parallel()
	ca, _ := testpki.NewCA("app")
	var sends atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.Header.Get("apns-push-type") != "background" || r.Header.Get("apns-priority") != "5" {
			t.Error("background headers")
		}
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, `{"reason":"Unregistered"}`)
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c := apns.NewApp(
		push.StaticCertStore{"com.example.app": appPair(t, ca, "com.example.app")},
		apns.WithHost(srv.URL),
		apns.WithRootCAs(roots),
	)
	defer c.Close()
	base := apns.AppRequest{
		Topic:    "com.example.app",
		Token:    []byte{1},
		PushType: "background",
		Payload:  json.RawMessage(`{"aps":{"content-available":1}}`),
	}
	if r := c.Send(t.Context(), base); r.Status != 410 || !r.TokenInvalid() {
		t.Fatalf("response: %+v", r)
	}
	for _, change := range []func(*apns.AppRequest){
		func(r *apns.AppRequest) { r.Priority = 10 },
		func(r *apns.AppRequest) { r.Token = nil },
		func(r *apns.AppRequest) { r.Topic = "" },
		func(r *apns.AppRequest) { r.PushType = "voip" },
		func(r *apns.AppRequest) { r.PushType = "alert"; r.Payload = json.RawMessage(`{"aps":{"alert":null}}`) },
		func(r *apns.AppRequest) { r.PushType = "alert"; r.Payload = json.RawMessage(`{"aps":{"badge":-1}}`) },
		func(r *apns.AppRequest) { r.Expiration = -1 },
		func(r *apns.AppRequest) { r.Payload = json.RawMessage(`{"aps":null}`) },
		func(r *apns.AppRequest) { r.Payload = json.RawMessage(`{"aps":{"content-available":1,"alert":"x"}}`) },
		func(r *apns.AppRequest) { r.Payload = json.RawMessage(strings.Repeat("x", 4097)) },
	} {
		r := base
		change(&r)
		if got := c.Send(t.Context(), r); !errors.Is(got.Err, apns.ErrRequest) {
			t.Fatalf("local validation: %+v", got)
		}
	}
	if sends.Load() != 1 {
		t.Fatal("invalid request reached APNs")
	}
}
