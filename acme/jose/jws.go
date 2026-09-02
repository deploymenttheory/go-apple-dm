package jose

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
	"slices"
)

// Signature algorithms this package accepts. RFC 8555 section 6.2 requires a
// server to support ES256 and RS256 and permits more; these six are the
// asymmetric algorithms Apple's ACME clients and the common CAs use. MAC
// algorithms are absent on purpose, and so is "none": on an ACME request
// either would mean the sender's key never took part.
const (
	ES256 = "ES256"
	ES384 = "ES384"
	ES512 = "ES512"
	RS256 = "RS256"
	RS384 = "RS384"
	RS512 = "RS512"
)

// algorithms is the accepted set in the order Algorithms reports it.
var algorithms = []string{ES256, ES384, ES512, RS256, RS384, RS512}

// Algorithms returns the algorithms this package verifies, for the
// "algorithms" field of a badSignatureAlgorithm problem document (RFC 8555
// section 6.2). The caller gets its own copy.
func Algorithms() []string { return slices.Clone(algorithms) }

// MaxBody is the largest request body Parse will look at. A transport
// handler should already be limiting the body it reads; this is the second
// line of the same defence, so that a body which reaches Parse by some
// other route cannot make it allocate without bound.
const MaxBody = 256 << 10 // 256 KiB

// Errors this package returns. Every failure wraps exactly one of them, so
// a handler can map them onto ACME problem types with errors.Is: ErrParse
// and ErrHeader are "malformed", ErrAlgorithm is "badSignatureAlgorithm",
// ErrSignature and ErrKey are "malformed" or "unauthorized" depending on
// what the caller was doing.
var (
	ErrParse     = errors.New("jose: malformed JWS")
	ErrAlgorithm = errors.New("jose: unsupported algorithm")
	ErrSignature = errors.New("jose: signature does not verify")
	ErrKey       = errors.New("jose: unsupported or malformed key")
	ErrHeader    = errors.New("jose: invalid protected header")
)

// Header is the protected header of an ACME request. Everything a handler
// is allowed to trust lives here, because everything here is covered by the
// signature. Exactly one of KeyID and JWK is set: a JWK on the requests
// that create or recover an account, a kid on every request afterwards.
type Header struct {
	Algorithm string // alg
	KeyID     string // kid
	JWK       *JWK   // jwk
	Nonce     string // nonce
	URL       string // url
}

// JWS is one parsed JWS in the flattened JSON serialisation.
//
// Protected is the base64url text of the protected header exactly as it
// arrived, not a re-encoding of Header, because that text is half of what
// the signature covers. Payload is decoded and is empty for a POST-as-GET.
//
// A JWS is only meaningful when it came from Parse; Verify refuses a
// zero value rather than inventing a signing input for it.
type JWS struct {
	Header    Header
	Payload   []byte
	Protected []byte
	Signature []byte

	// signingInput is protected || "." || payload, both as received. RFC
	// 7515 section 5.2 step 8 signs those octets, so we keep them rather
	// than re-encoding and risking a byte-for-byte difference.
	signingInput []byte
}

// flattened mirrors the flattened JSON serialisation of RFC 7515 section
// 7.2.2. The three members we want are pointers so that Parse can tell a
// member that was absent from one that was present and empty, which is the
// difference between a detached payload and a POST-as-GET. The two members
// we refuse are declared so we can name them in the error; anything else is
// rejected by RejectUnknownMembers.
type flattened struct {
	Protected  *string        `json:"protected"`
	Payload    *string        `json:"payload"`
	Signature  *string        `json:"signature"`
	Header     jsontext.Value `json:"header"`
	Signatures jsontext.Value `json:"signatures"`
}

// protectedHeader is the subset of the protected header ACME defines, plus
// the two members whose presence must be refused. Unknown members are
// tolerated, as RFC 7515 section 4 requires of any header parameter that is
// not listed in crit, and crit itself is refused below.
type protectedHeader struct {
	Alg   *string        `json:"alg"`
	Kid   *string        `json:"kid"`
	JWK   *JWK           `json:"jwk"`
	Nonce *string        `json:"nonce"`
	URL   *string        `json:"url"`
	B64   *bool          `json:"b64"`
	Crit  jsontext.Value `json:"crit"`
}

