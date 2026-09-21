package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
)

type (
	observationKey struct{}
	correlationKey struct{}
	observation    struct {
		mu      sync.Mutex
		event   Event
		decoded []byte
		outcome string
	}
)

// WithCorrelation is for authenticated internal transports. Public ingress
// always replaces caller-provided identifiers before processing a request.
func WithCorrelation(ctx context.Context, id string) context.Context {
	if id == "" || len(id) > 64 {
		return ctx
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return ctx
		}
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID returns the exchange correlation identifier stored in ctx, or an empty
// string when no observation has assigned one.
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// ObserveSubject records a resource only after the server has authenticated it.
func ObserveSubject(ctx context.Context, subject Subject) {
	o, _ := ctx.Value(observationKey{}).(*observation)
	if o != nil {
		o.mu.Lock()
		o.event.Subject = &subject
		o.mu.Unlock()
	}
}

// ObserveOutcome records the service result independently of its HTTP status.
func ObserveOutcome(ctx context.Context, outcome string) {
	o, _ := ctx.Value(observationKey{}).(*observation)
	if o != nil {
		o.mu.Lock()
		o.outcome = outcome
		o.mu.Unlock()
	}
}

// ObserveMDM records decoded vocabulary and the independently determined service
// result. HTTP 200 alone is never interpreted as successful MDM authorization.
func ObserveMDM(ctx context.Context, ck *mdm.Checkin, resp *mdm.Response, accepted bool) {
	o, _ := ctx.Value(observationKey{}).(*observation)
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outcome = "rejected"
	if accepted {
		o.outcome = "succeeded"
	}
	var id mdm.EnrollmentID
	if ck != nil {
		o.event.Data["operation"] = ck.Type
		o.decoded = bytes.Clone(ck.Raw)
		id = ck.ID
		if d, ok := ck.Message.(*checkin.DeclarativeManagement); ok {
			o.event.Type = "protocol.ddm.exchange"
			op := strings.SplitN(strings.TrimPrefix(d.Endpoint, "/"), "/", 2)[0]
			if op != "tokens" && op != "declaration-items" && op != "declaration" && op != "status" {
				op = "unknown"
			}
			o.event.Data["operation"] = op
			if len(d.Data) > 0 {
				o.event.Payloads = map[string]Payload{"report_json": BodyPayload(d.Data, "application/json", true)}
			}
		}
	}
	if resp != nil {
		id = resp.ID
		o.decoded = bytes.Clone(resp.Raw)
		o.event.Data["operation"] = "connect"
		o.event.Data["command_uuid"] = resp.CommandUUID
		o.event.Data["device_status"] = string(resp.Status)
	}
	if id.ID != "" {
		if accepted {
			o.event.Subject = &Subject{Kind: "enrollment", ID: id.ID, ParentID: id.ParentID, Channel: id.Channel.String()}
		} else {
			o.event.Data["claimed_id"] = id.ID
		}
	}
}

// ClassifyRoute deliberately excludes administration, internal DDM forwarding,
// health endpoints and payload retrieval. A missing route produces no event.
func ClassifyRoute(pattern, path string) (family, operation string) {
	if pattern == "" {
		return "", ""
	}
	switch {
	case path == "/mdm":
		return "mdm", "unknown"
	case strings.HasPrefix(path, "/scep"):
		return "scep", "exchange"
	case strings.HasPrefix(path, "/acme"):
		return "acme", "exchange"
	case strings.HasPrefix(path, "/pki/"):
		return "certificate_status", "exchange"
	case path == "/content-cache/metrics":
		return "content_cache", "report"
	case strings.HasPrefix(path, "/.well-known/com.apple.remotemanagement"):
		return "enrollment", "discovery"
	case strings.HasPrefix(path, "/enroll/ade"):
		return "enrollment", "ade"
	case strings.HasPrefix(path, "/enroll/authenticate") || strings.HasPrefix(path, "/enroll/oidc") || strings.HasPrefix(path, "/enroll/oauth"):
		return "enrollment", "authentication"
	case strings.HasPrefix(path, "/enroll/"):
		return "enrollment", "account-driven"
	case strings.HasPrefix(path, "/ota"):
		return "enrollment", "ota"
	case strings.HasPrefix(path, "/configuration-profiles/"):
		return "profile", "download"
	}
	return "", ""
}

type boundedBody struct {
	io.ReadCloser
	buf    bytes.Buffer
	limit  int
	read   int64
	eof    bool
	failed bool
}

// Read records request bytes up to the capture bound while forwarding reads to the wrapped
// body.
func (b *boundedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if left := b.limit - b.buf.Len(); left > 0 {
		_, _ = b.buf.Write(p[:min(n, left)])
	}
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.failed = true
	}
	return n, err
}

type observedWriter struct {
	http.ResponseWriter
	buf     bytes.Buffer
	limit   int
	status  int
	written int64
	failed  bool
}

// Unwrap returns the underlying writer so HTTP response controllers can reach its supported
// interfaces.
func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// WriteHeader forwards informational statuses and records and forwards only the first
// final response status.
func (w *observedWriter) WriteHeader(status int) {
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

// Write captures response bytes within the configured bound while preserving the underlying
// write result.
func (w *observedWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	if left := w.limit - w.buf.Len(); left > 0 {
		_, _ = w.buf.Write(p[:min(n, left)])
	}
	if err != nil {
		w.failed = true
	}
	return n, err
}

// Flush forwards a flush to the underlying writer while preserving the observed response
// state.
func (w *observedWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.failed = true
	}
}

