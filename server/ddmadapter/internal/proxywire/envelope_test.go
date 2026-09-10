package proxywire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/state"
)

func TestEnvelopeBindsRequestAndRejectsReplay(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	body := []byte("signed body")
	original := httptest.NewRequest(
		http.MethodPost,
		"https://ddm.example/ddm/v1/declarative-management",
		nil,
	)
	original.Header.Set("Content-Type", ContentType)
	sig, err := SignRequest(key, original, body)
	if err != nil {
		t.Fatal(err)
	}
	original.Header.Set(HeaderSignature, sig)
	for _, mutation := range []string{"method", "path", "content-type", "body", "duplicate"} {
		t.Run(mutation, func(t *testing.T) {
			st := state.NewMemory()
			req := original.Clone(t.Context())
			data := body
			switch mutation {
			case "method":
				req.Method = http.MethodPut
			case "path":
				req.RequestURI = "/another/resource"
			case "content-type":
				req.Header.Set("Content-Type", "text/plain")
			case "body":
				data = []byte("changed")
			case "duplicate":
				req.Header.Add(HeaderSignature, sig)
			}
			if err := VerifyRequest(
				t.Context(),
				st,
				key,
				req,
				data,
			); !errors.Is(
				err,
				ErrBadSignature,
			) {
				t.Fatal(err)
			}
			if err := VerifyRequest(t.Context(), st, key, original, body); err != nil {
				t.Fatal("invalid request consumed nonce", err)
			}
			if err := VerifyRequest(
				t.Context(),
				st,
				key,
				original,
				body,
			); !errors.Is(
				err,
				ErrBadSignature,
			) {
				t.Fatal("replay accepted", err)
			}
		})
	}
}

func TestEnvelopeFreshnessAndBoundResponse(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	req := httptest.NewRequest(
		http.MethodPost,
		"https://ddm.example/ddm/v1/declarative-management",
		nil,
	)
	sig, err := SignRequest(key, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderSignature, sig)
	for _, offset := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		st := state.NewMemory()
		st.Now = func() time.Time { return time.Now().Add(offset) }
		if err = VerifyRequest(
			context.Background(),
			st,
			key,
			req,
			nil,
		); !errors.Is(
			err,
			ErrBadSignature,
		) {
			t.Fatal("stale/future envelope", err)
		}
	}
	response := []byte(`{"token":"secret"}`)
	mac := SignBoundResponse(key, sig, 200, "application/json", response)
	if err = VerifyBoundResponse(key, mac, sig, 200, "application/json", response); err != nil {
		t.Fatal(err)
	}
	other, err := SignRequest(key, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		request string
		status  int
		ct      string
		body    []byte
	}{
		{other, 200, "application/json", response}, {sig, 404, "application/json", response}, {sig, 200, "text/html", response}, {sig, 200, "application/json", []byte("different")},
	} {
		if err = VerifyBoundResponse(
			key,
			mac,
			v.request,
			v.status,
			v.ct,
			v.body,
		); !errors.Is(
			err,
			ErrBadSignature,
		) {
			t.Fatal("substituted response accepted", err)
		}
	}
}

func TestEnvelopeRejectsMalformedMetadataAndMissingReplayStore(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	r := httptest.NewRequest("POST", "https://ddm.example/v1/declarative-management", nil)
	sig, err := SignRequest(key, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(sig, ".")
	for _, signature := range []string{
		"v2.invalid." + parts[2] + "." + parts[3],
		"v2." + parts[1] + ".invalid." + parts[3],
		sig,
	} {
		r.Header.Set(HeaderSignature, signature)
		if err := VerifyRequest(t.Context(), nil, key, r, nil); !errors.Is(err, ErrBadSignature) {
			t.Fatal(err)
		}
	}
	// The v1 response verifier is retained only to prove legacy signatures cannot
	// authenticate a v2 response.
	old := SignResponse(key, 200, nil)
	if err := VerifyResponse(key, old, 200, nil); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBoundResponse(key, old, sig, 200, "", nil); !errors.Is(err, ErrBadSignature) {
		t.Fatal("legacy response accepted", err)
	}
}