// Parse reads one flattened-serialisation JWS and checks its shape against
// what RFC 8555 section 6.2 allows. It does not verify the signature; that
// needs a key, which the caller finds from the kid or the jwk this returns.
//
// The rules are deliberately unforgiving. A general serialisation, an
// unprotected header, a detached payload, an unknown top-level member, an
// unencoded payload (RFC 7797 b64), a crit member, a missing url or nonce,
// both or neither of jwk and kid, padded base64: each of those is a
// malformed request rather than a variation to be accommodated, and letting
// one through would mean a handler reading a value the signature never
// covered.
func Parse(body []byte) (*JWS, error) {
	if len(body) > MaxBody {
		return nil, fmt.Errorf("%w: body of %d bytes exceeds the %d byte limit", ErrParse, len(body), MaxBody)
	}
	var f flattened
	if err := json.Unmarshal(body, &f, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParse, err)
	}
	if len(f.Signatures) > 0 {
		return nil, fmt.Errorf("%w: general serialisation (signatures member); ACME requires the flattened form", ErrParse)
	}
	if len(f.Header) > 0 {
		return nil, fmt.Errorf("%w: unprotected header member; ACME requires every field to be protected", ErrParse)
	}
	if f.Protected == nil || *f.Protected == "" {
		return nil, fmt.Errorf("%w: missing protected header", ErrParse)
	}
	if f.Payload == nil {
		return nil, fmt.Errorf("%w: detached payload; the payload member is required", ErrParse)
	}
	if f.Signature == nil || *f.Signature == "" {
		return nil, fmt.Errorf("%w: missing signature", ErrParse)
	}

	protectedJSON, err := base64.RawURLEncoding.DecodeString(*f.Protected)
	if err != nil {
		return nil, fmt.Errorf("%w: protected is not unpadded base64url: %w", ErrParse, err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(*f.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not unpadded base64url: %w", ErrParse, err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(*f.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not unpadded base64url: %w", ErrParse, err)
	}

	header, err := parseProtected(protectedJSON)
	if err != nil {
		return nil, err
	}

	signingInput := make([]byte, 0, len(*f.Protected)+1+len(*f.Payload))
	signingInput = append(signingInput, *f.Protected...)
	signingInput = append(signingInput, '.')
	signingInput = append(signingInput, *f.Payload...)

	return &JWS{
		Header:       *header,
		Payload:      payload,
		Protected:    []byte(*f.Protected),
		Signature:    signature,
		signingInput: signingInput,
	}, nil
}

// parseProtected decodes and validates the protected header's JSON.
func parseProtected(raw []byte) (*Header, error) {
	var p protectedHeader
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHeader, err)
	}
	// RFC 7797's unencoded payload option changes what the signature covers,
	// and ACME has no use for it; a b64 of false is a request we cannot
	// safely interpret rather than one we should try to.
	if p.B64 != nil && !*p.B64 {
		return nil, fmt.Errorf("%w: b64 is false; unencoded payloads (RFC 7797) are not accepted", ErrHeader)
	}
	// crit names extensions the sender insists we understand. We understand
	// none of them, and RFC 7515 section 4.1.11 says that means we must
	// reject the JWS.
	if len(p.Crit) > 0 {
		return nil, fmt.Errorf("%w: crit is present and no extensions are supported", ErrHeader)
	}
	if p.Alg == nil {
		return nil, fmt.Errorf("%w: missing alg", ErrHeader)
	}
	if !slices.Contains(algorithms, *p.Alg) {
		return nil, fmt.Errorf("%w: alg %q is not one of %v", ErrAlgorithm, *p.Alg, algorithms)
	}
	if (p.Kid == nil) == (p.JWK == nil) {
		return nil, fmt.Errorf("%w: exactly one of jwk and kid is required", ErrHeader)
	}
	if p.Kid != nil && *p.Kid == "" {
		return nil, fmt.Errorf("%w: empty kid", ErrHeader)
	}
	if p.URL == nil || *p.URL == "" {
		return nil, fmt.Errorf("%w: missing url", ErrHeader)
	}
	if p.Nonce == nil || *p.Nonce == "" {
		return nil, fmt.Errorf("%w: missing nonce", ErrHeader)
	}
	// Validate an embedded key here so that a caller holding a parsed JWS
	// can use Header.JWK without wondering whether it decodes.
	if p.JWK != nil {
		if _, err := p.JWK.Public(); err != nil {
			return nil, err
		}
	}
	h := &Header{Algorithm: *p.Alg, JWK: p.JWK, URL: *p.URL, Nonce: *p.Nonce}
	if p.Kid != nil {
		h.KeyID = *p.Kid
	}
	return h, nil
}

