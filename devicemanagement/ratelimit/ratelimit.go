package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var (
	ErrInvalid     = errors.New("ratelimit: invalid quota")
	ErrUnavailable = errors.New("ratelimit: store unavailable")
	ErrCapacity    = errors.New("ratelimit: state capacity reached")
)

// Bucket permits one request per Interval, accumulating at most Burst requests.
// Key is caller-selected; it is hashed before persistence.
type Bucket struct {
	Key      string
	Interval time.Duration
	Burst    int
}

// Validate checks that a quota can be represented with shared microsecond precision.
func (b Bucket) Validate() error {
	if b.Key == "" || b.Interval < time.Microsecond || b.Interval%time.Microsecond != 0 ||
		b.Interval > 24*time.Hour ||
		b.Burst < 1 ||
		b.Burst > 1000000 ||
		int64(b.Burst) > int64((30*24*time.Hour)/b.Interval) {
		return ErrInvalid
	}
	return nil
}

// Decision reports the atomic result across all requested buckets.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Checker is injectable into an HTTP transport or other admission boundary.
type Checker interface {
	Check(context.Context, []Bucket) (Decision, error)
}

// Limiter accounts over shared state. MaxEntries defaults to 4096 (maximum 10000).
// Namespace separates independent capacity pools. It is configuration, not user input.
type Limiter struct {
	Store      state.Store
	MaxEntries int
	Namespace  string
}

func key(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Check consumes every bucket or none. Store failures wrap ErrUnavailable. State
// capacity exhaustion is unavailable rather than an unaccounted successful request.
func (l *Limiter) Check(ctx context.Context, buckets []Bucket) (Decision, error) {
	if l.Store == nil || len(buckets) == 0 || len(buckets) > 32 {
		return Decision{}, ErrInvalid
	}
	limit := l.MaxEntries
	if limit == 0 {
		limit = 4096
	}
	if limit < 1 || limit > 10000 {
		return Decision{}, ErrInvalid
	}
	prefix := "rate/" + key(l.Namespace) + "/"
	keys := []string{prefix + "capacity"}
	seen := map[string]bool{}
	for _, b := range buckets {
		if err := b.Validate(); err != nil {
			return Decision{}, err
		}
		k := prefix + key(b.Key)
		if seen[k] {
			return Decision{}, ErrInvalid
		}
		seen[k] = true
		keys = append(keys, k)
	}
	d := Decision{Allowed: true}
	err := l.Store.Update(ctx, keys, func(tx state.Tx) error {
		now := tx.Now().Truncate(time.Microsecond)
		pending := make([]state.Record, len(buckets))
		newKeys := 0
		for i, b := range buckets {
			k := keys[i+1]
			tat := now
			r, err := tx.Get(ctx, k)
			if errors.Is(err, state.ErrNotFound) {
				newKeys++
			} else if err != nil {
				return err
			} else {
				if len(r.Value) != 8 {
					return errors.New("ratelimit: corrupt state")
				}
				stored := binary.BigEndian.Uint64(r.Value)
				if stored > math.MaxInt64 {
					return errors.New("ratelimit: corrupt timestamp")
				}
				tat = time.UnixMicro(int64(stored))
			}
			allowAt := tat.Add(-time.Duration(b.Burst-1) * b.Interval)
			if allowAt.After(now) {
				d.Allowed = false
				d.RetryAfter = max(d.RetryAfter, allowAt.Sub(now))
			}
			if tat.Before(now) {
				tat = now
			}
			next := tat.Add(b.Interval)
			value := make([]byte, 8)
			binary.BigEndian.PutUint64(value, uint64(next.UnixMicro()))
			pending[i] = state.Record{Key: k, Value: value, ExpiresAt: next}
		}
		if !d.Allowed {
			return nil
		}
		if newKeys > 0 {
			// Capacity lock is also held by every writer. Expired buckets can be
			// reclaimed here without racing renewal or requiring a cleanup worker.
			records, err := tx.List(ctx, prefix, "", limit)
			if err != nil {
				return err
			}
			live := 0
			for _, r := range records {
				if !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt) && !seen[r.Key] {
					if err := tx.Delete(ctx, r.Key); err != nil {
						return err
					}
				} else {
					live++
				}
			}
			if live+newKeys > limit {
				return ErrCapacity
			}
		}
		for _, r := range pending {
			if err := tx.Put(ctx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Decision{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return d, nil
}
