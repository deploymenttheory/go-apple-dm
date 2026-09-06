package jose

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
)

// Key types this package understands, as they appear in the kty member.
const (
	keyTypeEC  = "EC"
	keyTypeRSA = "RSA"
)

// Curve names as they appear in the crv member (RFC 7518 section 6.2.1.1).
const (
	curveP256 = "P-256"
	curveP384 = "P-384"
	curveP521 = "P-521"
)

// minRSABits is the shortest RSA modulus this package will verify with. An
// ACME account or certificate key below it is not an interoperability
// problem to be worked around, it is a key we decline to trust.
const minRSABits = 2048

// JWK is a public key in JSON Web Key form, holding only the members RFC
// 7638 feeds into a thumbprint plus the kty and crv that identify the key.
// Nothing else is kept: an ACME server has no use for kid, use or alg on a
// key that arrives inside a protected header, and carrying them would only
// invite code to trust a value the signature does not pin down.
//
// Coordinates and integers are unpadded base64url as they appear on the
// wire. Use Public to turn one into a usable key; it validates as it goes.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}

// curveByName maps a crv member to its curve and the fixed width, in bytes,
// that RFC 7518 section 6.2.1.2 requires of the x and y coordinates. The
// width is ceil(bits/8), which is 66 rather than 65 for P-521.
func curveByName(name string) (elliptic.Curve, int, bool) {
	switch name {
	case curveP256:
		return elliptic.P256(), 32, true
	case curveP384:
		return elliptic.P384(), 48, true
	case curveP521:
		return elliptic.P521(), 66, true
	default:
		return nil, 0, false
	}
}

// curveName is the inverse of curveByName for the curves we support.
func curveName(c elliptic.Curve) (string, int, bool) {
	switch c {
	case elliptic.P256():
		return curveP256, 32, true
	case elliptic.P384():
		return curveP384, 48, true
	case elliptic.P521():
		return curveP521, 66, true
	default:
		return "", 0, false
	}
}

// JWKFromPublic converts an EC (P-256, P-384 or P-521) or RSA public key
// into its JWK form. Any other key type, an EC key on a curve we do not
// support, and an RSA key below minRSABits are ErrKey.
func JWKFromPublic(pub crypto.PublicKey) (*JWK, error) {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		name, size, ok := curveName(k.Curve)
		if !ok {
			return nil, fmt.Errorf("%w: unsupported elliptic curve", ErrKey)
		}
		x, err := fixedWidth(k.X, size)
		if err != nil {
			return nil, fmt.Errorf("%w: x coordinate: %w", ErrKey, err)
		}
		y, err := fixedWidth(k.Y, size)
		if err != nil {
			return nil, fmt.Errorf("%w: y coordinate: %w", ErrKey, err)
		}
		return &JWK{
			Kty: keyTypeEC,
			Crv: name,
			X:   base64.RawURLEncoding.EncodeToString(x),
			Y:   base64.RawURLEncoding.EncodeToString(y),
		}, nil
	case *rsa.PublicKey:
		if err := checkRSASize(k); err != nil {
			return nil, err
		}
		if k.E <= 0 {
			return nil, fmt.Errorf("%w: non-positive RSA exponent", ErrKey)
		}
		return &JWK{
			Kty: keyTypeRSA,
			N:   base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(new(big.Int).SetInt64(int64(k.E)).Bytes()),
		}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported public key type %T", ErrKey, pub)
	}
}

// errCoordinateRange reports a coordinate that cannot be written in the
// width its curve demands, which means it was never a point on that curve.
var errCoordinateRange = errors.New("outside the range of the curve")

// fixedWidth renders v big-endian and left-padded to size bytes. It refuses
// rather than panicking, because callers reach it with keys from the wire.
func fixedWidth(v *big.Int, size int) ([]byte, error) {
	if v == nil || v.Sign() < 0 || v.BitLen() > size*8 {
		return nil, errCoordinateRange
	}
	return v.FillBytes(make([]byte, size)), nil
}

// checkRSASize rejects a modulus we consider too short to trust.
func checkRSASize(k *rsa.PublicKey) error {
	if k.N == nil || k.N.Sign() <= 0 {
		return fmt.Errorf("%w: empty RSA modulus", ErrKey)
	}
	if bits := k.N.BitLen(); bits < minRSABits {
		return fmt.Errorf("%w: RSA modulus of %d bits, minimum is %d", ErrKey, bits, minRSABits)
	}
	return nil
}