// PayloadIsEmpty reports whether the payload member was the empty string,
// which is how RFC 8555 section 6.3 writes a POST-as-GET.
func (j *JWS) PayloadIsEmpty() bool { return len(j.Payload) == 0 }

// hashFor returns the digest algorithm alg is defined over.
func hashFor(alg string) (crypto.Hash, error) {
	switch alg {
	case ES256, RS256:
		return crypto.SHA256, nil
	case ES384, RS384:
		return crypto.SHA384, nil
	case ES512, RS512:
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("%w: alg %q", ErrAlgorithm, alg)
	}
}

// curveForAlg gives the one curve each ECDSA alg is defined over in RFC
// 7518 section 3.4, together with the width of r and s on it.
func curveForAlg(alg string) (string, int, bool) {
	switch alg {
	case ES256:
		return curveP256, 32, true
	case ES384:
		return curveP384, 48, true
	case ES512:
		return curveP521, 66, true
	default:
		return "", 0, false
	}
}

// Verify checks the signature over the exact octets that arrived. It does
// not look at the JWS's own jwk member: the caller decides which key ought
// to have signed, whether that is the embedded key of a new account or the
// stored key the kid resolved to.
func (j *JWS) Verify(pub crypto.PublicKey) error {
	if j == nil || len(j.signingInput) == 0 {
		return fmt.Errorf("%w: JWS did not come from Parse", ErrParse)
	}
	hash, err := hashFor(j.Header.Algorithm)
	if err != nil {
		return err
	}
	digester := hash.New()
	// A hash.Hash never reports an error from Write, so there is nothing to
	// handle and no branch worth carrying.
	_, _ = digester.Write(j.signingInput)
	digest := digester.Sum(nil)

	if wantCurve, size, isEC := curveForAlg(j.Header.Algorithm); isEC {
		return j.verifyECDSA(pub, digest, wantCurve, size)
	}
	return j.verifyRSA(pub, hash, digest)
}

func (j *JWS) verifyECDSA(pub crypto.PublicKey, digest []byte, wantCurve string, size int) error {
	key, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: alg %s needs an ECDSA key, got %T", ErrAlgorithm, j.Header.Algorithm, pub)
	}
	haveCurve, _, ok := curveName(key.Curve)
	if !ok {
		return fmt.Errorf("%w: unsupported elliptic curve", ErrKey)
	}
	// RFC 7518 section 3.4 pairs each ECDSA alg with exactly one curve, so a
	// P-384 key presented as ES256 is a mismatched request, not a signature
	// to be attempted with a different digest.
	if haveCurve != wantCurve {
		return fmt.Errorf("%w: alg %s requires %s, key is on %s", ErrAlgorithm, j.Header.Algorithm, wantCurve, haveCurve)
	}
	for _, rs := range candidateSplits(j.Signature, size) {
		r := new(big.Int).SetBytes(rs[0])
		s := new(big.Int).SetBytes(rs[1])
		if ecdsa.Verify(key, digest, r, s) {
			return nil
		}
	}
	return fmt.Errorf("%w: ECDSA signature over %d bytes", ErrSignature, len(j.Signature))
}

