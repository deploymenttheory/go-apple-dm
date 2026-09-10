package proxywire

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/state"
)

// ValidKeys requires independent keys with at least 256 bits of key material.
func ValidKeys(send, recv []byte) bool {
	return len(send) >= 32 && len(recv) >= 32 && string(send) != string(recv)
}

// SignRequest creates a fresh v2 envelope covering the method, target and body.
func SignRequest(key []byte, r *http.Request, body []byte) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("proxywire: nonce: %w", err)
	}
	prefix := "v2." + strconv.FormatInt(
		time.Now().Unix(),
		10,
	) + "." + base64.RawURLEncoding.EncodeToString(
		nonce,
	)
	return prefix + "." + sign(key, requestPrefix(r, prefix), body), nil
}

func requestPrefix(r *http.Request, prefix string) []byte {
	target := r.RequestURI
	if target == "" {
		target = r.URL.RequestURI()
	}
	return []byte(
		"ddm-request-v2\n" + r.Method + "\n" + target + "\n" + r.Header.Get(
			"Content-Type",
		) + "\n" + prefix + "\n",
	)
}

// VerifyRequest authenticates freshness and atomically claims the nonce before effects.
func VerifyRequest(
	ctx context.Context,
	st state.Store,
	key []byte,
	r *http.Request,
	body []byte,
) error {
	values := r.Header.Values(HeaderSignature)
	if len(values) != 1 {
		return ErrBadSignature
	}
	header := values[0]
	parts := strings.Split(header, ".")
	if len(parts) != 4 || parts[0] != "v2" {
		return ErrBadSignature
	}
	seconds, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(nonce) != 32 {
		return ErrBadSignature
	}
	if err = verify(
		key,
		parts[3],
		requestPrefix(r, strings.Join(parts[:3], ".")),
		body,
	); err != nil {
		return err
	}
	if st == nil {
		return ErrBadSignature
	}
	h := sha256.Sum256(append(append([]byte(nil), key...), nonce...))
	k := fmt.Sprintf("ddm/replay/%x", h)
	err = st.Update(ctx, []string{k}, func(tx state.Tx) error {
		stamp := time.Unix(seconds, 0)
		if !stamp.After(tx.Now().Add(-5*time.Minute)) || stamp.After(tx.Now().Add(5*time.Minute)) {
			return ErrBadSignature
		}
		old, err := tx.Get(ctx, k)
		if err == nil && tx.Now().Before(old.ExpiresAt) {
			return ErrBadSignature
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("proxywire: replay record: %w", err)
		}
		return tx.Put(
			ctx,
			state.Record{Key: k, Value: []byte{1}, ExpiresAt: tx.Now().Add(10 * time.Minute)},
		)
	})
	if err != nil {
		return fmt.Errorf("proxywire: verify request: %w", err)
	}
	return nil
}

// SignBoundResponse binds a response to this request and its exact representation.
func SignBoundResponse(key []byte, request string, status int, ct string, body []byte) string {
	return sign(
		key,
		[]byte(fmt.Sprintf("ddm-response-v2\n%s\n%d\n%s\n", request, status, ct)),
		body,
	)
}

// VerifyBoundResponse rejects response substitution, including another request's response.
func VerifyBoundResponse(
	key []byte,
	header, request string,
	status int,
	ct string,
	body []byte,
) error {
	return verify(
		key,
		header,
		[]byte(fmt.Sprintf("ddm-response-v2\n%s\n%d\n%s\n", request, status, ct)),
		body,
	)
}
