package dep

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/textproto"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
)

// Token PKI constants.
const (
	// TokenKeyBits is the RSA size of a token PKI key.
	TokenKeyBits = 2048
	// maxTokenFile bounds the .p7m a caller may hand to Unwrap.
	maxTokenFile = 1 << 20
	beginMessage = "-----BEGIN MESSAGE-----"
	endMessage   = "-----END MESSAGE-----"
)

// GenerateTokenPKI creates a 2048-bit RSA key and a self-signed
// certificate with cn as its common name, valid from now for validity,
// ready to upload to the portal as the MDM server's public key.
func GenerateTokenPKI(cn string, validity time.Duration, now time.Time) (*Keypair, error) {
	if cn == "" {
		return nil, fmt.Errorf("%w: empty common name", ErrInvalid)
	}
	if validity <= 0 {
		return nil, fmt.Errorf("%w: validity must be positive", ErrInvalid)
	}
	key, err := rsa.GenerateKey(rand.Reader, TokenKeyBits)
	if err != nil {
		return nil, fmt.Errorf("dep: generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("dep: serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             now.Add(-10 * time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("dep: create certificate: %w", err)
	}
	return &Keypair{
		CertPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:    pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		CreatedAt: now,
	}, nil
}

// Certificate parses CertPEM.
func (k *Keypair) Certificate() (*x509.Certificate, error) {
	block, _ := pem.Decode(k.CertPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: keypair certificate is not PEM", ErrInvalid)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: keypair certificate: %w", ErrInvalid, err)
	}
	return cert, nil
}

// PrivateKey parses KeyPEM (PKCS#1 or PKCS#8 RSA).
func (k *Keypair) PrivateKey() (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(k.KeyPEM)
	if block == nil {
		return nil, fmt.Errorf("%w: keypair key is not PEM", ErrInvalid)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: keypair key: %w", ErrInvalid, err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: keypair key is not RSA", ErrInvalid)
	}
	return key, nil
}

// Unwrap decrypts a server token file as the portal produces it: an
// S/MIME message whose base64 body is a PKCS#7 enveloped-data structure
// encrypted to the keypair's certificate; the plaintext is a second MIME
// message whose body frames the token JSON between BEGIN MESSAGE and END
// MESSAGE lines. The JSON is validated before it is returned, so a
// corrupt file changes nothing.
func Unwrap(p7m []byte, kp *Keypair) (Tokens, error) {
	if kp == nil {
		return Tokens{}, fmt.Errorf("%w: nil keypair", ErrInvalid)
	}
	if len(p7m) > maxTokenFile {
		return Tokens{}, fmt.Errorf("%w: token file exceeds %d bytes", ErrInvalid, maxTokenFile)
	}
	cert, err := kp.Certificate()
	if err != nil {
		return Tokens{}, err
	}
	key, err := kp.PrivateKey()
	if err != nil {
		return Tokens{}, err
	}
	_, body, err := splitMIME(p7m)
	if err != nil {
		return Tokens{}, err
	}
	der, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(body)))
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: token file body is not base64: %w", ErrInvalid, err)
	}
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: token file is not PKCS#7: %w", ErrInvalid, err)
	}
	plain, err := p7.Decrypt(cert, key)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: decrypt token file: %w", ErrInvalid, err)
	}
	_, inner, err := splitMIME(plain)
	if err != nil {
		return Tokens{}, err
	}
	raw, err := unframe(inner)
	if err != nil {
		return Tokens{}, err
	}
	var t Tokens
	if err := Unmarshal(raw, &t); err != nil {
		return Tokens{}, fmt.Errorf("%w: token JSON: %w", ErrInvalid, err)
	}
	if err := t.Validate(); err != nil {
		return Tokens{}, err
	}
	return t, nil
}

// splitMIME reads the header block of a MIME message and returns the
// headers and the raw body.
func splitMIME(msg []byte) (textproto.MIMEHeader, []byte, error) {
	br := bufio.NewReader(bytes.NewReader(msg))
	hdr, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: MIME header: %w", ErrInvalid, err)
	}
	body, err := io.ReadAll(br)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: MIME body: %w", ErrInvalid, err)
	}
	if ct := hdr.Get("Content-Type"); ct != "" {
		if _, _, err := mime.ParseMediaType(ct); err != nil {
			return nil, nil, fmt.Errorf("%w: Content-Type %q: %w", ErrInvalid, ct, err)
		}
	}
	return hdr, body, nil
}

// unframe returns the bytes between the BEGIN MESSAGE and END MESSAGE
// lines.
func unframe(body []byte) ([]byte, error) {
	s := strings.TrimSpace(string(body))
	start := strings.Index(s, beginMessage)
	end := strings.LastIndex(s, endMessage)
	if start < 0 || end < 0 || end < start {
		return nil, fmt.Errorf("%w: token body lacks MESSAGE framing", ErrInvalid)
	}
	inner := strings.TrimSpace(s[start+len(beginMessage) : end])
	if inner == "" {
		return nil, fmt.Errorf("%w: empty token message", ErrInvalid)
	}
	return []byte(inner), nil
}

// Wrap produces a server token file for cert the way the portal does:
// the JSON framed by MESSAGE lines inside a MIME message, PKCS#7
// enveloped to cert, base64 in an S/MIME wrapper. It exists so the
// exchange can be tested end to end; tokenJSON must be a valid Tokens
// document.
func Wrap(tokenJSON []byte, cert *x509.Certificate) ([]byte, error) {
	if cert == nil {
		return nil, fmt.Errorf("%w: nil certificate", ErrInvalid)
	}
	var t Tokens
	if err := Unmarshal(tokenJSON, &t); err != nil {
		return nil, err
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	var inner bytes.Buffer
	inner.WriteString("Content-Type: text/plain;charset=US-ASCII\r\nContent-Transfer-Encoding: 7bit\r\n\r\n")
	inner.WriteString(beginMessage + "\n")
	inner.Write(tokenJSON)
	inner.WriteString("\n" + endMessage + "\n")
	enveloped, err := pkcs7.Encrypt(inner.Bytes(), []*x509.Certificate{cert})
	if err != nil {
		return nil, fmt.Errorf("dep: encrypt token: %w", err)
	}
	var out bytes.Buffer
	out.WriteString("Content-Type: application/pkcs7-mime; name=\"smime.p7m\"; smime-type=enveloped-data\r\n")
	out.WriteString("Content-Transfer-Encoding: base64\r\n")
	out.WriteString("Content-Disposition: attachment; filename=\"smime.p7m\"\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString(enveloped)
	for len(enc) > 64 {
		out.WriteString(enc[:64] + "\r\n")
		enc = enc[64:]
	}
	out.WriteString(enc + "\r\n")
	return out.Bytes(), nil
}
