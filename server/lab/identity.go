package lab

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
)

var errLab = errors.New("lab")

// privateFile creates a new identity file with mode 0600, refusing to overwrite an
// existing path.
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

// privateOverwrite writes a private file, replacing any existing content. It is for
// generated, non-identity material such as the container environment.
func privateOverwrite(path string, data []byte) error {
	return wrapError(os.WriteFile(path, data, 0o600))
}

// initialize creates the local identity and credential material required by the lab
// workspace. hosts adds DNS names and IP addresses to the HTTPS leaf beyond loopback, so
// devices on another network can validate the server.
func initialize(dir string, hosts []string) error {
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
	leaf := serverLeaf(serial, hosts)
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

// serverLeaf builds the HTTPS leaf template for loopback plus the configured hosts.
func serverLeaf(serial *big.Int, hosts []string) *x509.Certificate {
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
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		switch {
		case host == "":
		case net.ParseIP(host) != nil:
			leaf.IPAddresses = append(leaf.IPAddresses, net.ParseIP(host))
		default:
			leaf.DNSNames = append(leaf.DNSNames, host)
		}
	}
	return leaf
}

// ReissueTLS replaces the workspace HTTPS leaf with one covering loopback and hosts,
// signed by the retained workspace CA. Devices that already trust the CA keep working, so
// the trust profile does not have to be installed again.
func ReissueTLS(w *Workspace, hosts []string) error {
	dir := w.path("mdm")
	// #nosec G304 -- Fixed filenames in the operator-selected local lab workspace.
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return wrapError(err)
	}
	// #nosec G304 -- Fixed filenames in the operator-selected local lab workspace.
	caKeyPEM, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		return wrapError(err)
	}
	caCert, caKey, err := parseIdentity(caPEM, caKeyPEM)
	if err != nil {
		return err
	}
	tlsKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("%w: TLS key: %w", errLab, err)
	}
	serial, err := ca.Serial()
	if err != nil {
		return fmt.Errorf("%w: serial: %w", errLab, err)
	}
	der, err := x509.CreateCertificate(rand.Reader, serverLeaf(serial, hosts), caCert, tlsKey.Public(), caKey)
	if err != nil {
		return fmt.Errorf("%w: TLS certificate: %w", errLab, err)
	}
	for name, data := range map[string][]byte{
		"tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"tls.key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(tlsKey)}),
	} {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return wrapError(err)
		}
	}
	w.Hosts = hosts
	return w.save()
}

// parseIdentity decodes a PEM certificate and RSA private key pair.
func parseIdentity(certPEM, keyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, nil, fmt.Errorf("%w: workspace CA is not PEM encoded", errLab)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: workspace CA: %w", errLab, err)
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: workspace CA key: %w", errLab, err)
	}
	return cert, key, nil
}
