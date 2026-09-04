package gdmftest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/gdmf"
)

// Catalog is a fixture shaped like the pmv document: two iOS releases,
// two macOS releases, one visionOS release, and one seeding-only build.
func Catalog() *gdmf.Catalog {
	return &gdmf.Catalog{
		PublicAssetSets: map[string][]gdmf.Asset{
			"iOS": {
				{ProductVersion: "17.5.1", Build: "21F90", PostingDate: "2024-05-20", ExpirationDate: "2024-08-18", SupportedDevices: []string{"iPhone15,2", "iPhone14,7", "iPad13,16"}},
				{ProductVersion: "18.0", Build: "22A3354", PostingDate: "2024-09-16", ExpirationDate: "2024-12-15", SupportedDevices: []string{"iPhone15,2", "iPhone17,1", "iPad13,16"}},
			},
			"macOS": {
				{ProductVersion: "14.5", Build: "23F79", PostingDate: "2024-05-13", ExpirationDate: "2024-08-11", SupportedDevices: []string{"Mac14,7", "J413AP"}},
				{ProductVersion: "15.0", Build: "24A335", PostingDate: "2024-09-16", ExpirationDate: "2024-12-15", SupportedDevices: []string{"Mac14,7", "Mac16,1", "J413AP"}},
			},
			"visionOS": {
				{ProductVersion: "2.0", Build: "22N320", PostingDate: "2024-09-16", ExpirationDate: "2024-12-15", SupportedDevices: []string{"RealityDevice14,1"}},
			},
		},
		AssetSets: map[string][]gdmf.Asset{
			"iOS": {
				{ProductVersion: "18.1", Build: "22B5007p", PostingDate: "2024-09-18", ExpirationDate: "2024-12-17", SupportedDevices: []string{"iPhone15,2"}},
			},
		},
	}
}

// Server serves a catalog over HTTP. Fields may be changed between
// requests; Hits counts requests served.
type Server struct {
	*httptest.Server
	Hits atomic.Int64

	mu      sync.Mutex
	catalog *gdmf.Catalog
	raw     []byte
	status  int
}

// NewServer starts a server for catalog (nil serves Catalog()).
func NewServer(catalog *gdmf.Catalog) *Server {
	if catalog == nil {
		catalog = Catalog()
	}
	s := &Server{catalog: catalog, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// SetCatalog replaces the catalog served.
func (s *Server) SetCatalog(c *gdmf.Catalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalog, s.raw = c, nil
}

// SetRaw serves body verbatim instead of the catalog, for malformed
// responses. Nil restores the catalog.
func (s *Server) SetRaw(body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw = body
}

// SetStatus sets the response status (default 200).
func (s *Server) SetStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

func (s *Server) serve(w http.ResponseWriter, _ *http.Request) {
	s.Hits.Add(1)
	s.mu.Lock()
	catalog, raw, status := s.catalog, s.raw, s.status
	s.mu.Unlock()
	body := raw
	if body == nil {
		var err error
		if body, err = json.Marshal(catalog); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// Fake is a Lookup answering from a map, or failing with Err when set.
type Fake struct {
	// Assets by device identifier.
	Assets map[string]*gdmf.Asset
	// Err, when set, is returned by every call.
	Err error
	// Calls counts Latest invocations.
	Calls atomic.Int64
}

// NewFake builds a Fake from the fixture catalog for the given devices.
func NewFake(devices ...string) *Fake {
	f := &Fake{Assets: map[string]*gdmf.Asset{}}
	cat := Catalog()
	for _, d := range devices {
		if a, ok := cat.Latest(d, false); ok {
			f.Assets[d] = a
		}
	}
	return f
}

// Latest implements gdmf.Lookup.
func (f *Fake) Latest(_ context.Context, deviceID string) (*gdmf.Asset, error) {
	f.Calls.Add(1)
	if f.Err != nil {
		return nil, f.Err
	}
	a, ok := f.Assets[deviceID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", gdmf.ErrNotFound, deviceID)
	}
	out := *a
	return &out, nil
}
