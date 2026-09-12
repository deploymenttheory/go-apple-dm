package dep

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 5849 HMAC-SHA1 is the only method Apple's /session accepts
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// OAuth1Realm is the realm Apple documents for /session.
const OAuth1Realm = "ADM"

// OAuth1 holds the inputs of one RFC 5849 HMAC-SHA1 signature.
type OAuth1 struct {
	ConsumerKey    string
	ConsumerSecret string
	// Token and TokenSecret are the access token pair; both may be empty
	// for the temporary-credential request of the RFC example.
	Token       string
	TokenSecret string
	Timestamp   int64
	Nonce       string
	// Realm is the Authorization realm; empty omits it.
	Realm string
	// Version sends oauth_version="1.0" when true (Apple's header has it;
	// the RFC examples do not).
	Version bool
	// Extra adds protocol parameters such as oauth_callback to the
	// signature and the header.
	Extra url.Values
}

// Sign computes the oauth_signature for method and u (query parameters
// included) as RFC 5849 section 3.4 describes.
func (o OAuth1) Sign(method string, u *url.URL) string {
	params := o.params()
	for k, vs := range u.Query() {
		for _, v := range vs {
			params = append(params, [2]string{k, v})
		}
	}
	base := strings.ToUpper(method) + "&" + percentEncode(baseURL(u)) + "&" + percentEncode(normalize(params))
	key := percentEncode(o.ConsumerSecret) + "&" + percentEncode(o.TokenSecret)
	mac := hmac.New(sha1.New, []byte(key)) // #nosec G401 -- RFC 5849 HMAC-SHA1, required by Apple's /session
	mac.Write([]byte(base))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Header renders the Authorization header value for method and u, in the
// parameter order Apple's documented request uses.
func (o OAuth1) Header(method string, u *url.URL) string {
	sig := o.Sign(method, u)
	var sb strings.Builder
	sb.WriteString("OAuth ")
	if o.Realm != "" {
		sb.WriteString(`realm="` + o.Realm + `", `)
	}
	pairs := [][2]string{{"oauth_consumer_key", o.ConsumerKey}}
	if o.Token != "" {
		pairs = append(pairs, [2]string{"oauth_token", o.Token})
	}
	pairs = append(pairs,
		[2]string{"oauth_signature_method", "HMAC-SHA1"},
		[2]string{"oauth_signature", sig},
		[2]string{"oauth_timestamp", strconv.FormatInt(o.Timestamp, 10)},
		[2]string{"oauth_nonce", o.Nonce},
	)
	if o.Version {
		pairs = append(pairs, [2]string{"oauth_version", "1.0"})
	}
	for _, k := range sortedKeys(o.Extra) {
		for _, v := range o.Extra[k] {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	for i, p := range pairs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(percentEncode(p[0]) + `="` + percentEncode(p[1]) + `"`)
	}
	return sb.String()
}

// params lists the protocol parameters that enter the signature.
func (o OAuth1) params() [][2]string {
	out := [][2]string{
		{"oauth_consumer_key", o.ConsumerKey},
		{"oauth_signature_method", "HMAC-SHA1"},
		{"oauth_timestamp", strconv.FormatInt(o.Timestamp, 10)},
		{"oauth_nonce", o.Nonce},
	}
	if o.Token != "" {
		out = append(out, [2]string{"oauth_token", o.Token})
	}
	if o.Version {
		out = append(out, [2]string{"oauth_version", "1.0"})
	}
	for k, vs := range o.Extra {
		for _, v := range vs {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// ParseOAuth1Header reads the parameters of an OAuth Authorization header
// value into a map (the realm included under "realm"). Values are
// percent-decoded. It is what a verifier such as deptest.Server uses.
func ParseOAuth1Header(h string) (map[string]string, error) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "OAuth") {
		return nil, fmt.Errorf("%w: not an OAuth header", ErrInvalid)
	}
	out := map[string]string{}
	for part := range strings.SplitSeq(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, fmt.Errorf("%w: malformed OAuth parameter %q", ErrInvalid, part)
		}
		v = strings.Trim(v, `"`)
		dv, err := url.PathUnescape(v)
		if err != nil {
			return nil, fmt.Errorf("%w: OAuth parameter %s: %w", ErrInvalid, k, err)
		}
		out[k] = dv
	}
	return out, nil
}

// baseURL renders scheme://host/path with the scheme and host lower-cased
// and default ports removed (RFC 5849 section 3.4.1.2).
func baseURL(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	switch {
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

// normalize encodes, sorts, and joins the parameters (section 3.4.1.3.2).
func normalize(params [][2]string) string {
	enc := make([][2]string, len(params))
	for i, p := range params {
		enc[i] = [2]string{percentEncode(p[0]), percentEncode(p[1])}
	}
	sort.Slice(enc, func(i, j int) bool {
		if enc[i][0] != enc[j][0] {
			return enc[i][0] < enc[j][0]
		}
		return enc[i][1] < enc[j][1]
	})
	parts := make([]string, len(enc))
	for i, p := range enc {
		parts[i] = p[0] + "=" + p[1]
	}
	return strings.Join(parts, "&")
}

// percentEncode applies section 3.6: unreserved characters pass, every
// other byte becomes an upper-case %XX.
func percentEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
			sb.WriteByte(c)
		default:
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[c>>4])
			sb.WriteByte(hexDigits[c&15])
		}
	}
	return sb.String()
}

func sortedKeys(v url.Values) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// randomNonce returns 16 random bytes as hex, the default nonce source.
func randomNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("dep: nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
