package admin

import (
	"database/sql"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var resetLinkRE = regexp.MustCompile(`(/reset\?t=[A-Za-z0-9_\-]+)`)

// inviteViewer creates a viewer via the invite flow and returns the username.
func inviteViewer(t *testing.T, srv string, admin *http.Client, username string) {
	t.Helper()
	_, usersPage := getBody(t, admin, srv+"/users")
	_, body := postForm(t, admin, srv+"/users/invite", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "role": {"viewer"}, "username": {username},
	})
	link := extractInviteLink(t, body, srv)
	tok := link[strings.Index(link, "t=")+2:]
	invitee := newClient(t)
	_, redeemPage := getBody(t, invitee, link)
	postForm(t, invitee, srv+"/invite?t="+tok, url.Values{
		"csrf": {csrfFrom(t, redeemPage)}, "t": {tok},
		"new": {"viewer-password-123"}, "confirm": {"viewer-password-123"},
	})
}

func extractResetLink(t *testing.T, body, base string) string {
	t.Helper()
	m := resetLinkRE.FindStringSubmatch(body)
	if len(m) < 2 {
		t.Fatalf("no reset link in page: %s", first(body, 400))
	}
	return base + m[1]
}

func canLogin(t *testing.T, srv, username, password string) bool {
	t.Helper()
	c := newClient(t)
	// Don't follow redirects: an unauthenticated /dashboard 303s to /login
	// (which is itself a 200), so we must inspect the direct status instead.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	_, page := getBody(t, c, srv+"/login")
	postForm(t, c, srv+"/login", url.Values{
		"csrf": {csrfFrom(t, page)}, "username": {username}, "password": {password},
	})
	resp, err := c.Get(srv + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard: %v", err)
	}
	resp.Body.Close()
	// 200 = authenticated; 303 -> /login = not. (A forced password change would
	// 303 to /account/password, which also counts as not-a-clean-login here.)
	return resp.StatusCode == http.StatusOK
}

// TestForgotPasswordFlow: user files a request from /forgot, the admin sees it
// on /users, issues a reset link, the user redeems it and can log in with the
// new password.
func TestForgotPasswordFlow(t *testing.T) {
	srv, database := newServer(t)
	base := srv.URL
	admin := loginAdmin(t, srv)
	inviteViewer(t, base, admin, "viewer1")

	// 1. User submits the forgot-password form (no auth).
	pub := newClient(t)
	_, fp := getBody(t, pub, base+"/forgot")
	if !strings.Contains(fp, "Forgot your password?") {
		t.Fatalf("forgot page missing: %s", first(fp, 200))
	}
	_, sent := postForm(t, pub, base+"/forgot", url.Values{
		"csrf": {csrfFrom(t, fp)}, "username": {"viewer1"},
	})
	if !strings.Contains(sent, "administrator has been notified") {
		t.Fatalf("expected confirmation, got: %s", first(sent, 200))
	}

	// 2. Admin sees the pending request on the Users page.
	_, usersPage := getBody(t, admin, base+"/users")
	if !strings.Contains(usersPage, "reset requested") {
		t.Fatalf("admin users page missing reset-requested badge: %s", first(usersPage, 600))
	}

	// 3. Admin issues a reset link.
	_, body := postForm(t, admin, base+"/users/reset", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "user_id": {userIDByName(t, database, "viewer1")},
	})
	link := extractResetLink(t, body, base)

	// Issuing the link clears the pending request.
	_, usersPage2 := getBody(t, admin, base+"/users")
	if strings.Contains(usersPage2, "reset requested") {
		t.Fatalf("reset request should be cleared after issuing link")
	}

	// 4. User redeems the reset link with a new password.
	tok := link[strings.Index(link, "t=")+2:]
	redeemer := newClient(t)
	_, rp := getBody(t, redeemer, link)
	if !strings.Contains(rp, "Set a new password") {
		t.Fatalf("reset page missing: %s", first(rp, 200))
	}
	postForm(t, redeemer, base+"/reset?t="+tok, url.Values{
		"csrf": {csrfFrom(t, rp)}, "t": {tok},
		"new": {"viewer-new-password-456"}, "confirm": {"viewer-new-password-456"},
	})

	// 5. New password works; old one doesn't.
	if !canLogin(t, base, "viewer1", "viewer-new-password-456") {
		t.Fatalf("viewer should log in with the new password")
	}
	if canLogin(t, base, "viewer1", "viewer-password-123") {
		t.Fatalf("old password should no longer work after reset")
	}

	// 6. The reset link is single-use.
	used := newClient(t)
	_, again := getBody(t, used, link)
	if !strings.Contains(again, "not valid") {
		t.Fatalf("reset link should be single-use, got: %s", first(again, 200))
	}
}

