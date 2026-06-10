package admin

import (
	"net/url"
	"testing"
)

// TestForcedChangeSkipsCurrentPassword: the forced first-login change accepts a
// new password without re-entering the (just-used) current one.
func TestForcedChangeSkipsCurrentPassword(t *testing.T) {
	srv, _ := newServer(t)
	c := newClient(t)

	_, page := getBody(t, c, srv.URL+"/login")
	_, after := postForm(t, c, srv.URL+"/login", url.Values{
		"csrf": {csrfFrom(t, page)}, "username": {"admin"}, "password": {initialPW},
	})
	// 'after' is the forced-change page. Submit a new password WITHOUT 'current'.
	postForm(t, c, srv.URL+"/account/password", url.Values{
		"csrf": {csrfFrom(t, after)}, "new": {"brand-new-password-1"}, "confirm": {"brand-new-password-1"},
	})

	if !canLogin(t, srv.URL, "admin", "brand-new-password-1") {
		t.Fatal("forced change without the current password should succeed")
	}
}
