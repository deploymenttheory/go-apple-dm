package webhook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

type retained struct {
	Event Event              `json:"event"`
	Body  []byte             `json:"body"`
	Parts map[string]Payload `json:"parts"`
}

// BodyPayload preserves bytes without a lossy intermediate number conversion.
func BodyPayload(body []byte, contentType string, asJSON bool) Payload {
	if asJSON && json.Valid(body) {
		// Hash the same JSON representation embedded in the envelope and served
		// by a reference; whitespace and escaping cannot change the digest.
		body, _ = json.Marshal(json.RawMessage(body))
	}
	h := sha256.Sum256(body)
	p := Payload{ContentType: contentType, Availability: "complete", Size: len(body), SHA256: hex.EncodeToString(h[:]), Encoding: "base64"}
	if asJSON {
		p.Encoding = "json"
		if !json.Valid(body) {
			p.Availability = "undecodable"
			return p
		}
		p.Value = slices.Clone(body)
	} else {
		p.Value, _ = json.Marshal(base64.StdEncoding.EncodeToString(body))
	}
	return p
}

// payloadBytes returns the byte representation used for payload size and digest
// calculation.
func payloadBytes(p Payload) ([]byte, error) {
	if p.Availability != "complete" {
		return nil, ErrNotFound
	}
	if p.Encoding == "json" {
		return p.Value, nil
	}
	var b64 string
	if err := json.Unmarshal(p.Value, &b64); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(b64)
}

// Capture participates in the caller's SQL transaction. It copies payloads at
// capture time, never retains mutable pointers, and records nothing when no
// enabled subscription matches.
func (s *Store) Capture(ctx context.Context, e Event) error {
	if !known(e.Type) {
		return ErrInvalid
	}
	if e.EventID == "" {
		e.EventID = rand.Text()
	}
	if len(e.EventID) > 64 {
		return ErrInvalid
	}
	e.SchemaVersion = SchemaVersion
	if e.OccurredAt.IsZero() {
		e.OccurredAt = s.cfg.Now().UTC()
	}
	if e.Source == "" {
		e.Source = s.cfg.Source
	}
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	err := s.unit.Run(ctx, func(ctx context.Context) error {
		after := ""
		for {
			subs, err := s.List(ctx, after, 1000)
			if err != nil {
				return err
			}
			for _, candidate := range subs {
				if !candidate.Enabled || candidate.Deleted || !candidate.Spec.matches(e) {
					continue
				}
				sub, err := s.load(ctx, candidate.ID, true)
				if err != nil {
					return err
				}
				if !sub.Enabled || sub.Deleted || !sub.Spec.matches(e) {
					continue
				}
				if _, err = s.captureFor(ctx, e, sub.Subscription, e.OccurredAt.Add(s.cfg.PayloadRetention)); err != nil {
					return err
				}
			}
			if len(subs) < 1000 {
				break
			}
			after = subs[len(subs)-1].ID
		}
		return nil
	})
	if err != nil {
		s.captureFailures.Add(1)
	}
	return err
}