// TestResetReactivatesInactiveUser: a deactivated user can be recovered via a
// reset link, which flips them back to active.
func TestResetReactivatesInactiveUser(t *testing.T) {
	srv, database := newServer(t)
	base := srv.URL
	admin := loginAdmin(t, srv)
	inviteViewer(t, base, admin, "viewer1")

	id := userIDByName(t, database, "viewer1")

	// Deactivate the viewer.
	_, usersPage := getBody(t, admin, base+"/users")
	postForm(t, admin, base+"/users/deactivate", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "user_id": {id},
	})
	if canLogin(t, base, "viewer1", "viewer-password-123") {
		t.Fatalf("deactivated user should not be able to log in")
	}

	// Admin issues a reset for the inactive user.
	_, usersPage = getBody(t, admin, base+"/users")
	_, body := postForm(t, admin, base+"/users/reset", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "user_id": {id},
	})
	link := extractResetLink(t, body, base)
	tok := link[strings.Index(link, "t=")+2:]

	// Redeem it.
	redeemer := newClient(t)
	_, rp := getBody(t, redeemer, link)
	postForm(t, redeemer, base+"/reset?t="+tok, url.Values{
		"csrf": {csrfFrom(t, rp)}, "t": {tok},
		"new": {"back-in-action-789"}, "confirm": {"back-in-action-789"},
	})

	// The account is active again and the new password works.
	if !canLogin(t, base, "viewer1", "back-in-action-789") {
		t.Fatalf("reset should reactivate the account and set the new password")
	}
	var active int
	if err := database.QueryRow(`SELECT active FROM users WHERE id = ?`, id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("user should be active after reset, active=%d", active)
	}
}

// TestDeleteUserFreesUsername: deleting a user lets the same username be
// invited again — the exact collision that deactivate+re-invite caused.
func TestDeleteUserFreesUsername(t *testing.T) {
	srv, database := newServer(t)
	base := srv.URL
	admin := loginAdmin(t, srv)
	inviteViewer(t, base, admin, "viewer1")

	id := userIDByName(t, database, "viewer1")

	// Delete the user.
	_, usersPage := getBody(t, admin, base+"/users")
	postForm(t, admin, base+"/users/delete", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "user_id": {id},
	})
	if n := countByName(t, database, "viewer1"); n != 0 {
		t.Fatalf("user should be gone after delete, found %d", n)
	}

	// Re-inviting the same username now succeeds (no "already taken").
	inviteViewer(t, base, admin, "viewer1")
	if n := countByName(t, database, "viewer1"); n != 1 {
		t.Fatalf("username should be reusable after delete, found %d rows", n)
	}
	if !canLogin(t, base, "viewer1", "viewer-password-123") {
		t.Fatalf("re-invited user should be able to log in")
	}
}

// TestDeleteLastAdminBlocked guards the sole-admin lockout.
func TestDeleteLastAdminBlocked(t *testing.T) {
	srv, database := newServer(t)
	base := srv.URL
	admin := loginAdmin(t, srv)

	id := userIDByName(t, database, "admin")
	_, usersPage := getBody(t, admin, base+"/users")
	_, body := postForm(t, admin, base+"/users/delete", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "user_id": {id},
	})
	if !strings.Contains(body, "last admin") && !strings.Contains(body, "your own account") {
		t.Fatalf("deleting the sole admin should be blocked, got: %s", first(body, 200))
	}
	if n := countByName(t, database, "admin"); n != 1 {
		t.Fatalf("admin should still exist, found %d", n)
	}
}

// ---- helpers ----

func userIDByName(t *testing.T, database *sql.DB, name string) string {
	t.Helper()
	var id int64
	if err := database.QueryRow(`SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
		t.Fatalf("lookup user %q: %v", name, err)
	}
	return strconv.FormatInt(id, 10)
}

func countByName(t *testing.T, database *sql.DB, name string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