// decodedPayload decodes an observed protocol body for capture while retaining explicit
// decoding failures.
func decodedPayload(body []byte, limit int) Payload {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return Payload{ContentType: "application/json", Encoding: "json", Availability: "empty"}
	}
	if json.Valid(trimmed) {
		return BodyPayload(trimmed, "application/json", true)
	}
	var value any
	if err := (plist.Decoder{MaxBytes: limit}).Unmarshal(body, &value); err == nil {
		if b, err := json.Marshal(value); err == nil && len(b) <= limit {
			return BodyPayload(b, "application/json", true)
		}
	}
	return Payload{ContentType: "application/json", Encoding: "json", Availability: "undecodable"}
}

// Observe captures only bytes consumed/written by the handler. It never drains
// rejected bodies or changes protocol responses when observation persistence
// fails. Capture failures remain visible through Status.
func (s *Store) Observe(next http.Handler, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The content-cache bearer is a path segment. Classify using the
		// registered redacted route even when credential parsing later rejects it.
		routeRequest := r
		if strings.HasPrefix(r.URL.Path, "/content-cache/metrics/") {
			routeRequest = r.Clone(r.Context())
			routeRequest.URL.Path, routeRequest.URL.RawPath = "/content-cache/metrics", ""
		}
		_, pattern := mux.Handler(routeRequest)
		family, op := ClassifyRoute(pattern, routeRequest.URL.Path)
		if family == "" {
			if strings.HasPrefix(r.URL.Path, "/admin/") {
				r = r.WithContext(WithCorrelation(r.Context(), rand.Text()))
			}
			next.ServeHTTP(w, r)
			return
		}
		started := s.cfg.Now().UTC()
		o := &observation{event: Event{Type: "protocol." + family + ".exchange", OccurredAt: started, Source: s.cfg.Source, Data: map[string]any{"operation": op}}}
		ctx := WithCorrelation(r.Context(), rand.Text())
		o.event.CorrelationID = CorrelationID(ctx)
		ctx = context.WithValue(ctx, observationKey{}, o)
		body := &boundedBody{ReadCloser: r.Body, limit: s.cfg.MaxBody}
		r = r.WithContext(ctx)
		r.Body = body
		writer := &observedWriter{ResponseWriter: w, limit: s.cfg.MaxBody}
		completed := false
		defer func() {
			if writer.status == 0 {
				writer.status = http.StatusOK
			}
			o.mu.Lock()
			defer o.mu.Unlock()
			outcome := o.outcome
			if outcome == "" {
				outcome = "unknown"
				if writer.status >= 400 && writer.status < 500 {
					outcome = "rejected"
				}
				if writer.status >= 500 {
					outcome = "failed"
				}
			}
			if !completed || writer.failed {
				outcome = "incomplete"
			}
			o.event.Data["outcome"] = outcome
			o.event.Data["http"] = map[string]any{"method": r.Method, "route": pattern, "status": writer.status, "duration_ms": s.cfg.Now().Sub(started).Milliseconds(), "response_complete": completed && !writer.failed}
			if o.event.Payloads == nil {
				o.event.Payloads = map[string]Payload{}
			}
			requestComplete := !body.failed && (body.eof || r.ContentLength >= 0 && body.read == r.ContentLength)
			for name, part := range map[string]struct {
				b        []byte
				ct       string
				size     int64
				complete bool
			}{"request": {body.buf.Bytes(), r.Header.Get("Content-Type"), body.read, requestComplete}, "response": {writer.buf.Bytes(), writer.Header().Get("Content-Type"), writer.written, completed && !writer.failed}} {
				p := BodyPayload(part.b, part.ct, false)
				p.Size = int(part.size)
				if !part.complete || part.size > int64(s.cfg.MaxBody) {
					p.Value = nil
					p.SHA256 = ""
					p.Availability = "incomplete"
					if part.size > int64(s.cfg.MaxBody) {
						p.Availability = "too_large"
					}
				}
				if name == "request" && body.read == 0 && r.ContentLength != 0 {
					p.Availability = "not_read"
					p.Value = nil
					p.SHA256 = ""
				}
				o.event.Payloads[name+"_raw"] = p
				if p.Availability == "complete" {
					decoded := part.b
					if name == "request" && len(o.decoded) > 0 {
						decoded = o.decoded
					}
					o.event.Payloads[name+"_json"] = decodedPayload(decoded, s.cfg.MaxBody)
				} else {
					o.event.Payloads[name+"_json"] = Payload{ContentType: "application/json", Encoding: "json", Availability: p.Availability}
				}
			}
			capture, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			if o.event.Subject != nil && s.cfg.CommandType != nil {
				if uuid, _ := o.event.Data["command_uuid"].(string); uuid != "" {
					id := mdm.EnrollmentID{ID: o.event.Subject.ID}
					typ, err := s.cfg.CommandType(capture, id, uuid)
					if err != nil {
						s.captureFailures.Add(1)
						return
					}
					if typ != "" {
						o.event.Data["command_type"] = typ
					}
				}
			}
			_ = s.Capture(capture, o.event)
		}()
		next.ServeHTTP(writer, r)
		completed = true
	})
}