// captureFor freezes one occurrence under the subscription disclosure policy and stores
// its encrypted body and outbox delivery in the current capture transaction.
func (s *Store) captureFor(ctx context.Context, e Event, sub Subscription, expires time.Time) (string, error) {
	id := rand.Text()
	e.SchemaVersion = SchemaVersion
	e.Payloads = copyPayloads(e.Payloads, sub.Payload)
	for name, p := range e.Payloads {
		if p.Size > s.cfg.MaxBody {
			p.Value = nil
			p.Href = ""
			p.SHA256 = ""
			p.Availability = "too_large"
			e.Payloads[name] = p
		}
	}
	r := retained{Event: e, Parts: copyPayloads(e.Payloads, sub.Payload)}
	r.Event.Payloads = nil
	body, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	if len(body) > InlineLimit {
		for name, p := range e.Payloads {
			if len(p.Value) == 0 {
				continue
			}
			p.Value = nil
			p.Href = PayloadPath + id + "/" + name
			p.ExpiresAt = &expires
			e.Payloads[name] = p
		}
		body, err = json.Marshal(e)
		if err != nil {
			return "", err
		}
	}
	if len(body) > InlineLimit {
		return "", fmt.Errorf("%w: summary exceeds inline bound", ErrInvalid)
	}
	r.Body = body
	sealed, err := s.seal(r, "webhook_messages.payload", id)
	if err != nil {
		return "", err
	}
	meta := e
	meta.Payloads = nil
	metadata, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	sensitive := 0
	if sub.Payload.Sensitive() {
		sensitive = 1
	}
	_, err = s.exec(ctx, `INSERT INTO webhook_messages (delivery_id, event_id, subscription_id, revision, type, occurred_at, expires_at, sensitive_capture, payload, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, e.EventID, sub.ID, sub.Revision, e.Type, e.OccurredAt.UnixMicro(), expires.UnixMicro(), sensitive, sealed, string(metadata))
	if err != nil {
		return "", err
	}
	// Outbox records deliberately contain no webhook body or receiver URL. The
	// ordinary event/audit API may safely expose this scheduling marker.
	err = s.outbox.Capture(ctx, eventsink.Record{EventID: id, Type: "webhook-delivery", At: e.OccurredAt}, []string{destination(sub.ID, sub.Revision)})
	if err == nil && sub.Paused {
		_, err = s.exec(ctx, "UPDATE event_deliveries SET state = 'paused' WHERE event_id = ?", id)
	}
	return id, err
}

// copyPayloads copies the captured payload entries allowed by the destination's disclosure
// policy.
func copyPayloads(parts map[string]Payload, policy PayloadPolicy) map[string]Payload {
	out := map[string]Payload{}
	for name, p := range parts {
		if policy.allows(name) {
			p.Value = slices.Clone(p.Value)
			out[name] = p
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CaptureOutcome adapts existing in-process events without changing library APIs.
// The projection remains the summary; complete internal data is a distinct,
// separately authorized sensitive representation.
func (s *Store) CaptureOutcome(ctx context.Context, in event.Event) error {
	typ := OutcomeType(string(in.Type))
	if typ == "" {
		return nil
	}
	rec := eventsink.Default().Project(in)
	e := Event{EventID: in.ID, Type: typ, OccurredAt: in.At, CorrelationID: CorrelationID(ctx), Data: map[string]any{"actor": rec.Actor, "fields": rec.Fields}}
	if in.Enrollment.ID != "" && !strings.HasSuffix(string(in.Type), "denied") && !strings.HasSuffix(string(in.Type), "rejected") && in.Type != event.UserAuthFailed {
		e.Subject = &Subject{Kind: "enrollment", ID: rec.ID, ParentID: rec.Parent, Channel: rec.Channel}
	} else if in.Enrollment.ID != "" {
		e.Data["claimed_id"] = rec.ID
	}
	if typ, _ := rec.Fields["request_type"].(string); typ != "" {
		e.Data["command_type"] = typ
	}
	if in.Type == event.CommandResult && s.cfg.CommandType != nil {
		if uuid, _ := rec.Fields["command_uuid"].(string); uuid != "" {
			typ, err := s.cfg.CommandType(ctx, in.Enrollment, uuid)
			if err != nil {
				return err
			}
			if typ != "" {
				e.Data["command_type"] = typ
			}
		}
	}
	if in.Data != nil {
		var p Payload
		switch data := in.Data.(type) {
		case *mdm.Command:
			p = decodedPayload(data.Raw, s.cfg.MaxBody)
		case *mdm.Response:
			p = decodedPayload(data.Raw, s.cfg.MaxBody)
		default:
			b, err := json.Marshal(in.Data)
			if err != nil {
				return err
			}
			p = BodyPayload(b, "application/json", true)
		}
		e.Payloads = map[string]Payload{"event_json": p}
	}
	return s.Capture(ctx, e)
}

// Delivery is the administrative view of a delivery attempt sequence, retaining its
// occurrence ID, subscription revision, and delivery state without captured bodies.
type Delivery struct {
	ID             string    `json:"id"`
	EventID        string    `json:"event_id"`
	SubscriptionID string    `json:"subscription_id"`
	Revision       int       `json:"revision"`
	State          string    `json:"state"`
	Attempts       int       `json:"attempts"`
	NextAttempt    time.Time `json:"next_attempt"`
	LastCode       string    `json:"last_code"`
	ExpiresAt      time.Time `json:"expires_at"`
	Sensitive      bool      `json:"sensitive"`
	Event          Event     `json:"event"`
}

// Deliveries lists delivery metadata after an exclusive delivery-ID cursor. A nonempty id
// selects a subscription or exact delivery; a zero limit selects 100 and the maximum is
// 1000. Captured payload bodies are not returned.
func (s *Store) Deliveries(ctx context.Context, id, after string, limit int) ([]Delivery, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	q := `SELECT m.delivery_id, m.event_id, m.subscription_id, m.revision, d.state, d.attempts, d.next_attempt, d.last_code, m.expires_at, m.sensitive_capture, m.metadata FROM webhook_messages m JOIN event_deliveries d ON d.event_id = m.delivery_id WHERE m.delivery_id > ?`
	args := []any{after}
	if id != "" {
		q += " AND (m.subscription_id = ? OR m.delivery_id = ?)"
		args = append(args, id, id)
	}
	q += " ORDER BY m.delivery_id LIMIT ?"
	args = append(args, limit)
	rows, err := s.query(ctx).QueryContext(ctx, s.d.Rebind(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		var next, expires int64
		var sensitive int
		var meta string
		if err = rows.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Revision, &d.State, &d.Attempts, &next, &d.LastCode, &expires, &sensitive, &meta); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(meta), &d.Event); err != nil {
			return nil, err
		}
		d.Sensitive = sensitive != 0
		d.NextAttempt = time.UnixMicro(next).UTC()
		d.ExpiresAt = time.UnixMicro(expires).UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// retained loads a delivery's captured representation, rejecting missing or expired
// retained payloads.
func (s *Store) retained(ctx context.Context, id string) (retained, Delivery, error) {
	list, err := s.Deliveries(ctx, id, "", 1)
	if err != nil {
		return retained{}, Delivery{}, err
	}
	if len(list) == 0 || list[0].ID != id {
		return retained{}, Delivery{}, ErrNotFound
	}
	d := list[0]
	if !s.cfg.Now().Before(d.ExpiresAt) {
		return retained{}, d, ErrExpired
	}
	var b []byte
	err = s.query(ctx).QueryRowContext(ctx, s.d.Rebind("SELECT payload FROM webhook_messages WHERE delivery_id = ?"), id).Scan(&b)
	if err != nil {
		return retained{}, d, err
	}
	if len(b) == 0 {
		return retained{}, d, ErrExpired
	}
	var r retained
	err = s.open(b, &r, "webhook_messages.payload", id)
	return r, d, err
}

// Retry requeues the same retained delivery for its original subscription revision. Expired
// payloads or changed, paused, disabled, or deleted destinations cannot be retried;
// sensitive captures require the authority flag.
func (s *Store) Retry(ctx context.Context, id string, root bool) error {
	return s.unit.Run(ctx, func(ctx context.Context) error {
		_, d, err := s.retained(ctx, id)
		if err != nil {
			return err
		}
		sub, err := s.load(ctx, d.SubscriptionID, true)
		if err != nil {
			return err
		}
		if (d.Sensitive || sub.Payload.Sensitive()) && !root {
			return ErrForbidden
		}
		if sub.Deleted || !sub.Enabled || sub.Paused || sub.Revision != d.Revision {
			return ErrConflict
		}
		return s.outbox.Retry(ctx, id, destination(sub.ID, sub.Revision))
	})
}

// ReplayRequest selects retained occurrences for a destination. Key makes a non-dry
// replay idempotent; DryRun reports the selection without creating deliveries.
type ReplayRequest struct {
	SubscriptionID string    `json:"subscription_id"`
	EventIDs       []string  `json:"event_ids,omitempty"`
	Type           string    `json:"type,omitempty"`
	After          time.Time `json:"after,omitempty"`
	Before         time.Time `json:"before,omitempty"`
	Key            string    `json:"key"`
	DryRun         bool      `json:"dry_run"`
	Limit          int       `json:"limit,omitempty"`
}

// ReplayResult reports selected occurrence IDs, newly created delivery IDs, missing
// representations, and whether the bounded selection was truncated.
type ReplayResult struct {
	EventIDs    []string            `json:"event_ids"`
	DeliveryIDs []string            `json:"delivery_ids"`
	DryRun      bool                `json:"dry_run"`
	Truncated   bool                `json:"truncated"`
	Missing     map[string][]string `json:"missing_payloads,omitempty"`
}

// Replay selects retained occurrences and creates fresh deliveries under the destination's
// current disclosure policy. It never reconstructs missing payloads from live state.
// Non-dry runs require an idempotency key; reusing it with different input returns
// ErrConflict. The authority flag controls access to sensitive captures.
func (s *Store) Replay(ctx context.Context, req ReplayRequest, root bool) (ReplayResult, error) {
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > 1000 || len(req.EventIDs) > 1000 || len(req.Key) > 64 || !req.DryRun && req.Key == "" || req.Type != "" && !known(req.Type) || !req.After.IsZero() && !req.Before.IsZero() && !req.After.Before(req.Before) {
		return ReplayResult{}, ErrInvalid
	}
	var result ReplayResult
	err := s.unit.Run(ctx, func(ctx context.Context) error {
		sub, err := s.load(ctx, req.SubscriptionID, true)
		if err != nil {
			return err
		}
		if sub.Payload.Sensitive() && !root {
			return ErrForbidden
		}
		if sub.Deleted || !sub.Enabled {
			return ErrConflict
		}
		requestBytes, _ := json.Marshal(req)
		requestHash := hashToken(string(requestBytes))
		if !req.DryRun {
			var existingHash, raw string
			err = s.query(ctx).QueryRowContext(ctx, s.d.Rebind("SELECT request_hash, result FROM webhook_replays WHERE id = ?"), req.Key).Scan(&existingHash, &raw)
			if err == nil {
				if existingHash != requestHash {
					return ErrConflict
				}
				return json.Unmarshal([]byte(raw), &result)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		q := "SELECT delivery_id FROM webhook_messages WHERE expires_at > ? AND payload IS NOT NULL"
		args := []any{s.cfg.Now().UnixMicro()}
		if req.Type != "" {
			q += " AND type = ?"
			args = append(args, req.Type)
		}
		if !req.After.IsZero() {
			q += " AND occurred_at >= ?"
			args = append(args, req.After.UnixMicro())
		}
		if !req.Before.IsZero() {
			q += " AND occurred_at < ?"
			args = append(args, req.Before.UnixMicro())
		}
		if !root {
			q += " AND sensitive_capture = 0"
		}
		if len(req.EventIDs) > 0 {
			q += " AND event_id IN ("
			for i, id := range req.EventIDs {
				if i > 0 {
					q += ","
				}
				q += "?"
				args = append(args, id)
			}
			q += ")"
		}
		q += " ORDER BY occurred_at, event_id, sensitive_capture DESC, delivery_id LIMIT ?"
		args = append(args, 10000)
		rows, err := s.query(ctx).QueryContext(ctx, s.d.Rebind(q), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		result = ReplayResult{EventIDs: []string{}, DeliveryIDs: []string{}, DryRun: req.DryRun, Truncated: len(ids) == 10000, Missing: map[string][]string{}}
		// Select the retained representation with the best coverage of the new
		// policy. Never assemble a wider snapshot from live state or revisions.
		type candidate struct {
			r        retained
			d        Delivery
			score    int
			observed int
		}
		best := map[string]candidate{}
		order := []string{}
		for _, id := range ids {
			r, d, err := s.retained(ctx, id)
			if err != nil {
				return err
			}
			if !sub.Spec.matches(r.Event) {
				continue
			}
			score, observed := 0, 0
			for name, p := range r.Parts {
				if !sub.Payload.allows(name) || p.Availability == "not_captured" {
					continue
				}
				observed++
				if p.Availability == "complete" || p.Availability == "empty" {
					score++
				}
			}
			old, ok := best[d.EventID]
			if !ok {
				order = append(order, d.EventID)
			}
			if !ok || score > old.score || score == old.score && observed > old.observed {
				best[d.EventID] = candidate{r, d, score, observed}
			}
		}
		if len(order) > req.Limit {
			result.Truncated = true
			order = order[:req.Limit]
		}
		for _, eventID := range order {
			c := best[eventID]
			r, d := c.r, c.d
			parts := []string{"event_json"}
			if strings.HasPrefix(r.Event.Type, "protocol.") {
				parts = []string{"request_json", "response_json", "request_raw", "response_raw"}
			}
			for _, part := range parts {
				if !sub.Payload.allows(part) {
					continue
				}
				if p, ok := r.Parts[part]; ok && p.Availability != "not_captured" {
					continue
				}
				result.Missing[eventID] = append(result.Missing[eventID], part)
				if r.Parts == nil {
					r.Parts = map[string]Payload{}
				}
				encoding, ct := "json", "application/json"
				if strings.HasSuffix(part, "_raw") {
					encoding, ct = "base64", "application/octet-stream"
				}
				r.Parts[part] = Payload{Encoding: encoding, ContentType: ct, Availability: "not_captured"}
			}
			result.EventIDs = append(result.EventIDs, d.EventID)
			if !req.DryRun {
				r.Event.Payloads = r.Parts
				delivery, err := s.captureFor(ctx, r.Event, sub.Subscription, d.ExpiresAt)
				if err != nil {
					return err
				}
				result.DeliveryIDs = append(result.DeliveryIDs, delivery)
			}
		}
		if !req.DryRun {
			b, err := json.Marshal(result)
			if err != nil {
				return err
			}
			_, err = s.exec(ctx, "INSERT INTO webhook_replays (id, request_hash, result, created_at) VALUES (?, ?, ?, ?)", req.Key, requestHash, string(b), s.cfg.Now().UnixMicro())
			if err != nil && s.d.IsUniqueViolation(err) {
				return ErrConflict
			}
			return err
		}
		return nil
	})
	return result, err
}

// Prune expires payloads before deleting metadata. Replay never changes expiry.
func (s *Store) Prune(ctx context.Context) error {
	return s.unit.Run(ctx, func(ctx context.Context) error {
		now := s.cfg.Now()
		if _, err := s.exec(ctx, `UPDATE event_deliveries SET state = 'expired', lease_token = '', lease_until = 0 WHERE state NOT IN ('delivered','cancelled','expired') AND event_id IN (SELECT delivery_id FROM webhook_messages WHERE expires_at <= ?)`, now.UnixMicro()); err != nil {
			return err
		}
		if _, err := s.exec(ctx, "UPDATE webhook_messages SET payload = NULL WHERE expires_at <= ?", now.UnixMicro()); err != nil {
			return err
		}
		cutoff := now.Add(-s.cfg.MetadataRetention).UnixMicro()
		for _, table := range []string{"event_deliveries", "event_records"} {
			if _, err := s.exec(ctx, "DELETE FROM "+table+" WHERE event_id IN (SELECT delivery_id FROM webhook_messages WHERE occurred_at < ?)", cutoff); err != nil {
				return err
			}
		}
		if _, err := s.exec(ctx, "DELETE FROM webhook_messages WHERE occurred_at < ?", cutoff); err != nil {
			return err
		}
		_, err := s.exec(ctx, "DELETE FROM webhook_replays WHERE created_at < ?", cutoff)
		return err
	})
}

// RunRetention prunes expired payloads and metadata immediately and then once per minute
// until cancellation. A pruning error stops the worker so the host can expose the failure.
func (s *Store) RunRetention(ctx context.Context) error {
	for {
		if err := s.Prune(ctx); err != nil {
			return err
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Status returns delivery counts and oldest occurrence times by state, capture failures,
// and configured retention periods without exposing destination secrets or captured
// payloads.
func (s *Store) Status(ctx context.Context) (map[string]any, error) {
	rows, err := s.query(ctx).QueryContext(ctx, `SELECT d.state, COUNT(*), MIN(m.occurred_at) FROM event_deliveries d JOIN webhook_messages m ON m.delivery_id = d.event_id GROUP BY d.state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int64{}
	oldest := map[string]time.Time{}
	for rows.Next() {
		var state string
		var count, at int64
		if err = rows.Scan(&state, &count, &at); err != nil {
			return nil, err
		}
		counts[state] = count
		oldest[state] = time.UnixMicro(at).UTC()
	}
	return map[string]any{"states": counts, "oldest": oldest, "capture_failures": s.captureFailures.Load(), "payload_retention_seconds": int64(s.cfg.PayloadRetention.Seconds()), "metadata_retention_seconds": int64(s.cfg.MetadataRetention.Seconds())}, rows.Err()
}
