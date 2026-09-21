package contentcache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// DefaultRetention is the report lifetime when none is configured.
const DefaultRetention = 30 * 24 * time.Hour

// ErrCredential does not disclose whether an enrollment or credential exists.
var ErrCredential = errors.New("contentcache: invalid ingestion credential")

// StoredReport binds untrusted report data to its authenticated enrollment.
type StoredReport struct {
	ID         string
	Enrollment mdm.EnrollmentID
	ReceivedAt time.Time
	Report     *Report
}

// ReportStore is the ingestion, credential lifecycle and inspection contract.
// Accept must revalidate the credential atomically with persisting the report.
type ReportStore interface {
	// RotateCredential replaces the enrollment's ingestion credential and returns the new
	// token once, invalidating the previous credential.
	RotateCredential(context.Context, mdm.EnrollmentID) (string, error)
	// RevokeCredential invalidates the enrollment's current ingestion credential.
	RevokeCredential(context.Context, mdm.EnrollmentID) error
	// Authenticate resolves a valid ingestion credential to its bound enrollment without
	// revealing whether an unknown credential or enrollment exists.
	Authenticate(context.Context, string) (mdm.EnrollmentID, error)
	// Accept revalidates the credential and stores its report atomically, binding the report
	// to the authenticated enrollment.
	Accept(context.Context, string, *Report) error
	// Reports returns the retained reports for an enrollment in a bounded page.
	Reports(context.Context, mdm.EnrollmentID, paging.Page) (paging.Result[StoredReport], error)
}

// StateStore implements ReportStore on the shared transactional state contract.
// Memory, SQLite, PostgreSQL and MySQL therefore share the same implementation.
// Existing state migrations, backup and expiry maintenance apply unchanged.
type StateStore struct {
	State     state.Store
	Retention time.Duration
}

const storePrefix = "contentcache/v1/"

type ingestionCredential struct {
	Enrollment mdm.EnrollmentID
	Digest     []byte
}

// enrollmentKey constructs the state key for an enrolled content-cache server.
func enrollmentKey(id mdm.EnrollmentID) (string, error) {
	if id.Validate() != nil || id.Channel != mdm.ChannelDevice {
		return "", state.ErrInvalid
	}
	data, err := json.Marshal(id)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// RotateCredential immediately invalidates any previous ingestion credential.
// The token is returned once. Only its SHA-256 digest is persisted.
func (s *StateStore) RotateCredential(ctx context.Context, id mdm.EnrollmentID) (string, error) {
	key, err := enrollmentKey(id)
	if err != nil {
		return "", err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := key + "." + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	data, err := json.Marshal(ingestionCredential{Enrollment: id, Digest: digest[:]})
	if err != nil {
		return "", err
	}
	key = storePrefix + "credential/" + key
	err = s.State.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: data}) })
	if err != nil {
		return "", err
	}
	return token, nil
}

// RevokeCredential is idempotent and serializes with in-flight report writes.
func (s *StateStore) RevokeCredential(ctx context.Context, id mdm.EnrollmentID) error {
	key, err := enrollmentKey(id)
	if err != nil {
		return err
	}
	key = storePrefix + "credential/" + key
	return s.State.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Delete(ctx, key) })
}

// credentialKey constructs the state key for a content-cache reporting credential.
func credentialKey(token string) (string, error) {
	key, secret, ok := strings.Cut(token, ".")
	if !ok || len(key) != 64 || len(secret) != 43 {
		return "", ErrCredential
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", ErrCredential
	}
	if _, err := base64.RawURLEncoding.DecodeString(secret); err != nil {
		return "", ErrCredential
	}
	return storePrefix + "credential/" + key, nil
}

// authenticate authenticates a reporting token against its stored digest and bound
// enrollment identity.
func authenticate(ctx context.Context, reader state.Reader, token string) (mdm.EnrollmentID, error) {
	key, err := credentialKey(token)
	if err != nil {
		return mdm.EnrollmentID{}, err
	}
	record, err := reader.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return mdm.EnrollmentID{}, ErrCredential
	}
	if err != nil {
		return mdm.EnrollmentID{}, err
	}
	var cred ingestionCredential
	if err := json.Unmarshal(record.Value, &cred); err != nil {
		return mdm.EnrollmentID{}, ErrCredential
	}
	digest := sha256.Sum256([]byte(token))
	identity, err := enrollmentKey(cred.Enrollment)
	if err != nil || key != storePrefix+"credential/"+identity || subtle.ConstantTimeCompare(digest[:], cred.Digest) != 1 {
		return mdm.EnrollmentID{}, ErrCredential
	}
	return cred.Enrollment, nil
}

// Authenticate validates a credential without accepting any report data.
func (s *StateStore) Authenticate(ctx context.Context, token string) (mdm.EnrollmentID, error) {
	return authenticate(ctx, s.State, token)
}

// Accept validates and persists a report before acknowledging it. Credential
// validation and persistence share the same lock as rotation and revocation.
func (s *StateStore) Accept(ctx context.Context, token string, report *Report) error {
	if report == nil || s.Retention < 0 {
		return state.ErrInvalid
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if _, err := Decode(data); err != nil {
		return err
	}
	key, err := credentialKey(token)
	if err != nil {
		return err
	}
	lifetime := s.Retention
	if lifetime == 0 {
		lifetime = DefaultRetention
	}
	return s.State.Update(ctx, []string{key}, func(tx state.Tx) error {
		id, err := authenticate(ctx, tx, token)
		if err != nil {
			return err
		}
		identity, err := enrollmentKey(id)
		if err != nil {
			return err
		}
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		now := tx.Now().UTC()
		rec := StoredReport{ID: hex.EncodeToString(random), Enrollment: id, ReceivedAt: now, Report: report}
		data, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		reportKey := fmt.Sprintf("%sreports/%s/%019d-%s", storePrefix, identity, math.MaxInt64-now.UnixMicro(), rec.ID)
		return tx.Put(ctx, state.Record{Key: reportKey, Value: data, ExpiresAt: now.Add(lifetime)})
	})
}

// Reports returns newest-first reports using opaque enrollment-bound cursors.
// Expired records are hidden even before the state maintenance worker prunes them.
func (s *StateStore) Reports(ctx context.Context, id mdm.EnrollmentID, page paging.Page) (paging.Result[StoredReport], error) {
	result := paging.Result[StoredReport]{Items: []StoredReport{}}
	identity, err := enrollmentKey(id)
	if err != nil {
		return result, err
	}
	limit := page.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return result, state.ErrInvalid
	}
	prefix := storePrefix + "reports/" + identity + "/"
	after := ""
	if page.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(page.Cursor)
		after = string(decoded)
		if err != nil || !strings.HasPrefix(after, prefix) || !state.ValidKey(after) {
			return result, state.ErrInvalid
		}
	}
	// Use store time for expiry, avoiding differences between replica clocks.
	err = s.State.Update(ctx, []string{storePrefix + "credential/" + identity}, func(tx state.Tx) error {
		for {
			rows, err := tx.List(ctx, prefix, after, limit+1)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if !row.ExpiresAt.IsZero() && !tx.Now().Before(row.ExpiresAt) {
					after = row.Key
					continue
				}
				if len(result.Items) == limit {
					result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(after))
					return nil
				}
				var report StoredReport
				if err := json.Unmarshal(row.Value, &report); err != nil {
					return err
				}
				if report.Enrollment != id {
					return state.ErrInvalid
				}
				result.Items = append(result.Items, report)
				after = row.Key
			}
			if len(rows) < limit+1 {
				break
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}
