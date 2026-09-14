package app

import (
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
)

const (
	ActionReadCertificates   = "readCertificates"
	ActionImportCertificates = "importCertificates"
	ActionRevokeCertificates = "revokeCertificates"
)

func (a *App) pkiAdminRoutes() []adminRoute {
	if a.revocations == nil {
		return nil
	}
	return []adminRoute{
		{
			Pattern:       "GET /pki/certificates/{issuer}/{serial}",
			Action:        ActionReadCertificates,
			Family:        "pki",
			LocalMutation: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				registry, err := a.certificateRegistry(r.Context())
				if err != nil {
					pkiError(w, err)
					return
				}
				serial, err := serialFromPath(r)
				if err != nil {
					pkiError(w, err)
					return
				}
				c, err := registry.Lookup(r.Context(), r.PathValue("issuer"), serial)
				if err != nil {
					pkiError(w, err)
					return
				}
				c.DER = nil
				writeJSON(w, 200, c)
			}),
		},
		{
			Pattern:       "POST /pki/certificates/import",
			Action:        ActionImportCertificates,
			Family:        "pki",
			LocalMutation: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				registry, err := a.certificateRegistry(r.Context())
				if err != nil {
					pkiError(w, err)
					return
				}
				var body struct {
					Issuer      string `json:"issuer"`
					Certificate []byte `json:"certificate"`
				}
				if err := json.UnmarshalRead(
					http.MaxBytesReader(w, r.Body, MaxAdminBody),
					&body,
				); err != nil {
					pkiError(w, revocation.ErrInvalid)
					return
				}
				if block, _ := pem.Decode(body.Certificate); block != nil {
					body.Certificate = block.Bytes
				}
				cert, err := x509.ParseCertificate(body.Certificate)
				if err != nil {
					pkiError(w, revocation.ErrInvalid)
					return
				}
				if err := registry.Register(
					r.Context(),
					body.Issuer,
					cert,
					revocation.Provenance{Source: "import"},
				); err != nil {
					pkiError(w, err)
					return
				}
				writeJSON(
					w,
					200,
					map[string]any{"issuer": body.Issuer, "serial": cert.SerialNumber.Text(16)},
				)
			}),
		},
		{
			Pattern:       "POST /pki/certificates/{issuer}/{serial}/revoke",
			Action:        ActionRevokeCertificates,
			Family:        "pki",
			LocalMutation: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				registry, err := a.certificateRegistry(r.Context())
				if err != nil {
					pkiError(w, err)
					return
				}
				serial, err := serialFromPath(r)
				if err != nil {
					pkiError(w, err)
					return
				}
				var body struct {
					Reason int `json:"reason"`
				}
				if err := json.UnmarshalRead(
					http.MaxBytesReader(w, r.Body, 4096),
					&body,
				); err != nil {
					pkiError(w, revocation.ErrInvalid)
					return
				}
				issuer := r.PathValue("issuer")
				if err := registry.Revoke(
					r.Context(),
					issuer,
					serial,
					body.Reason,
				); err != nil {
					pkiError(w, err)
					return
				}
				if a.cfg.publisher() != nil {
					_ = a.cfg.publisher().Publish(
						r.Context(),
						event.Event{
							Type:  event.CertificateRevoked,
							At:    a.cfg.Clock.Now(),
							Actor: "admin",
							Data: map[string]any{
								"issuer": issuer,
								"serial": serial.Text(16),
								"reason": body.Reason,
							},
						},
					)
				}
				writeJSON(w, 200, map[string]any{"status": revocation.Revoked})
			}),
		},
	}
}

func pkiError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, revocation.ErrUnknown):
		status = 404
	case errors.Is(err, revocation.ErrInvalid):
		status = 400
	case errors.Is(err, revocation.ErrRevoked):
		status = 409
	}
	http.Error(w, http.StatusText(status), status)
}