// Public turns the JWK into a crypto.PublicKey, validating everything it
// reads: the members decode as unpadded base64url, EC coordinates are
// exactly the width the curve requires and the point lies on the curve, and
// an RSA key is long enough and has a usable exponent. Every failure wraps
// ErrKey.
func (k *JWK) Public() (crypto.PublicKey, error) {
	if k == nil {
		return nil, fmt.Errorf("%w: nil key", ErrKey)
	}
	switch k.Kty {
	case keyTypeEC:
		return k.ecPublic()
	case keyTypeRSA:
		return k.rsaPublic()
	default:
		return nil, fmt.Errorf("%w: unsupported key type %q", ErrKey, k.Kty)
	}
}

func (k *JWK) ecPublic() (*ecdsa.PublicKey, error) {
	curve, size, ok := curveByName(k.Crv)
	if !ok {
		return nil, fmt.Errorf("%w: unsupported curve %q", ErrKey, k.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("%w: x is not unpadded base64url: %w", ErrKey, err)
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("%w: y is not unpadded base64url: %w", ErrKey, err)
	}
	// RFC 7518 section 6.2.1.2 fixes the octet length of each coordinate at
	// the curve size, so a short or long coordinate is malformed rather than
	// something to be left-padded into shape. Being strict here also means
	// Thumbprint cannot produce two different digests for one key.
	if len(x) != size || len(y) != size {
		return nil, fmt.Errorf("%w: %s needs %d-byte coordinates, got x=%d y=%d", ErrKey, k.Crv, size, len(x), len(y))
	}
	// The uncompressed SEC 1 encoding is 0x04 || x || y, and parsing it is
	// what checks the point actually lies on the curve.
	point := make([]byte, 0, 1+2*size)
	point = append(point, 0x04)
	point = append(point, x...)
	point = append(point, y...)
	pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
	if err != nil {
		return nil, fmt.Errorf("%w: point is not on %s: %w", ErrKey, k.Crv, err)
	}
	return pub, nil
}

func (k *JWK) rsaPublic() (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("%w: n is not unpadded base64url: %w", ErrKey, err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("%w: e is not unpadded base64url: %w", ErrKey, err)
	}
	if len(n) == 0 {
		return nil, fmt.Errorf("%w: empty RSA modulus", ErrKey)
	}
	// A four-byte cap keeps the exponent inside an int on every platform and
	// is far more than any real key needs; 65537 is three bytes.
	if len(e) == 0 || len(e) > 4 {
		return nil, fmt.Errorf("%w: RSA exponent of %d bytes", ErrKey, len(e))
	}
	pub := &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}
	if err := checkRSASize(pub); err != nil {
		return nil, err
	}
	if pub.E < 3 || pub.E%2 == 0 {
		return nil, fmt.Errorf("%w: unusable RSA exponent %d", ErrKey, pub.E)
	}
	return pub, nil
}

// Thumbprint is the RFC 7638 SHA-256 thumbprint of the key, unpadded
// base64url. ACME builds a key authorisation from it, so it has to be the
// same digest every other implementation computes: the required members
// only, lexicographically ordered, no whitespace, and coordinates at their
// fixed width. The key is validated first and re-encoded from the parsed
// form, so a JWK that spelled a coordinate unusually cannot change the
// digest; a malformed key is ErrKey rather than a thumbprint of nonsense.
func (k *JWK) Thumbprint() (string, error) {
	pub, err := k.Public()
	if err != nil {
		return "", err
	}
	canonical, err := JWKFromPublic(pub)
	if err != nil {
		return "", err
	}
	var required any
	switch canonical.Kty {
	case keyTypeEC:
		// Field order is the lexicographic order RFC 7638 section 3.2 asks
		// for, and json/v2 emits struct members in declaration order.
		required = struct {
			Crv string `json:"crv"`
			Kty string `json:"kty"`
			X   string `json:"x"`
			Y   string `json:"y"`
		}{canonical.Crv, canonical.Kty, canonical.X, canonical.Y}
	default:
		required = struct {
			E   string `json:"e"`
			Kty string `json:"kty"`
			N   string `json:"n"`
		}{canonical.E, canonical.Kty, canonical.N}
	}
	encoded, err := json.Marshal(required)
	if err != nil {
		return "", fmt.Errorf("%w: encoding thumbprint input: %w", ErrKey, err)
	}
	sum := sha256.Sum256(encoded)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}
