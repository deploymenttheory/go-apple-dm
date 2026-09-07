package revocation

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/state"
	"golang.org/x/crypto/ocsp"
)

type publication struct {
	DER       []byte
	Number    int64
	RefreshAt time.Time
	Dirty     bool
}

func readPublication(ctx context.Context, s state.Reader, issuer string) (publication, error) {
	v, err := s.Get(ctx, crlKey(issuer))
	if errors.Is(err, state.ErrNotFound) {
		return publication{}, nil
	}
	if err != nil {
		return publication{}, err
	}
	var p publication
	err = json.Unmarshal(v.Value, &p)
	return p, err
}

// CRL returns a currently published CRL, signing a new numbered publication when
// revoked state changes or refresh is due. Counter and DER commit atomically, so
// replicas cannot reuse a number for different lists or serve stale local caches.
func (r *Registry) CRL(ctx context.Context, issuer string) ([]byte, error) {
	i, ok := r.issuers[issuer]
	if !ok {
		return nil, ErrUnknown
	}
	var der []byte
	err := r.Store.Update(ctx, []string{issuerKey(issuer)}, func(tx state.Tx) error {
		p, err := readPublication(ctx, tx, issuer)
		if err != nil {
			return err
		}
		now := tx.Now()
		if now.Before(i.Certificate.NotBefore) || !now.Before(i.Certificate.NotAfter) {
			return ErrExpired
		}
		if len(p.DER) > 0 && !p.Dirty && now.Before(p.RefreshAt) {
			der = p.DER
			return nil
		}
		var entries []x509.RevocationListEntry
		after := ""
		for {
			records, err := tx.List(ctx, certificatePrefix(issuer), after, 1000)
			if err != nil {
				return err
			}
			for _, v := range records {
				var c Certificate
				if err := json.Unmarshal(v.Value, &c); err != nil {
					return err
				}
				if c.Status == Revoked && !c.NotAfter.Before(now) {
					serial, ok := new(big.Int).SetString(c.Serial, 16)
					if !ok {
						return ErrInvalid
					}
					entries = append(entries, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: c.RevokedAt, ReasonCode: c.Reason})
				}
				after = v.Key
			}
			if len(records) < 1000 {
				break
			}
		}
		number := p.Number + 1
		if number <= 0 {
			return ErrInvalid
		}
		next := now.Add(i.CRLTTL)
		if i.Certificate.NotAfter.Before(next) {
			next = i.Certificate.NotAfter
		}
		der, err = x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(number), ThisUpdate: now, NextUpdate: next, RevokedCertificateEntries: entries}, i.Certificate, i.Signer)
		if err != nil {
			return err
		}
		return putJSON(ctx, tx, crlKey(issuer), publication{DER: der, Number: number, RefreshAt: now.Add(i.CRLRefresh)})
	})
	return der, err
}

// OCSP signs a response for the requested issuer and serial. Unregistered serials
// answer Unknown, never Good; the request's issuer hashes must match the route.
func (r *Registry) OCSP(ctx context.Context, issuer string, request []byte) ([]byte, error) {
	i, ok := r.issuers[issuer]
	if !ok {
		return nil, ErrUnknown
	}
	req, err := ocsp.ParseRequest(request)
	if err != nil {
		return nil, ErrInvalid
	}
	expected, err := ocsp.CreateRequest(&x509.Certificate{SerialNumber: req.SerialNumber}, i.Certificate, &ocsp.RequestOptions{Hash: req.HashAlgorithm})
	if err != nil {
		return nil, ErrInvalid
	}
	want, err := ocsp.ParseRequest(expected)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(want.IssuerKeyHash, req.IssuerKeyHash) || !bytes.Equal(want.IssuerNameHash, req.IssuerNameHash) {
		return nil, ErrUnknown
	}
	now := r.now()
	if now.Before(i.Certificate.NotBefore) || !now.Before(i.Certificate.NotAfter) {
		return nil, ErrExpired
	}
	next := now.Add(i.OCSPTTL)
	if i.Certificate.NotAfter.Before(next) {
		next = i.Certificate.NotAfter
	}
	response := ocsp.Response{Status: ocsp.Unknown, SerialNumber: req.SerialNumber, ThisUpdate: now, NextUpdate: next, IssuerHash: req.HashAlgorithm}
	c, err := r.Lookup(ctx, issuer, req.SerialNumber)
	if err != nil && !errors.Is(err, ErrUnknown) {
		return nil, err
	}
	if err == nil {
		switch c.Status {
		case Issued:
			response.Status = ocsp.Good
		case Revoked:
			response.Status = ocsp.Revoked
			response.RevokedAt = c.RevokedAt
			response.RevocationReason = c.Reason
		}
	}
	return ocsp.CreateResponse(i.Certificate, i.Certificate, response, i.Signer)
}

// Handler exposes GET <prefix>/crl/{issuer} and GET/POST
// <prefix>/ocsp/{issuer}[/base64-request]. Configure these exact URLs in ca.Policy.
// Certificate administration is intentionally a separate authenticated API.
func (r *Registry) Handler(prefix string) http.Handler {
	prefix = strings.TrimSuffix(prefix, "/")
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+prefix+"/crl/{issuer}", func(w http.ResponseWriter, q *http.Request) {
		der, err := r.CRL(q.Context(), q.PathValue("issuer"))
		if err != nil {
			statusError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/pkix-crl")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(der)
	})
	handle := func(w http.ResponseWriter, q *http.Request) {
		var raw []byte
		var err error
		if q.Method == http.MethodPost {
			if ct := q.Header.Get("Content-Type"); ct != "application/ocsp-request" {
				http.Error(w, "Unsupported Media Type", 415)
				return
			}
			raw, err = io.ReadAll(http.MaxBytesReader(w, q.Body, 4096))
		} else {
			encoded := q.PathValue("request")
			if len(encoded) > 8192 {
				http.Error(w, "Bad Request", 400)
				return
			}
			raw, err = base64.StdEncoding.DecodeString(encoded)
		}
		if err != nil {
			http.Error(w, "Bad Request", 400)
			return
		}
		der, err := r.OCSP(q.Context(), q.PathValue("issuer"), raw)
		if err != nil {
			statusError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Length", strconv.Itoa(len(der)))
		_, _ = w.Write(der)
	}
	mux.HandleFunc("POST "+prefix+"/ocsp/{issuer}", handle)
	mux.HandleFunc("GET "+prefix+"/ocsp/{issuer}/{request...}", handle)
	return mux
}

func statusError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	if errors.Is(err, ErrUnknown) {
		status = http.StatusNotFound
	}
	if errors.Is(err, ErrInvalid) {
		status = http.StatusBadRequest
	}
	http.Error(w, http.StatusText(status), status)
}
