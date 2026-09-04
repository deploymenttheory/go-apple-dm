package adetest_test

import (
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/adetest"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
)

func TestSign(t *testing.T) {
	t.Parallel()
	chain := adetest.NewChain(t)
	info := adetest.Info("C02TEST")
	t.Run("SignedAttributes", func(t *testing.T) {
		t.Parallel()
		for _, d := range []adetest.Digest{adetest.SHA1, adetest.SHA256} {
			blob := adetest.Sign(t, chain, info, adetest.SignOptions{SignedAttributes: true, Digest: d})
			content, signer, err := cms.VerifyAttachedWith(blob, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: chain.Anchors()})
			if err != nil || !signer.Equal(chain.Leaf.Cert) {
				t.Fatalf("digest %d: %v", d, err)
			}
			var back ade.MachineInfo
			if err := plist.Unmarshal(content, &back); err != nil || back.SERIAL != "C02TEST" {
				t.Fatalf("%+v %v", back, err)
			}
		}
	})
	t.Run("ContentOnly", func(t *testing.T) {
		t.Parallel()
		blob := adetest.Sign(t, chain, info, adetest.SignOptions{})
		if _, _, err := cms.VerifyAttachedWith(blob, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{chain.Root.Cert}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("OmitIntermediate", func(t *testing.T) {
		t.Parallel()
		blob := adetest.Sign(t, chain, info, adetest.SignOptions{OmitIntermediate: true})
		if _, _, err := cms.VerifyAttachedWith(blob, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{chain.Root.Cert}}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("%v", err)
		}
		if _, _, err := cms.VerifyAttachedWith(blob, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{chain.Intermediate.Cert}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ChainShape", func(t *testing.T) {
		t.Parallel()
		if chain.Intermediate.Cert.NotAfter.Year() != 2014 || chain.Intermediate.Cert.SignatureAlgorithm != x509.SHA1WithRSA || chain.Leaf.Cert.SignatureAlgorithm != x509.SHA1WithRSA {
			t.Fatal("chain does not mirror Apple's")
		}
		if err := chain.Intermediate.Cert.CheckSignatureFrom(chain.Root.Cert); err == nil {
			t.Fatal("stock verification should reject the SHA-1 intermediate")
		}
		if opts := chain.Options(); len(opts.Anchors) != 2 {
			t.Fatal("options")
		}
	})
	t.Run("Content", func(t *testing.T) {
		t.Parallel()
		blob := adetest.Sign(t, chain, info, adetest.SignOptions{Content: []byte("<plist/>")})
		content, _, err := cms.VerifyAttachedWith(blob, cms.VerifyAttachedOptions{})
		if err != nil || string(content) != "<plist/>" {
			t.Fatalf("%q %v", content, err)
		}
	})
}

func TestRequest(t *testing.T) {
	t.Parallel()
	chain := adetest.NewChain(t)
	blob := adetest.Sign(t, chain, adetest.Info("C02REQ"), adetest.SignOptions{SignedAttributes: true})
	for _, lane := range []adetest.Lane{adetest.LaneHeader, adetest.LaneQuery, adetest.LaneBody} {
		req := adetest.Request(t, "https://mdm.example.com/enroll/ade?x=1", blob, lane)
		rec := httptest.NewRecorder()
		var got *ade.Parsed
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			p, err := ade.ParseMachineInfo(r, chain.Options())
			if err != nil {
				t.Errorf("lane %d: %v", lane, err)
				return
			}
			got = p
		}).ServeHTTP(rec, req)
		if got == nil || got.SERIAL != "C02REQ" {
			t.Fatalf("lane %d: %+v", lane, got)
		}
		want := map[adetest.Lane]ade.Origin{adetest.LaneHeader: ade.OriginHeader, adetest.LaneQuery: ade.OriginQuery, adetest.LaneBody: ade.OriginBody}[lane]
		if got.Origin != want {
			t.Fatalf("lane %d origin %s", lane, got.Origin)
		}
	}
	if adetest.Header([]byte{1, 2, 3}) != "AQID" {
		t.Fatal("header")
	}
}
