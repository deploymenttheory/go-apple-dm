package dep_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/dep"
)

func TestOAuth1(t *testing.T) {
	t.Parallel()

	t.Run("SignatureVector", func(t *testing.T) {
		t.Parallel()
		// RFC 5849 section 1.2, the third request: an access-token-protected
		// GET with query parameters, signed with both secrets.
		u, _ := url.Parse("http://photos.example.net/photos?file=vacation.jpg&size=original")
		o := dep.OAuth1{ConsumerKey: "dpf43f3p2l4k3l03", ConsumerSecret: "kd94hf93k423kf44", Token: "nnch734d00sl2jdk", TokenSecret: "pfkkdhi9sl3r4s00", Timestamp: 137131202, Nonce: "chapoH", Realm: "Photos"}
		if got := o.Sign("GET", u); got != "MdpQcU8iPSUjWoN/UDMsK2sui9I=" {
			t.Fatalf("RFC 5849 1.2 (photos): %s", got)
		}
		// The first request of the same section: no token, an
		// oauth_callback protocol parameter, POST.
		u, _ = url.Parse("https://photos.example.net/initiate")
		o = dep.OAuth1{ConsumerKey: "dpf43f3p2l4k3l03", ConsumerSecret: "kd94hf93k423kf44", Timestamp: 137131200, Nonce: "wIjqoS", Realm: "Photos", Extra: url.Values{"oauth_callback": {"http://printer.example.com/ready"}}}
		if got := o.Sign("POST", u); got != "74KNZJeDHnMBp0EMJ9ZHt/XKycU=" {
			t.Fatalf("RFC 5849 1.2 (initiate): %s", got)
		}
		h := o.Header("POST", u)
		if !strings.HasPrefix(h, `OAuth realm="Photos", oauth_consumer_key="dpf43f3p2l4k3l03", `) || !strings.Contains(h, `oauth_callback="http%3A%2F%2Fprinter.example.com%2Fready"`) || !strings.Contains(h, `oauth_signature="74KNZJeDHnMBp0EMJ9ZHt%2FXKycU%3D"`) || strings.Contains(h, "oauth_token") {
			t.Fatalf("header: %s", h)
		}
		// Apple's documented /session header: realm ADM, the parameter
		// order of the example, and oauth_version="1.0".
		u, _ = url.Parse("https://mdmenrollment.apple.com/session")
		o = dep.OAuth1{ConsumerKey: "CK_00fadb3d36c6094cf479838455321b7c", ConsumerSecret: "CS_secret", Token: "AT_O2109279022Oe03b641fd6f07d7face7894211d521fd8bef09c3O137392", TokenSecret: "AS_secret", Timestamp: 137131200, Nonce: "4572616e48616d6d65724c61686176", Realm: dep.OAuth1Realm, Version: true}
		h = o.Header("GET", u)
		// RFC 5849 §3.6 percent-encodes every reserved character of the
		// signature, as Apple's example (…%2FPY%3D) shows.
		sig := strings.NewReplacer("/", "%2F", "+", "%2B", "=", "%3D").Replace(o.Sign("GET", u))
		want := `OAuth realm="ADM", oauth_consumer_key="CK_00fadb3d36c6094cf479838455321b7c", oauth_token="AT_O2109279022Oe03b641fd6f07d7face7894211d521fd8bef09c3O137392", oauth_signature_method="HMAC-SHA1", oauth_signature="` + sig + `", oauth_timestamp="137131200", oauth_nonce="4572616e48616d6d65724c61686176", oauth_version="1.0"`
		if h != want {
			t.Fatalf("Apple header\n got %s\nwant %s", h, want)
		}
		// The header parses back to its parameters, and the signature
		// verifies from them.
		p, err := dep.ParseOAuth1Header(h)
		if err != nil {
			t.Fatal(err)
		}
		if p["realm"] != "ADM" || p["oauth_version"] != "1.0" || p["oauth_signature"] != o.Sign("GET", u) || p["oauth_timestamp"] != "137131200" {
			t.Fatalf("parsed: %v", p)
		}
		// Encoding: unreserved characters pass, everything else is %XX upper
		// case; default ports and case are normalised in the base URL.
		u1, _ := url.Parse("HTTPS://Example.COM:443/a b?q=1")
		u2, _ := url.Parse("https://example.com/a%20b?q=1")
		o = dep.OAuth1{ConsumerKey: "k", ConsumerSecret: "s", Timestamp: 1, Nonce: "n"}
		if o.Sign("get", u1) != o.Sign("GET", u2) {
			t.Fatal("base URL normalisation differs")
		}
		u3, _ := url.Parse("http://example.com:80")
		u4, _ := url.Parse("http://example.com/")
		if o.Sign("GET", u3) != o.Sign("GET", u4) {
			t.Fatal("default http port not normalised")
		}
		u5, _ := url.Parse("http://example.com:8080/")
		if o.Sign("GET", u5) == o.Sign("GET", u4) {
			t.Fatal("non-default port must change the signature")
		}
		// Duplicate parameter names sort by value.
		u6, _ := url.Parse("http://example.com/?a=2&a=1")
		u7, _ := url.Parse("http://example.com/?a=1&a=2")
		if o.Sign("GET", u6) != o.Sign("GET", u7) {
			t.Fatal("duplicate keys not sorted by value")
		}
	})

	t.Run("ParseFailures", func(t *testing.T) {
		t.Parallel()
		for _, h := range []string{"", "Basic abc", "OAuth", "OAuth realm", `OAuth oauth_nonce="%zz"`} {
			if _, err := dep.ParseOAuth1Header(h); !errors.Is(err, dep.ErrInvalid) {
				t.Errorf("%q: %v", h, err)
			}
		}
		if p, err := dep.ParseOAuth1Header(`oauth oauth_nonce="a%20b", x=y`); err != nil || p["oauth_nonce"] != "a b" || p["x"] != "y" {
			t.Fatalf("lenient parse: %v %v", p, err)
		}
	})
}
