package gdmf_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/gdmf"
	"github.com/deploymenttheory/go-apple-mdm/gdmf/gdmftest"
)

func TestCatalogLatest(t *testing.T) {
	t.Parallel()
	cat := gdmftest.Catalog()
	cases := []struct {
		device    string
		nonPublic bool
		want      string
		ok        bool
	}{
		{"iPhone15,2", false, "18.0", true},
		{"iPhone15,2", true, "18.1", true},
		{"iPhone14,7", false, "17.5.1", true},
		{"Mac14,7", false, "15.0", true},
		{"J413AP", false, "15.0", true},
		{"RealityDevice14,1", false, "2.0", true},
		{"Watch7,1", false, "", false},
		{"", false, "", false},
	}
	for _, c := range cases {
		a, ok := cat.Latest(c.device, c.nonPublic)
		if ok != c.ok || (ok && a.ProductVersion != c.want) {
			t.Fatalf("%q nonPublic=%v: %+v %v", c.device, c.nonPublic, a, ok)
		}
	}
	// The result is a copy.
	a, _ := cat.Latest("iPhone15,2", false)
	a.SupportedDevices[0] = "changed"
	if b, _ := cat.Latest("iPhone15,2", false); b.SupportedDevices[0] == "changed" {
		t.Fatal("Latest returned shared storage")
	}
	// Same version: the later posting date wins.
	tie := &gdmf.Catalog{PublicAssetSets: map[string][]gdmf.Asset{"iOS": {
		{ProductVersion: "18.0", Build: "A", PostingDate: "2024-09-16", SupportedDevices: []string{"d"}},
		{ProductVersion: "18.0", Build: "B", PostingDate: "2024-09-20", SupportedDevices: []string{"d"}},
	}}}
	if a, _ := tie.Latest("d", false); a.Build != "B" {
		t.Fatalf("tie: %+v", a)
	}
	var nilCat *gdmf.Catalog
	if _, ok := nilCat.Latest("d", false); ok {
		t.Fatal("nil catalog")
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"17.5.1", "18.0", -1}, {"18.0", "17.5.1", 1}, {"18", "18.0.0", 0}, {"16.1 (a)", "16.1", 0},
		{"14.5", "14.10", -1}, {"", "1", -1}, {"", "", 0}, {"1.a", "1.b", -1}, {"1.b", "1.a", 1}, {"2", "1.x", 1},
	}
	for _, c := range cases {
		if got := gdmf.CompareVersions(c.a, c.b); got != c.want {
			t.Fatalf("%q vs %q: %d", c.a, c.b, got)
		}
	}
}

func TestClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("FetchAndCache", func(t *testing.T) {
		t.Parallel()
		srv := gdmftest.NewServer(nil)
		defer srv.Close()
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		c := &gdmf.Client{URL: srv.URL, TTL: time.Minute, Now: func() time.Time { return now }, UserAgent: "go-apple-mdm-test"}
		a, err := c.Latest(ctx, "iPhone15,2")
		if err != nil || a.ProductVersion != "18.0" || a.Build != "22A3354" {
			t.Fatalf("%+v %v", a, err)
		}
		if _, err := c.Latest(ctx, "Mac14,7"); err != nil {
			t.Fatal(err)
		}
		if srv.Hits.Load() != 1 {
			t.Fatalf("hits %d", srv.Hits.Load())
		}
		now = now.Add(2 * time.Minute)
		if _, err := c.Latest(ctx, "Mac14,7"); err != nil || srv.Hits.Load() != 2 {
			t.Fatalf("refresh after TTL: %v hits %d", err, srv.Hits.Load())
		}
		if _, err := c.Latest(ctx, "Watch7,1"); !errors.Is(err, gdmf.ErrNotFound) {
			t.Fatalf("unknown device: %v", err)
		}
		// Non-public sets on request.
		np := &gdmf.Client{URL: srv.URL, IncludeNonPublic: true}
		if a, err := np.Latest(ctx, "iPhone15,2"); err != nil || a.ProductVersion != "18.1" {
			t.Fatalf("non-public: %+v %v", a, err)
		}
	})
	t.Run("StaleOnFailure", func(t *testing.T) {
		t.Parallel()
		srv := gdmftest.NewServer(nil)
		defer srv.Close()
		now := time.Now()
		c := &gdmf.Client{URL: srv.URL, TTL: time.Minute, Now: func() time.Time { return now }}
		if _, err := c.Latest(ctx, "iPhone15,2"); err != nil {
			t.Fatal(err)
		}
		srv.SetStatus(http.StatusServiceUnavailable)
		now = now.Add(time.Hour)
		if a, err := c.Latest(ctx, "iPhone15,2"); err != nil || a.ProductVersion != "18.0" {
			t.Fatalf("stale: %+v %v", a, err)
		}
		if err := c.Refresh(ctx); !errors.Is(err, gdmf.ErrStatus) {
			t.Fatalf("refresh reports: %v", err)
		}
		srv.SetStatus(http.StatusOK)
		srv.SetCatalog(&gdmf.Catalog{PublicAssetSets: map[string][]gdmf.Asset{"iOS": {{ProductVersion: "19.0", SupportedDevices: []string{"iPhone15,2"}}}}})
		if err := c.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		if a, _ := c.Latest(ctx, "iPhone15,2"); a.ProductVersion != "19.0" {
			t.Fatalf("after refresh: %+v", a)
		}
	})
	t.Run("MalformedJSON", func(t *testing.T) {
		t.Parallel()
		srv := gdmftest.NewServer(nil)
		defer srv.Close()
		c := &gdmf.Client{URL: srv.URL}
		for _, body := range [][]byte{[]byte("{not json"), []byte(`{"Other":1}`), []byte(`[]`)} {
			srv.SetRaw(body)
			if _, err := c.Latest(ctx, "iPhone15,2"); !errors.Is(err, gdmf.ErrDecode) {
				t.Fatalf("%s: %v", body, err)
			}
		}
		srv.SetRaw(nil)
		if _, err := c.Latest(ctx, "iPhone15,2"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("TooLarge", func(t *testing.T) {
		t.Parallel()
		srv := gdmftest.NewServer(nil)
		defer srv.Close()
		c := &gdmf.Client{URL: srv.URL, MaxBytes: 16}
		if _, err := c.Latest(ctx, "iPhone15,2"); !errors.Is(err, gdmf.ErrTooLarge) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("Unreachable", func(t *testing.T) {
		t.Parallel()
		srv := gdmftest.NewServer(nil)
		u := srv.URL
		srv.Close()
		c := &gdmf.Client{URL: u, HTTP: &http.Client{Timeout: time.Second}}
		if _, err := c.Latest(ctx, "iPhone15,2"); !errors.Is(err, gdmf.ErrRequest) {
			t.Fatalf("%v", err)
		}
		if _, err := (&gdmf.Client{URL: "::bad url"}).Latest(ctx, "x"); !errors.Is(err, gdmf.ErrRequest) {
			t.Fatalf("bad url: %v", err)
		}
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		live := gdmftest.NewServer(nil)
		defer live.Close()
		if _, err := (&gdmf.Client{URL: live.URL}).Catalog(cctx); !errors.Is(err, gdmf.ErrRequest) {
			t.Fatalf("cancelled: %v", err)
		}
	})
	t.Run("Status", func(t *testing.T) {
		t.Parallel()
		srv := gdmftest.NewServer(nil)
		defer srv.Close()
		srv.SetStatus(http.StatusTooManyRequests)
		if _, err := (&gdmf.Client{URL: srv.URL}).Latest(ctx, "iPhone15,2"); !errors.Is(err, gdmf.ErrStatus) {
			t.Fatalf("%v", err)
		}
	})
}

func TestFake(t *testing.T) {
	t.Parallel()
	f := gdmftest.NewFake("iPhone15,2", "Nope1,1")
	a, err := f.Latest(context.Background(), "iPhone15,2")
	if err != nil || a.ProductVersion != "18.0" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := f.Latest(context.Background(), "Nope1,1"); !errors.Is(err, gdmf.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	f.Err = gdmf.ErrRequest
	if _, err := f.Latest(context.Background(), "iPhone15,2"); !errors.Is(err, gdmf.ErrRequest) || f.Calls.Load() != 3 {
		t.Fatalf("%v %d", err, f.Calls.Load())
	}
	var _ gdmf.Lookup = f
	var _ gdmf.Lookup = (*gdmf.Client)(nil)
}
