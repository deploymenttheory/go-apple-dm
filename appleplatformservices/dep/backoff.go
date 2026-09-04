package dep

import (
	"math/rand/v2"
	"time"
)

// Backoff is a jittered exponential delay: Base doubled per attempt,
// capped at Max, then moved by up to Jitter (a fraction) either way.
type Backoff struct {
	Base time.Duration
	Max  time.Duration
	// Jitter is the fraction of the delay the random spread covers;
	// 0.2 spreads a 10s delay over 8s to 12s. Negative disables jitter.
	Jitter float64
	// Rand returns a value in [0, 1); nil uses math/rand/v2.
	Rand func() float64
}

// withDefaults fills zero fields.
func (b Backoff) withDefaults(base, maxDelay time.Duration) Backoff {
	if b.Base <= 0 {
		b.Base = base
	}
	if b.Max <= 0 {
		b.Max = maxDelay
	}
	if b.Jitter == 0 {
		b.Jitter = 0.2
	}
	if b.Rand == nil {
		b.Rand = rand.Float64 // #nosec G404 -- jitter spreads retries; it is not a secret
	}
	return b
}

// Delay returns the delay before attempt (1 is the first retry).
func (b Backoff) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := b.Base
	for i := 1; i < attempt && d < b.Max; i++ {
		d *= 2
	}
	if b.Max > 0 && d > b.Max {
		d = b.Max
	}
	if b.Jitter > 0 && b.Rand != nil {
		spread := float64(d) * b.Jitter
		d += time.Duration((b.Rand()*2 - 1) * spread)
	}
	if d < 0 {
		return 0
	}
	return d
}
