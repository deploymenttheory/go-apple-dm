package dep_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/dep/deptest"
)

// keypair generates a token PKI keypair for tests.
func keypair(t *testing.T) *dep.Keypair {
	t.Helper()
	kp, err := dep.GenerateTokenPKI("go-apple-mdm test", 24*time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func TestTokenPKI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("RoundTrip", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, withoutAccount)
		kp := keypair(t)
		cert, err := kp.Certificate()
		if err != nil {
			t.Fatal(err)
		}
		if cert.Subject.CommonName != "go-apple-mdm test" || cert.PublicKeyAlgorithm != x509.RSA || !cert.NotAfter.Equal(t0.Add(24*time.Hour)) {
			t.Fatalf("certificate: %+v", cert.Subject)
		}
		key, err := kp.PrivateKey()
		if err != nil || key.N.BitLen() != dep.TokenKeyBits {
			t.Fatalf("key: %v", err)
		}
		p7m, err := f.srv.TokenP7M(cert)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(p7m, []byte("Content-Type: application/pkcs7-mime")) {
			t.Fatalf("p7m header: %.80s", p7m)
		}
		got, err := dep.Unwrap(p7m, kp)
		if err != nil {
			t.Fatal(err)
		}
		want := f.srv.Tokens()
		if got.ConsumerKey != want.ConsumerKey || got.ConsumerSecret != want.ConsumerSecret || got.AccessToken != want.AccessToken || got.AccessSecret != want.AccessSecret || !got.AccessTokenExpiry.Equal(*want.AccessTokenExpiry) {
			t.Fatalf("unwrapped %+v, want %+v", got, want)
		}
		// The full exchange: stage, import, upstage, and a validated account.
		if err := f.store.PutKeypair(ctx, acct, dep.StageStaged, kp); err != nil {
			t.Fatal(err)
		}
		detail, err := f.client.ImportToken(ctx, acct, p7m, dep.ImportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if detail.OrgName != "Deployment Theory" {
			t.Fatalf("detail: %+v", detail)
		}
		a := f.account()
		if a.Tokens() != want && (a.ConsumerKey != want.ConsumerKey || a.AccessSecret != want.AccessSecret) {
			t.Fatalf("account tokens: %+v", a.Tokens())
		}
		if a.ServerUUID != "SERVER-UUID-DEPTEST" || a.State != (dep.AccountState{}) {
			t.Fatalf("account: %+v", a)
		}
		if _, err := f.store.Keypair(ctx, acct, dep.StageStaged); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("staged keypair survived the import")
		}
		current, err := f.store.Keypair(ctx, acct, dep.StageCurrent)
		if err != nil || !bytes.Equal(current.KeyPEM, kp.KeyPEM) {
			t.Fatalf("current keypair: %v", err)
		}
		if tok, _ := f.store.Session(ctx, acct); tok == "" {
			t.Fatal("session not persisted")
		}
		if _, err := f.client.Account(ctx, acct); err != nil || f.srv.SessionCalls() != 1 {
			t.Fatalf("after import: %v sessions=%d", err, f.srv.SessionCalls())
		}
		// A renewed token against the same certificate imports through the
		// current keypair when nothing is staged.
		if _, err := f.client.ImportToken(ctx, acct, p7m, dep.ImportOptions{}); err != nil {
			t.Fatalf("renewal: %v", err)
		}
		if _, err := f.store.Keypair(ctx, acct, dep.StageCurrent); err != nil {
			t.Fatal("current keypair lost on renewal")
		}
		// A PKCS#8 key parses as well.
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		pk8 := &dep.Keypair{CertPEM: kp.CertPEM, KeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})}
		if _, err := dep.Unwrap(p7m, pk8); err != nil {
			t.Fatalf("PKCS#8: %v", err)
		}
	})

	t.Run("CorruptLeavesCurrentKeypair", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		current, staged := keypair(t), keypair(t)
		if err := f.store.PutKeypair(ctx, acct, dep.StageCurrent, current); err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutKeypair(ctx, acct, dep.StageStaged, staged); err != nil {
			t.Fatal(err)
		}
		before := f.account()
		stagedCert, _ := staged.Certificate()
		good, err := f.srv.TokenP7M(stagedCert)
		if err != nil {
			t.Fatal(err)
		}
		corrupt := bytes.Replace(good, []byte("\r\n\r\n"), []byte("\r\n\r\nAAAA"), 1)
		for name, p7m := range map[string][]byte{
			"garbage":         []byte("not a token file"),
			"no body":         []byte("Content-Type: application/pkcs7-mime\r\n\r\n"),
			"bad base64":      []byte("Content-Type: application/pkcs7-mime\r\n\r\n!!!!\r\n"),
			"bad media type":  []byte("Content-Type: ;;;\r\n\r\nAAAA\r\n"),
			"not pkcs7":       []byte("Content-Type: application/pkcs7-mime\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte("plain")) + "\r\n"),
			"corrupt payload": corrupt,
			"too large":       bytes.Repeat([]byte("A"), 2<<20),
		} {
			if _, err := f.client.ImportToken(ctx, acct, p7m, dep.ImportOptions{}); !errors.Is(err, dep.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Encrypted to another certificate: undecryptable.
		otherCert, _ := current.Certificate()
		wrongKey, err := f.srv.TokenP7M(otherCert)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.client.ImportToken(ctx, acct, wrongKey, dep.ImportOptions{}); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("wrong key: %v", err)
		}
		// Valid envelope, bad contents: no framing, empty message, bad JSON,
		// incomplete tokens.
		for name, inner := range map[string]string{
			"no framing":    "Content-Type: text/plain\r\n\r\n{\"consumer_key\":\"x\"}\r\n",
			"empty message": "Content-Type: text/plain\r\n\r\n-----BEGIN MESSAGE-----\n-----END MESSAGE-----\n",
			"bad json":      "Content-Type: text/plain\r\n\r\n-----BEGIN MESSAGE-----\n{oops\n-----END MESSAGE-----\n",
			"incomplete":    "Content-Type: text/plain\r\n\r\n-----BEGIN MESSAGE-----\n{\"consumer_key\":\"x\"}\n-----END MESSAGE-----\n",
			"no header":     "-----BEGIN MESSAGE-----\n{}\n-----END MESSAGE-----\n",
		} {
			enveloped, err := pkcs7.Encrypt([]byte(inner), []*x509.Certificate{stagedCert})
			if err != nil {
				t.Fatal(err)
			}
			p7m := []byte("Content-Type: application/pkcs7-mime\r\n\r\n" + base64.StdEncoding.EncodeToString(enveloped) + "\r\n")
			if _, err := f.client.ImportToken(ctx, acct, p7m, dep.ImportOptions{}); !errors.Is(err, dep.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Nothing changed: both keypairs, the account, and the session.
		cur, err := f.store.Keypair(ctx, acct, dep.StageCurrent)
		if err != nil || !bytes.Equal(cur.KeyPEM, current.KeyPEM) {
			t.Fatalf("current keypair changed: %v", err)
		}
		stg, err := f.store.Keypair(ctx, acct, dep.StageStaged)
		if err != nil || !bytes.Equal(stg.KeyPEM, staged.KeyPEM) {
			t.Fatalf("staged keypair changed: %v", err)
		}
		if after := f.account(); after.UpdatedAt != before.UpdatedAt || after.ConsumerKey != before.ConsumerKey {
			t.Fatal("account changed by a corrupt token")
		}
		if len(f.srv.Requests()) != 0 {
			t.Fatal("a corrupt token reached the service")
		}
		// Validation failures after a good unwrap also leave the keypairs.
		f.srv.SetTermsNotSigned(true)
		if _, err := f.client.ImportToken(ctx, acct, good, dep.ImportOptions{}); !errors.Is(err, dep.ErrTermsNotSigned) {
			t.Fatalf("terms: %v", err)
		}
		if _, err := f.store.Keypair(ctx, acct, dep.StageStaged); err != nil {
			t.Fatal("staged keypair upstaged despite validation failing")
		}
		f.srv.SetTermsNotSigned(false)
		// A store failure during the final transaction leaves the staged
		// pair for a retry.
		failing := &deptest.Failing{Store: f.store, Fail: map[string]error{"UpstageKeypair": errors.New("readonly")}}
		c, _ := dep.NewClient(dep.ClientConfig{Store: failing, BaseURL: f.srv.URL(), Clock: f.clk})
		if _, err := c.ImportToken(ctx, acct, good, dep.ImportOptions{}); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("upstage failure: %v", err)
		}
		if _, err := f.store.Keypair(ctx, acct, dep.StageStaged); err != nil {
			t.Fatal("staged keypair lost despite the transaction failing")
		}
		for _, method := range []string{"PutAccount", "SetSession", "GetAccount", "Keypair"} {
			failing.Fail = map[string]error{method: errors.New("readonly")}
			if _, err := c.ImportToken(ctx, acct, good, dep.ImportOptions{}); err == nil || !strings.Contains(err.Error(), "readonly") {
				t.Fatalf("%s failure: %v", method, err)
			}
		}
		if _, err := f.client.ImportToken(ctx, "", good, dep.ImportOptions{}); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty name: %v", err)
		}
		if _, err := f.client.ImportToken(ctx, "nokeys", good, dep.ImportOptions{}); !errors.Is(err, dep.ErrNotFound) {
			t.Fatalf("no keypair: %v", err)
		}
	})

	t.Run("ConsumerKeyMismatch", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.putAccount(func(a *dep.Account) { a.ConsumerKey = "CK_previous" })
		kp := keypair(t)
		cert, _ := kp.Certificate()
		p7m, err := f.srv.TokenP7M(cert)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutKeypair(ctx, acct, dep.StageStaged, kp); err != nil {
			t.Fatal(err)
		}
		if _, err := f.client.ImportToken(ctx, acct, p7m, dep.ImportOptions{}); !errors.Is(err, dep.ErrConsumerKeyMismatch) {
			t.Fatalf("err = %v", err)
		}
		if f.account().ConsumerKey != "CK_previous" || len(f.srv.Requests()) != 0 {
			t.Fatal("mismatch changed the account or reached the service")
		}
		if _, err := f.client.ImportToken(ctx, acct, p7m, dep.ImportOptions{Force: true}); err != nil {
			t.Fatalf("forced: %v", err)
		}
		if f.account().ConsumerKey != f.srv.Tokens().ConsumerKey {
			t.Fatal("forced import did not replace the tokens")
		}
	})

	t.Run("Helpers", func(t *testing.T) {
		t.Parallel()
		if _, err := dep.GenerateTokenPKI("", time.Hour, t0); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty cn: %v", err)
		}
		if _, err := dep.GenerateTokenPKI("cn", 0, t0); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("zero validity: %v", err)
		}
		kp := keypair(t)
		cert, _ := kp.Certificate()
		if _, err := dep.Unwrap([]byte("x"), nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("nil keypair: %v", err)
		}
		bad := &dep.Keypair{CertPEM: []byte("nope"), KeyPEM: kp.KeyPEM}
		if _, err := bad.Certificate(); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("bad cert PEM: %v", err)
		}
		if _, err := dep.Unwrap([]byte("x"), bad); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("unwrap with bad cert: %v", err)
		}
		badDER := &dep.Keypair{CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})}
		if _, err := badDER.Certificate(); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("bad cert DER: %v", err)
		}
		for name, key := range map[string][]byte{
			"not pem": []byte("nope"),
			"garbage": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte{1, 2, 3}}),
			"not rsa": ecdsaPKCS8(t),
		} {
			k := &dep.Keypair{CertPEM: kp.CertPEM, KeyPEM: key}
			if _, err := k.PrivateKey(); !errors.Is(err, dep.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
			if _, err := dep.Unwrap([]byte("x"), k); !errors.Is(err, dep.ErrInvalid) {
				t.Errorf("unwrap %s: %v", name, err)
			}
		}
		if _, err := dep.Wrap([]byte(`{"consumer_key":"a","consumer_secret":"b","access_token":"c","access_secret":"d"}`), nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("nil cert: %v", err)
		}
		if _, err := dep.Wrap([]byte(`{bad`), cert); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("bad JSON: %v", err)
		}
		if _, err := dep.Wrap([]byte(`{"consumer_key":"a"}`), cert); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("incomplete tokens: %v", err)
		}
		// Tokens.Validate names each missing field.
		for _, tk := range []dep.Tokens{{}, {ConsumerKey: "a"}, {ConsumerKey: "a", ConsumerSecret: "b"}, {ConsumerKey: "a", ConsumerSecret: "b", AccessToken: "c"}} {
			if err := tk.Validate(); !errors.Is(err, dep.ErrInvalid) {
				t.Errorf("%+v: %v", tk, err)
			}
		}
		// A Keypair.Clone shares nothing.
		clone := kp.Clone()
		clone.KeyPEM[0] = 'X'
		if kp.KeyPEM[0] == 'X' {
			t.Fatal("Clone shares KeyPEM")
		}
	})
}

// ecdsaPKCS8 returns a PKCS#8 PEM of a non-RSA key.
func ecdsaPKCS8(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func FuzzUnwrap(f *testing.F) {
	kp, err := dep.GenerateTokenPKI("fuzz", time.Hour, time.Now())
	if err != nil {
		f.Fatal(err)
	}
	cert, _ := kp.Certificate()
	good, err := dep.Wrap([]byte(`{"consumer_key":"a","consumer_secret":"b","access_token":"c","access_secret":"d","access_token_expiry":"2030-01-01T00:00:00Z"}`), cert)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte("Content-Type: application/pkcs7-mime\r\n\r\nAAAA\r\n"))
	f.Add([]byte(""))
	f.Add([]byte("-----BEGIN MESSAGE-----"))
	f.Fuzz(func(t *testing.T, p7m []byte) {
		tokens, err := dep.Unwrap(p7m, kp)
		if err == nil && tokens.Validate() != nil {
			t.Fatalf("Unwrap returned invalid tokens without an error: %+v", tokens)
		}
		if err != nil && !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("unexpected error class: %v", err)
		}
	})
}
