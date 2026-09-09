package bench

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/deploymenttheory/go-apple-dm/pki/ca"
)

var errLab = errors.New("bench")

func privateFile(path string, data []byte) error {
	f, err := os.OpenFile(
		path,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	) // #nosec G304 -- explicit local lab path
	if err != nil {
		return fmt.Errorf("%w: create %s: %w", errLab, path, err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

func initialize(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: directory: %w", errLab, err)
	}
	for _, name := range []string{"ca.pem", "ca.key", "tls.pem", "tls.key", "admin-token", "storage-key", "scep-challenge"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf(
				"%w: initialization requires absent output files; preserve existing identities",
				errLab,
			)
		}
	}
	cert, key, err := ca.NewSelfSigned(
		ca.SelfSignedOptions{
			Subject:  pkix.Name{CommonName: "DeviceWeave Local MDM Lab CA"},
			Validity: 365 * 24 * time.Hour,
		},
	)
	if err != nil {
		return fmt.Errorf("%w: CA: %w", errLab, err)
	}
	tlsKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("%w: TLS key: %w", errLab, err)
	}
	serial, err := ca.Serial()
	if err != nil {
		return fmt.Errorf("%w: serial: %w", errLab, err)
	}
	leaf := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, cert, tlsKey.Public(), key)
	if err != nil {
		return fmt.Errorf("%w: TLS certificate: %w", errLab, err)
	}
	files := map[string][]byte{
		"ca.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		"ca.key": pem.EncodeToMemory(
			&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
		),
		"tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"tls.key": pem.EncodeToMemory(
			&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(tlsKey)},
		),
	}
	for _, name := range []string{"admin-token", "storage-key", "scep-challenge"} {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("%w: random: %w", errLab, err)
		}
		files[name] = []byte(hex.EncodeToString(b))
	}
	for name, data := range files {
		if err := privateFile(filepath.Join(dir, name), data); err != nil {
			return wrapError(err)
		}
	}

	return nil
}
