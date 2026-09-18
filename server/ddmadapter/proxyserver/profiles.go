package proxyserver

import (
	json "encoding/json/v2"
	"errors"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/internal/proxywire"
)

func (s *server) serveConfigurationProfile(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		s.reject(w, r, http.StatusUnsupportedMediaType, proxywire.ErrContentType)
		return
	}
	b, err := proxywire.ReadBody(r.Body, 4096)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	if err := proxywire.VerifyRequest(r.Context(), s.cfg.ReplayStore, s.cfg.RecvKey, r, b); err != nil {
		s.reject(w, r, http.StatusUnauthorized, err)
		return
	}
	var req proxywire.ConfigurationProfileRequest
	if json.Unmarshal(b, &req, json.RejectUnknownMembers(true)) != nil || req.Enrollment.Validate() != nil {
		s.write(w, r, http.StatusBadRequest, nil)
		return
	}
	data, info, err := s.cfg.ConfigurationProfiles.Fetch(r.Context(), req.Enrollment, req.Revision)
	if errors.Is(err, ddm.ErrNotFound) || errors.Is(err, ddm.ErrInvalid) {
		s.write(w, r, http.StatusNotFound, nil)
		return
	}
	if err != nil {
		s.write(w, r, http.StatusInternalServerError, nil)
		return
	}
	h := w.Header()
	h.Set("Content-Type", info.ContentType)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set(proxywire.HeaderSignature, proxywire.SignBoundResponse(s.cfg.SendKey, r.Header.Get(proxywire.HeaderSignature), http.StatusOK, info.ContentType, data))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- validated profile bytes, explicit non-HTML type
}
