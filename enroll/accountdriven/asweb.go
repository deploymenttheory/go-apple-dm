package accountdriven

import (
	"context"
	"net/http"
)

// AppleAsWeb is the simple authentication flow: the device opens URL in
// an ASWebAuthenticationSession with user-identifier appended; whatever
// authenticates the person there (a form, or enroll/webauth against an
// identity provider) calls Finish, which mints the access token and sends
// the 308 Apple expects.
type AppleAsWeb struct {
	// URL is the web-auth URL (https).
	URL    string
	Tokens *Tokens
}

// Challenge implements Authenticator.
func (a *AppleAsWeb) Challenge(context.Context, *http.Request, *DeviceInfo) (Challenge, error) {
	return Challenge{Method: MethodAppleAsWeb, URL: a.URL}, nil
}

// Finish completes the flow for an authenticated identity: it issues the
// single-use access token and redirects (308) to
// apple-remotemanagement-user-login://authentication-results.
func (a *AppleAsWeb) Finish(w http.ResponseWriter, r *http.Request, id Identity) error {
	if id.ManagedAppleID == "" {
		return ErrManagedAppleID
	}
	tok, err := a.Tokens.Issue(r.Context(), KindAccess, id, nil)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, ResultURL(tok), http.StatusPermanentRedirect)
	return nil
}

// UserIdentifier reads the user-identifier the device appends to the
// web-auth URL.
func UserIdentifier(r *http.Request) string { return r.URL.Query().Get("user-identifier") }