// maxOmittedZeroBytes bounds the interoperability workaround below. Each
// omitted byte is a leading zero, which happens to r or s with probability
// 1/256, so three missing bytes is already about a one in sixteen million
// signature and anything beyond it is far more likely to be corruption than
// a client bug. The bound also bounds the work: at most
// maxOmittedZeroBytes+1 candidate splits, each one ECDSA verification.
const maxOmittedZeroBytes = 3

// candidateSplits returns the (r, s) pairs worth trying for a signature of
// the given length, each already padded to size bytes.
//
// The straightforward case is the only correct one: RFC 7515 section 3.4
// fixes the signature at 2*size octets, r and s each left-padded to size,
// and that is what every conforming client sends. Some of Apple's ACME
// clients do not: they encode r and s as minimal big-endian integers and so
// drop leading zero bytes, producing a signature one or two octets short.
// step-ca ran into the same clients and patches the signature in
// retryVerificationWithPatchedSignatures (acme/api/middleware.go): it
// special-cases a deficit of exactly one, trying a zero prepended to r and
// then to s, and a deficit of exactly two, assuming one zero was dropped
// from each, mutating the parsed JWS in place and restoring it afterwards.
//
// Ours is the same idea expressed as a loop and without mutation. For a
// deficit d we try every way of splitting d between the front of r and the
// front of s, which is d+1 candidates and covers step-ca's two cases plus
// the ones it declines to guess at, and we compute the candidates instead
// of rewriting the signature we were handed. A signature longer than
// 2*size is never a truncation and is always rejected, and so is a deficit
// beyond maxOmittedZeroBytes.
func candidateSplits(sig []byte, size int) [][2][]byte {
	want := 2 * size
	switch {
	case len(sig) == want:
		return [][2][]byte{{sig[:size], sig[size:]}}
	case len(sig) > want, len(sig) < want-maxOmittedZeroBytes:
		return nil
	}
	deficit := want - len(sig)
	out := make([][2][]byte, 0, deficit+1)
	// fromR is how many leading zero bytes we assume were dropped from r;
	// the remainder were dropped from s.
	for fromR := range deficit + 1 {
		rLen := size - fromR
		r := make([]byte, size)
		s := make([]byte, size)
		copy(r[fromR:], sig[:rLen])
		copy(s[deficit-fromR:], sig[rLen:])
		out = append(out, [2][]byte{r, s})
	}
	return out
}

func (j *JWS) verifyRSA(pub crypto.PublicKey, hash crypto.Hash, digest []byte) error {
	key, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: alg %s needs an RSA key, got %T", ErrAlgorithm, j.Header.Algorithm, pub)
	}
	if err := checkRSASize(key); err != nil {
		return err
	}
	// RFC 7518 section 3.3 defines RS* as PKCS #1 v1.5; PSS is PS*, which we
	// do not accept, so there is nothing to negotiate here.
	if err := rsa.VerifyPKCS1v15(key, hash, digest, j.Signature); err != nil {
		return fmt.Errorf("%w: %w", ErrSignature, err)
	}
	return nil
}

// signHeader is the protected header Sign emits, in the member order RFC
// 8555 examples use. Nothing here is canonicalised: the bytes produced are
// the bytes signed, and Parse reads them back as they were written.
type signHeader struct {
	Alg   string `json:"alg"`
	Nonce string `json:"nonce,omitempty"`
	URL   string `json:"url,omitempty"`
	Kid   string `json:"kid,omitempty"`
	JWK   *JWK   `json:"jwk,omitempty"`
}

