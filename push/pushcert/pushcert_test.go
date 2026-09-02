package pushcert_test

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/push/pushcert"
)

const topic = "com.apple.mgmt.External.test"

var uid = asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}

func pemBlock(typ string, b []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b})
}

func TestParse(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("pushcert")
	if err != nil {
		t.Fatal(err)
	}
	notBefore := time.Now().Add(-time.Minute).Truncate(time.Second)
	id, err := ca.IssuePush(topic, notBefore)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("rsa pkcs8", func(t *testing.T) {
		t.Parallel()
		p, err := pushcert.Parse(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		if p.Topic != topic || !p.Leaf.Equal(id.Cert) || p.TLS.Leaf != p.Leaf || len(p.TLS.Certificate) != 1 {
			t.Fatalf("parsed %+v", p)
		}
		if !p.NotBefore.Equal(notBefore) || !p.NotAfter.Equal(notBefore.Add(24*time.Hour)) {
			t.Fatalf("validity %s %s", p.NotBefore, p.NotAfter)
		}
		if _, ok := p.TLS.PrivateKey.(*rsa.PrivateKey); !ok {
			t.Fatalf("key %T", p.TLS.PrivateKey)
		}
	})
	t.Run("rsa pkcs1", func(t *testing.T) {
		t.Parallel()
		der := x509.MarshalPKCS1PrivateKey(id.Key.(*rsa.PrivateKey))
		if _, err := pushcert.Parse(certPEM, pemBlock("RSA PRIVATE KEY", der)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ec sec1", func(t *testing.T) {
		t.Parallel()
		ec, err := ca.Issue("ec", notBefore)
		if err != nil {
			t.Fatal(err)
		}
		ecID, err := ca.IssuePushWithKey(topic, notBefore, ec.Key)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalECPrivateKey(ec.Key.(*ecdsa.PrivateKey))
		if err != nil {
			t.Fatal(err)
		}
		p, err := pushcert.Parse(pemBlock("CERTIFICATE", ecID.Cert.Raw), pemBlock("EC PRIVATE KEY", der))
		if err != nil {
			t.Fatal(err)
		}
		if p.Topic != topic {
			t.Fatalf("topic %q", p.Topic)
		}
	})
	t.Run("chain keeps both certificates", func(t *testing.T) {
		t.Parallel()
		chain := append(append([]byte{}, certPEM...), pemBlock("CERTIFICATE", ca.Cert.Raw)...)
		chain = append(chain, pemBlock("COMMENT", []byte("ignored"))...)
		p, err := pushcert.Parse(chain, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.TLS.Certificate) != 2 || !p.Leaf.Equal(id.Cert) {
			t.Fatalf("chain length %d", len(p.TLS.Certificate))
		}
	})
	t.Run("key mismatch", func(t *testing.T) {
		t.Parallel()
		other, err := ca.IssuePush(topic, notBefore)
		if err != nil {
			t.Fatal(err)
		}
		_, otherKey, err := other.PEM()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pushcert.Parse(certPEM, otherKey); !errors.Is(err, pushcert.ErrKeyMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no topic", func(t *testing.T) {
		t.Parallel()
		plain, err := ca.Issue("no-uid", notBefore)
		if err != nil {
			t.Fatal(err)
		}
		c, k, err := plain.PEM()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pushcert.Parse(c, k); !errors.Is(err, pushcert.ErrNoTopic) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid inputs", func(t *testing.T) {
		t.Parallel()
		encrypted := pem.EncodeToMemory(&pem.Block{
			Type:    "RSA PRIVATE KEY",
			Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,00"},
			Bytes:   []byte{1, 2, 3},
		})
		cases := map[string]struct{ cert, key []byte }{
			"empty":                    {nil, nil},
			"garbage cert":             {[]byte("not pem"), keyPEM},
			"wrong first block":        {keyPEM, keyPEM},
			"bad cert der":             {pemBlock("CERTIFICATE", []byte{1, 2, 3}), keyPEM},
			"empty key":                {certPEM, nil},
			"encrypted key headers":    {certPEM, encrypted},
			"encrypted key block type": {certPEM, pemBlock("ENCRYPTED PRIVATE KEY", []byte{1})},
			"unsupported key block":    {certPEM, pemBlock("DSA PRIVATE KEY", []byte{1})},
			"bad pkcs1":                {certPEM, pemBlock("RSA PRIVATE KEY", []byte{1})},
			"bad pkcs8":                {certPEM, pemBlock("PRIVATE KEY", []byte{1})},
			"bad ec":                   {certPEM, pemBlock("EC PRIVATE KEY", []byte{1})},
		}
		for name, tc := range cases {
			if _, err := pushcert.Parse(tc.cert, tc.key); !errors.Is(err, pushcert.ErrInvalid) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
}

func TestTopicFromCert(t *testing.T) {
	t.Parallel()
	cert := &x509.Certificate{Subject: pkix.Name{Names: []pkix.AttributeTypeAndValue{
		{Type: asn1.ObjectIdentifier{2, 5, 4, 3}, Value: "cn"},
		{Type: uid, Value: 42},
		{Type: uid, Value: ""},
		{Type: uid, Value: "com.apple.mgmt.External.abc"},
	}}}
	if got, err := pushcert.TopicFromCert(cert); err != nil || got != "com.apple.mgmt.External.abc" {
		t.Fatalf("topic = %q %v", got, err)
	}
	absent := &x509.Certificate{Subject: pkix.Name{CommonName: "com.apple.mgmt.External.abc"}}
	if _, err := pushcert.TopicFromCert(absent); !errors.Is(err, pushcert.ErrNoTopic) {
		t.Fatalf("absent: %v", err)
	}
	wrong := &x509.Certificate{Subject: pkix.Name{Names: []pkix.AttributeTypeAndValue{{Type: uid, Value: "com.example.other"}}}}
	if _, err := pushcert.TopicFromCert(wrong); !errors.Is(err, pushcert.ErrNoTopic) {
		t.Fatalf("wrong prefix: %v", err)
	}
	if _, err := pushcert.TopicFromCert(nil); !errors.Is(err, pushcert.ErrNoTopic) {
		t.Fatalf("nil: %v", err)
	}
}
