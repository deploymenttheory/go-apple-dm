package simulator_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

// fakeServer records check-ins and hands out queued commands.
type fakeServer struct {
	mu        sync.Mutex
	checkins  []*mdm.Checkin
	responses []*mdm.Response
	queue     []*mdm.Command
	signers   []string
	roots     *cms.VerifyOptions
	status    int // when non-zero, every request fails with this status
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if f.roots != nil {
			cert, err := cms.VerifyHeader(r.Header.Get(cms.HeaderName), body, *f.roots)
			if err != nil {
				t.Errorf("signature: %v", err)
				w.WriteHeader(400)
				return
			}
			f.mu.Lock()
			f.signers = append(f.signers, cert.Subject.CommonName)
			f.mu.Unlock()
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte("nope"))
			return
		}
		if r.Method != http.MethodPut {
			t.Errorf("method %s", r.Method)
		}
		switch r.Header.Get("Content-Type") {
		case simulator.ContentTypeCheckin:
			ck, err := mdm.DecodeCheckin(body)
			if err != nil {
				t.Errorf("checkin decode: %v", err)
				w.WriteHeader(400)
				return
			}
			f.mu.Lock()
			f.checkins = append(f.checkins, ck)
			f.mu.Unlock()
			switch ck.Type {
			case "GetBootstrapToken":
				out, _ := plist.Marshal(map[string]any{"BootstrapToken": []byte("bst")})
				_, _ = w.Write(out)
			case "GetToken":
				out, _ := plist.Marshal(map[string]any{"TokenData": []byte("tok")})
				_, _ = w.Write(out)
			case "DeclarativeManagement":
				_, _ = w.Write([]byte(`{"endpoint":"ok"}`))
			case "UserAuthenticate":
				out, _ := plist.Marshal(map[string]any{"DigestChallenge": ""})
				_, _ = w.Write(out)
			}
		case simulator.ContentTypeConnect:
			resp, err := mdm.DecodeResponse(body, "")
			if err != nil {
				t.Errorf("response decode: %v", err)
				w.WriteHeader(400)
				return
			}
			f.mu.Lock()
			f.responses = append(f.responses, resp)
			var next *mdm.Command
			if len(f.queue) > 0 {
				next, f.queue = f.queue[0], f.queue[1:]
			}
			f.mu.Unlock()
			if next != nil {
				_, _ = w.Write(next.Raw)
			}
		default:
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
			w.WriteHeader(400)
		}
	})
}

func newDevice(t *testing.T, f *fakeServer, opts ...simulator.Option) *simulator.Device {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return simulator.New("UDID-1", append([]simulator.Option{simulator.WithURLs(srv.URL+"/checkin", srv.URL+"/connect"), simulator.WithClient(srv.Client())}, opts...)...)
}

