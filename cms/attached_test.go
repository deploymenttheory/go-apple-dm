package cms_test

import (
	"errors"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-mdm/cms"
)

func TestSignAttachedRoundTrip(t *testing.T) {
	t.Parallel()
	ca := newCA(t)
	content := []byte("<?xml version=\"1.0\"?><plist version=\"1.0\"><dict/></plist>")
	leaf := newLeaf(t, ca, rsaKey(t), time.Now().Add(-time.Minute))
	der, err := cms.SignAttached(content, leaf.cert, leaf.key)
	if err != nil {
		t.Fatal(err)
	}
	if !cms.IsSigned(der) || cms.IsSigned(content) || cms.IsSigned(nil) {
		t.Fatal("IsSigned")
	}
	got, signer, err := cms.VerifyAttached(der, cms.VerifyOptions{Roots: pool(ca)})
	if err != nil || string(got) != string(content) || !signer.Equal(leaf.cert) {
		t.Fatalf("VerifyAttached: %v", err)
	}
	ec := newLeaf(t, ca, ecKey(t), time.Now().Add(-time.Minute))
	der2, err := cms.SignAttached(content, ec.cert, ec.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cms.VerifyAttached(der2, cms.VerifyOptions{}); err != nil {
		t.Fatalf("ecdsa attached: %v", err)
	}
	// Wrong root, garbage, detached input, nil key.
	if _, _, err := cms.VerifyAttached(der, cms.VerifyOptions{Roots: pool(newCA(t))}); !errors.Is(err, cms.ErrChain) {
		t.Fatalf("wrong root: %v", err)
	}
	if _, _, err := cms.VerifyAttached([]byte("garbage"), cms.VerifyOptions{}); !errors.Is(err, cms.ErrParse) {
		t.Fatalf("garbage: %v", err)
	}
	detached, _ := cms.Sign(content, leaf.cert, leaf.key)
	if _, _, err := cms.VerifyAttached(detached, cms.VerifyOptions{}); !errors.Is(err, cms.ErrParse) {
		t.Fatalf("detached as attached: %v", err)
	}
	if _, err := cms.SignAttached(content, nil, nil); !errors.Is(err, cms.ErrSign) {
		t.Fatal("nil key")
	}
	// Tampered embedded content fails verification.
	sd, _ := pkcs7.NewSignedData(content)
	_ = sd.AddSigner(leaf.cert, leaf.key, pkcs7.SignerInfoConfig{})
	good, _ := sd.Finish()
	bad := append([]byte(nil), good...)
	for i := range bad {
		if string(bad[i:i+6]) == "<plist" {
			bad[i+1] = 'q'
			break
		}
	}
	if _, _, err := cms.VerifyAttached(bad, cms.VerifyOptions{}); err == nil {
		t.Fatal("tampered content verified")
	}
}
