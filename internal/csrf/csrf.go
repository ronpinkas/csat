// Package csrf provides a double-submit-cookie CSRF defense for pre-session
// forms (the public survey, login, invite redemption). Authenticated forms use
// the per-session synchronizer token instead (see internal/admin).
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const (
	cookieName = "csrf"
	// FieldName is the hidden form field carrying the token.
	FieldName = "csrf"
)

// Issue mints a token, sets it as a cookie, and returns the value to embed in a
// form's hidden field.
func Issue(w http.ResponseWriter, secure bool) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   3600,
	})
	return tok
}

// Check reports whether the submitted form token matches the cookie.
func Check(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	form := r.PostFormValue(FieldName)
	if form == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(form)) == 1
}