func TestEnrollConnectAndCheckin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, _ := testpki.NewCA("ca")
	id, _ := ca.Issue("device-1", time.Now().Add(-time.Minute))
	f := &fakeServer{roots: &cms.VerifyOptions{Roots: ca.Pool()}}
	d := newDevice(t, f, simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}), simulator.WithTopic("com.apple.mgmt.t"))
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.checkins) != 2 || f.checkins[0].Type != "Authenticate" || f.checkins[1].Type != "TokenUpdate" || f.checkins[1].ID.ID != "UDID-1" {
		t.Fatalf("checkins = %+v", f.checkins)
	}
	if d.PushMagic == "" || len(d.PushToken) != 32 {
		t.Fatal("push info not generated")
	}
	lock, _ := mdm.NewCommand(&commands.DeviceLock{PIN: new("123456")}, mdm.WithUUID("C1"))
	info, _ := mdm.NewCommand(&commands.DeviceInformation{}, mdm.WithUUID("C2"))
	future, _ := mdm.NewCommand(&commands.DeviceLock{}, mdm.WithUUID("C3"))
	unknown := &mdm.Command{UUID: "C3", RequestType: "Future", Raw: []byte(strings.Replace(string(future.Raw), "DeviceLock", "Future", 1))}
	f.queue = []*mdm.Command{lock, info, unknown}
	d.Responder = func(cmd *mdm.Command) simulator.Reply {
		switch cmd.UUID {
		case "C1":
			return simulator.Reply{Status: mdm.StatusNotNow}
		case "C2":
			return simulator.Reply{Status: mdm.StatusError, ErrorChain: []mdm.ErrorChainItem{{ErrorCode: 12021, ErrorDomain: "MCMDMErrorDomain"}}}
		}
		return simulator.AcknowledgeAll(cmd)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("Connect = %d commands, %v", len(got), err)
	}
	// Idle + three replies.
	if len(f.responses) != 4 || !f.responses[0].IsIdle() || f.responses[1].Status != mdm.StatusNotNow || f.responses[2].Status != mdm.StatusError || len(f.responses[2].ErrorChain) != 1 || f.responses[3].Status != mdm.StatusAcknowledged {
		var got []string
		for _, r := range f.responses {
			got = append(got, string(r.Status)+"/"+r.CommandUUID)
		}
		t.Fatalf("responses = %v", got)
	}
	if len(d.Commands()) != 3 || len(d.Replies()) != 3 || d.Replies()[0].Status != mdm.StatusNotNow {
		t.Fatal("recording")
	}
	// Typed acknowledgement carries a response payload.
	f.queue = []*mdm.Command{lock}
	d.Responder = simulator.AcknowledgeAll
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	last := f.responses[len(f.responses)-1]
	if _, err := mdm.DecodeResponse(last.Raw, "DeviceLock"); err != nil {
		t.Fatalf("typed decode of acknowledgement: %v", err)
	}
	// Other check-ins.
	if err := d.SetBootstrapToken(ctx, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if tok, err := d.GetBootstrapToken(ctx); err != nil || string(tok) != "bst" {
		t.Fatalf("GetBootstrapToken = %q %v", tok, err)
	}
	if tok, err := d.GetToken(ctx, "com.apple.maid"); err != nil || string(tok) != "tok" {
		t.Fatalf("GetToken = %q %v", tok, err)
	}
	if body, err := d.DeclarativeManagement(ctx, "tokens", []byte(`{}`)); err != nil || string(body) != `{"endpoint":"ok"}` {
		t.Fatalf("DDM = %s %v", body, err)
	}
	if err := d.CheckOut(ctx); err != nil {
		t.Fatal(err)
	}
	// User channel.
	u := d.User("U1", "alice", "Alice")
	if body, err := u.Authenticate(ctx, ""); err != nil || !strings.Contains(string(body), "DigestChallenge") {
		t.Fatalf("UserAuthenticate = %s %v", body, err)
	}
	if err := u.TokenUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	lastCk := f.checkins[len(f.checkins)-1]
	if lastCk.ID.Channel != mdm.ChannelUser || lastCk.ID.ID != "UDID-1:U1" {
		t.Fatalf("user token update id = %+v", lastCk.ID)
	}
	if _, err := u.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if r := f.responses[len(f.responses)-1]; r.ID.Channel != mdm.ChannelUser {
		t.Fatalf("user idle id = %+v", r.ID)
	}
	// Re-enrollment with a new identity signs with it.
	id2, _ := ca.Issue("device-2", time.Now().Add(-time.Minute))
	if err := d.Reenroll(ctx, &simulator.Identity{Cert: id2.Cert, Key: id2.Key}); err != nil {
		t.Fatal(err)
	}
	if f.signers[len(f.signers)-1] != "device-2" {
		t.Fatalf("last signer = %s", f.signers[len(f.signers)-1])
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeServer{status: 403}
	d := newDevice(t, f)
	var he *simulator.HTTPError
	if err := d.Enroll(ctx); !errors.As(err, &he) || he.Status != 403 || !strings.Contains(he.Error(), "403") {
		t.Fatalf("expected HTTPError, got %v", err)
	}
	if _, err := d.Connect(ctx); !errors.As(err, &he) {
		t.Fatalf("connect: %v", err)
	}
	if _, err := d.GetBootstrapToken(ctx); err == nil {
		t.Fatal("GetBootstrapToken should fail")
	}
	if _, err := d.GetToken(ctx, "x"); err == nil {
		t.Fatal("GetToken should fail")
	}
	// Unreachable server.
	off := simulator.New("U", simulator.WithURLs("http://127.0.0.1:1/c", "http://127.0.0.1:1/s"))
	if err := off.Authenticate(ctx); err == nil {
		t.Fatal("unreachable should fail")
	}
	if _, err := off.Connect(ctx); err == nil {
		t.Fatal("unreachable connect should fail")
	}
	if err := off.User("u", "s", "l").TokenUpdate(ctx); err == nil {
		t.Fatal("unreachable user token update should fail")
	}
	// Bad URL.
	bad := simulator.New("U", simulator.WithURLs("://bad", "://bad"))
	if err := bad.CheckOut(ctx); err == nil {
		t.Fatal("bad url should fail")
	}
	// Server returns a non-command body on connect.
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("garbage")) }))
	t.Cleanup(junk.Close)
	j := simulator.New("U", simulator.WithURLs(junk.URL, junk.URL))
	if _, err := j.Connect(ctx); err == nil {
		t.Fatal("junk command should fail")
	}
	if _, err := j.GetBootstrapToken(ctx); err == nil {
		t.Fatal("junk bootstrap token should fail")
	}
	if tok, err := (simulator.New("U", simulator.WithURLs(emptyServer(t), emptyServer(t)))).GetBootstrapToken(ctx); err != nil || tok != nil {
		t.Fatalf("empty bootstrap token = %v %v", tok, err)
	}
	// A server that never drains hits the loop limit.
	lock, _ := mdm.NewCommand(&commands.DeviceLock{}, mdm.WithUUID("C"))
	forever := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(lock.Raw) }))
	t.Cleanup(forever.Close)
	fd := simulator.New("U", simulator.WithURLs(forever.URL, forever.URL))
	fd.MaxCommandsPerConnect = 3
	if _, err := fd.Connect(ctx); !errors.Is(err, simulator.ErrTooManyCommands) {
		t.Fatalf("loop limit: %v", err)
	}
	fd.MaxCommandsPerConnect = 0
	if _, err := fd.Connect(ctx); !errors.Is(err, simulator.ErrTooManyCommands) {
		t.Fatalf("default loop limit: %v", err)
	}
	// Unsignable identity.
	ca, _ := testpki.NewCA("ca")
	id, _ := ca.Issue("d", time.Now())
	broken := simulator.New("U", simulator.WithURLs(emptyServer(t), emptyServer(t)), simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: nil}))
	if err := broken.CheckOut(ctx); err == nil {
		t.Fatal("nil key should fail to sign")
	}
}

func emptyServer(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(s.Close)
	return s.URL
}
