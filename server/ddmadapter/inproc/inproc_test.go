package inproc_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/inproc"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/inmem"
)

// stub answers Handle from a table keyed by endpoint and records the call.
type stub struct {
	responses map[string]ddm.Response
	errs      map[string]error
	calls     []call
}

type call struct {
	id       mdm.EnrollmentID
	endpoint string
	data     []byte
}

func (s *stub) Handle(_ context.Context, id mdm.EnrollmentID, endpoint string, data []byte) (ddm.Response, error) {
	s.calls = append(s.calls, call{id: id, endpoint: endpoint, data: data})
	if err := s.errs[endpoint]; err != nil {
		return ddm.Response{}, err
	}
	return s.responses[endpoint], nil
}

func dmCheckin(t *testing.T, udid, endpoint string, data []byte) (*mdm.Checkin, *checkin.DeclarativeManagement) {
	t.Helper()
	fields := map[string]any{"MessageType": "DeclarativeManagement", "UDID": udid, "Endpoint": endpoint}
	if data != nil {
		fields["Data"] = data
	}
	raw, err := plist.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := mdm.DecodeCheckin(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := ck.Message.(*checkin.DeclarativeManagement)
	if !ok {
		t.Fatalf("message is %T", ck.Message)
	}
	return ck, m
}

func TestHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	engineErr := errors.New("store down")
	newStub := func() *stub {
		return &stub{
			responses: map[string]ddm.Response{
				"tokens":                        {Body: []byte(`{"SyncTokens":{}}`), Status: 200},
				"declaration-items":             {Body: []byte(`{"Declarations":{}}`)},
				"declaration/configuration/one": {Body: []byte(`{"Identifier":"one"}`), Status: 200},
				"declaration/configuration/two": {Status: 404},
				"status":                        {Status: 200},
			},
			errs: map[string]error{
				"bogus":    fmt.Errorf("%w: %q", ddm.ErrBadEndpoint, "bogus"),
				"big":      fmt.Errorf("%w: 2 bytes", ddm.ErrStatusTooLarge),
				"garbled":  fmt.Errorf("%w: eof", ddm.ErrStatusMalformed),
				"failing":  engineErr,
				"notfound": ddm.ErrNotFound,
			},
		}
	}
	ok := func(t *testing.T, endpoint string, data []byte, wantBody string, wantStatus int) {
		t.Helper()
		s := newStub()
		h := inproc.Handler(s)
		ck, m := dmCheckin(t, "D1", endpoint, data)
		got, err := h(ctx, &mdm.Request{}, ck, m)
		if err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
		if string(got.Body) != wantBody || got.Status != wantStatus || got.ContentType != inproc.ContentTypeJSON {
			t.Fatalf("%s: got %+v", endpoint, got)
		}
		if len(s.calls) != 1 || s.calls[0].id != ck.ID || s.calls[0].endpoint != endpoint || string(s.calls[0].data) != string(data) {
			t.Fatalf("%s: backend saw %+v", endpoint, s.calls)
		}
	}
	fail := func(t *testing.T, endpoint string, data []byte, wantCode service.Code, wantIs error) {
		t.Helper()
		h := inproc.Handler(newStub())
		ck, m := dmCheckin(t, "D1", endpoint, data)
		_, err := h(ctx, &mdm.Request{}, ck, m)
		if err == nil || service.CodeOf(err) != wantCode || !errors.Is(err, wantIs) {
			t.Fatalf("%s: got %v (code %v), want code %v wrapping %v", endpoint, err, service.CodeOf(err), wantCode, wantIs)
		}
	}
	t.Run("Tokens", func(t *testing.T) { t.Parallel(); ok(t, "tokens", nil, `{"SyncTokens":{}}`, 200) })
	t.Run("Items", func(t *testing.T) { t.Parallel(); ok(t, "declaration-items", nil, `{"Declarations":{}}`, 200) })
	t.Run("Declaration", func(t *testing.T) {
		t.Parallel()
		ok(t, "declaration/configuration/one", nil, `{"Identifier":"one"}`, 200)
	})
	t.Run("Declaration404", func(t *testing.T) { t.Parallel(); ok(t, "declaration/configuration/two", nil, "", 404) })
	t.Run("Status", func(t *testing.T) { t.Parallel(); ok(t, "status", []byte(`{"StatusItems":{}}`), "", 200) })
	t.Run("BadEndpoint400", func(t *testing.T) {
		t.Parallel()
		fail(t, "bogus", nil, service.CodeBadRequest, ddm.ErrBadEndpoint)
		fail(t, "garbled", []byte("x"), service.CodeBadRequest, ddm.ErrStatusMalformed)
	})
	t.Run("StatusTooLarge400", func(t *testing.T) {
		t.Parallel()
		fail(t, "big", []byte("xx"), service.CodeBadRequest, ddm.ErrStatusTooLarge)
	})
	t.Run("EngineError500", func(t *testing.T) {
		t.Parallel()
		fail(t, "failing", nil, service.CodeInternal, engineErr)
		// ErrNotFound never reaches the adapter as an error from the engine
		// (it becomes a 404 response); a backend that leaks it is internal.
		fail(t, "notfound", nil, service.CodeInternal, ddm.ErrNotFound)
	})
	t.Run("NilBackend", func(t *testing.T) {
		t.Parallel()
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		_, err := inproc.Handler(nil)(ctx, &mdm.Request{}, ck, m)
		if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, inproc.ErrNoBackend) {
			t.Fatalf("nil backend: %v", err)
		}
	})
	t.Run("NilMessage", func(t *testing.T) {
		t.Parallel()
		h := inproc.Handler(newStub())
		ck, m := dmCheckin(t, "D1", "tokens", nil)
		if _, err := h(ctx, &mdm.Request{}, nil, m); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, service.ErrInvalidMessage) {
			t.Fatalf("nil check-in: %v", err)
		}
		if _, err := h(ctx, &mdm.Request{}, ck, nil); service.CodeOf(err) != service.CodeBadRequest {
			t.Fatalf("nil message: %v", err)
		}
	})
	t.Run("RealEngine", func(t *testing.T) {
		t.Parallel()
		e, err := ddm.New(ddm.Config{Store: inmem.New()})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.PutDeclaration(ctx, []byte(`{"Type":"com.apple.management.organization-info","Identifier":"org","Payload":{"Name":"Acme"}}`)); err != nil {
			t.Fatal(err)
		}
		dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
		if _, err := e.AssignDeclaration(ctx, dev, "org"); err != nil {
			t.Fatal(err)
		}
		h := inproc.Handler(e)
		serve := func(endpoint string, data []byte) (service.DMResponse, error) {
			ck, m := dmCheckin(t, "D1", endpoint, data)
			return h(ctx, &mdm.Request{}, ck, m)
		}
		tokens, err := serve("tokens", nil)
		if err != nil || tokens.Status != http.StatusOK || !strings.Contains(string(tokens.Body), `"DeclarationsToken"`) {
			t.Fatalf("tokens: %+v %v", tokens, err)
		}
		want, err := e.Tokens(ctx, dev)
		if err != nil || string(want) != string(tokens.Body) {
			t.Fatalf("tokens differ from the engine: %s vs %s (%v)", tokens.Body, want, err)
		}
		items, err := serve("declaration-items", nil)
		if err != nil || items.Status != http.StatusOK || !strings.Contains(string(items.Body), `"Identifier":"org"`) {
			t.Fatalf("declaration-items: %+v %v", items, err)
		}
		decl, err := serve("declaration/management/org", nil)
		if err != nil || decl.Status != http.StatusOK || !strings.Contains(string(decl.Body), `"Name":"Acme"`) || !strings.Contains(string(decl.Body), `"ServerToken"`) {
			t.Fatalf("declaration: %+v %v", decl, err)
		}
		missing, err := serve("declaration/management/other", nil)
		if err != nil || missing.Status != http.StatusNotFound || len(missing.Body) != 0 {
			t.Fatalf("unknown declaration: %+v %v", missing, err)
		}
		wrongKind, err := serve("declaration/configuration/org", nil)
		if err != nil || wrongKind.Status != http.StatusNotFound {
			t.Fatalf("wrong kind: %+v %v", wrongKind, err)
		}
		status, err := serve("status", []byte(`{"StatusItems":{"device":{"model":{"family":"Mac"}}}}`))
		if err != nil || status.Status != http.StatusOK || len(status.Body) != 0 {
			t.Fatalf("status: %+v %v", status, err)
		}
		if _, err := serve("status", nil); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, ddm.ErrBadEndpoint) {
			t.Fatalf("status without data: %v", err)
		}
		if _, err := serve("status", []byte(`{`)); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, ddm.ErrStatusMalformed) {
			t.Fatalf("malformed status: %v", err)
		}
		if _, err := serve("declaration/nope/x", nil); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, ddm.ErrBadEndpoint) {
			t.Fatalf("bad kind: %v", err)
		}
	})
}

func TestCodeFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want service.Code
	}{
		{ddm.ErrBadEndpoint, service.CodeBadRequest},
		{ddm.ErrStatusTooLarge, service.CodeBadRequest},
		{fmt.Errorf("wrapped: %w", ddm.ErrStatusMalformed), service.CodeBadRequest},
		{ddm.ErrInvalid, service.CodeInternal},
		{errors.New("other"), service.CodeInternal},
	} {
		if got := inproc.CodeFor(tc.err); got != tc.want {
			t.Errorf("CodeFor(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestResponse(t *testing.T) {
	t.Parallel()
	got := inproc.Response(ddm.Response{Body: []byte("{}")})
	if got.Status != 200 || string(got.Body) != "{}" || got.ContentType != inproc.ContentTypeJSON {
		t.Fatalf("zero status: %+v", got)
	}
	if got := inproc.Response(ddm.Response{Status: 404}); got.Status != 404 || got.Body != nil {
		t.Fatalf("404: %+v", got)
	}
}