// Sign produces a flattened JWS over payload. It exists for the tests, for
// the simulator, and for any client code that has to talk to an ACME server
// of our own; a server never signs a JWS.
//
// If h.Algorithm is empty the algorithm is derived from the key: the curve
// for ECDSA, RS256 for RSA. If it is set it must be one we accept and it
// must match the key. The rest of h is written out as given, so a caller can
// produce a header Parse will reject on purpose.
func Sign(key crypto.Signer, h Header, payload []byte) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil signer", ErrKey)
	}
	alg, err := algorithmFor(key.Public(), h.Algorithm)
	if err != nil {
		return nil, err
	}
	hash, err := hashFor(alg)
	if err != nil {
		return nil, err
	}
	protectedJSON, err := json.Marshal(signHeader{
		Alg:   alg,
		Nonce: h.Nonce,
		URL:   h.URL,
		Kid:   h.KeyID,
		JWK:   h.JWK,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encoding the protected header: %w", ErrHeader, err)
	}
	protected := base64.RawURLEncoding.EncodeToString(protectedJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := protected + "." + encodedPayload

	digester := hash.New()
	_, _ = digester.Write([]byte(signingInput))
	raw, err := key.Sign(rand.Reader, digester.Sum(nil), hash)
	if err != nil {
		return nil, fmt.Errorf("%w: signing: %w", ErrKey, err)
	}
	if _, size, isEC := curveForAlg(alg); isEC {
		if raw, err = fixedWidthECDSA(size, raw); err != nil {
			return nil, err
		}
	}

	body, err := json.Marshal(flattenedOut{
		Protected: protected,
		Payload:   encodedPayload,
		Signature: base64.RawURLEncoding.EncodeToString(raw),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encoding the JWS: %w", ErrParse, err)
	}
	return body, nil
}

// flattenedOut is the serialisation Sign writes. It is separate from
// flattened because that one exists to reject members, not to emit them.
type flattenedOut struct {
	Protected string `json:"protected"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// algorithmFor picks or checks the algorithm for a key.
func algorithmFor(pub crypto.PublicKey, want string) (string, error) {
	var derived string
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		name, _, ok := curveName(k.Curve)
		if !ok {
			return "", fmt.Errorf("%w: unsupported elliptic curve", ErrKey)
		}
		switch name {
		case curveP256:
			derived = ES256
		case curveP384:
			derived = ES384
		default:
			derived = ES512
		}
	case *rsa.PublicKey:
		if err := checkRSASize(k); err != nil {
			return "", err
		}
		derived = RS256
	default:
		return "", fmt.Errorf("%w: cannot sign with %T", ErrKey, pub)
	}
	if want == "" {
		return derived, nil
	}
	if !slices.Contains(algorithms, want) {
		return "", fmt.Errorf("%w: alg %q is not one of %v", ErrAlgorithm, want, algorithms)
	}
	// For ECDSA the curve pins the algorithm exactly; for RSA any of the
	// three digests is legitimate, so only the family has to agree.
	_, _, wantEC := curveForAlg(want)
	derivedEC := derived != RS256
	if wantEC != derivedEC || (wantEC && want != derived) {
		return "", fmt.Errorf("%w: alg %q does not match a key that signs %s", ErrAlgorithm, want, derived)
	}
	return want, nil
}

// ecdsaASN1 is the SEQUENCE crypto.Signer implementations return for ECDSA.
type ecdsaASN1 struct {
	R, S *big.Int
}

// fixedWidthECDSA converts the ASN.1 signature a crypto.Signer produces
// into the fixed-width r||s concatenation RFC 7515 section 3.4 requires.
func fixedWidthECDSA(size int, der []byte) ([]byte, error) {
	var parsed ecdsaASN1
	rest, err := asn1.Unmarshal(der, &parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: signer did not return an ASN.1 ECDSA signature: %w", ErrSignature, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes after the ECDSA signature", ErrSignature, len(rest))
	}
	r, err := fixedWidth(parsed.R, size)
	if err != nil {
		return nil, fmt.Errorf("%w: r: %w", ErrSignature, err)
	}
	s, err := fixedWidth(parsed.S, size)
	if err != nil {
		return nil, fmt.Errorf("%w: s: %w", ErrSignature, err)
	}
	return append(r, s...), nil
}
