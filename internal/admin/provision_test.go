package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/ronpinkas/csat/internal/token"
)

const multiSecret = "multi-secret-32bytes-minimum-aaaaaa"

func provisionToken(t *testing.T, ref string, ttl time.Duration) string {
	t.Helper()
	tok, err := token.Encrypt(multiSecret, ProvisionSubject, time.Now().Add(ttl).Unix(), "", ref)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// provisionInvite POSTs a provisioning token and returns the admin invite URL.
func provisionInvite(t *testing.T, base, ref string) string {
	t.Helper()
	_, body := postForm(t, newClient(t), base+"/provision?t="+provisionToken(t, ref, time.Hour), url.Values{})
	var r struct {
		InviteURL string `json:"invite_url"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil || r.InviteURL == "" {
		t.Fatalf("bad provision response: %s", first(body, 200))
	}
	return r.InviteURL
}

// redeemAdminInvite redeems an admin invite, choosing a username + password.
func redeemAdminInvite(t *testing.T, inviteURL, ref, username, pass string) {
	t.Helper()
	u, err := url.Parse(inviteURL)
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t)
	_, rp := getBody(t, c, inviteURL)
	postForm(t, c, inviteURL, url.Values{
		"csrf": {csrfFrom(t, rp)}, "t": {u.Query().Get("t")}, "ref": {ref},
		"username": {username}, "new": {pass}, "confirm": {pass},
	})
}

// canLoginMulti reports whether (user, pass) can sign into tenant ref.
func canLoginMulti(t *testing.T, base, ref, user, pass string) bool {
	t.Helper()
	c := newClient(t)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	_, page := getBody(t, c, base+"/login?ref="+url.QueryEscape(ref))
	postForm(t, c, base+"/login?ref="+url.QueryEscape(ref), url.Values{
		"csrf": {csrfFrom(t, page)}, "ref": {ref}, "username": {user}, "password": {pass},
	})
	resp, err := c.Get(base + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// TestProvisionRepeatableRecovery: provisioning always returns a fresh admin
// invite — repeatable even after a tenant already has an admin — so a tenant
// whose admin lost their password can always be given a way back in.
func TestProvisionRepeatableRecovery(t *testing.T) {
	srv, prov := newMultiServer(t)

	// 1) Onboard the first admin.
	redeemAdminInvite(t, provisionInvite(t, srv.URL, "acme.com"), "acme.com", "owner", "owner-password-123")
	db, _ := prov.DB("acme.com")
	if n := countByName(t, db, "owner"); n != 1 {
		t.Fatalf("owner admin not created, got %d", n)
	}
	if !canLoginMulti(t, srv.URL, "acme.com", "owner", "owner-password-123") {
		t.Fatal("owner should log in after onboarding")
	}

	// 2) Owner lost the password and there is no other admin. Re-provision and
	// redeem with the SAME email: the invite reclaims the existing account (acts
	// as a password reset) — no duplicate, new password works, old does not.
	redeemAdminInvite(t, provisionInvite(t, srv.URL, "acme.com"), "acme.com", "owner", "recovered-pw-456")
	if n := countByName(t, db, "owner"); n != 1 {
		t.Fatalf("reclaim must not duplicate the account, got %d", n)
	}
	var admins int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("expected exactly 1 admin after reclaim, got %d", admins)
	}
	if !canLoginMulti(t, srv.URL, "acme.com", "owner", "recovered-pw-456") {
		t.Fatal("owner should log in with the recovered password")
	}
	if canLoginMulti(t, srv.URL, "acme.com", "owner", "owner-password-123") {
		t.Fatal("old password should no longer work after reclaim")
	}
}

// TestPlatformInviteOverridesNormalDoesNot locks the gate: a platform invite
// (created_by NULL) reclaims an existing username, a normal admin-issued invite
// does not.
func TestPlatformInviteOverridesNormalDoesNot(t *testing.T) {
	_, db := newServer(t)
	oldHash, _ := hashPassword("old-password-123")
	if _, err := db.Exec(
		`INSERT INTO users(username, password_hash, role, must_change_pw, active, created_at) VALUES('owner', ?, 'admin', 0, 1, 0)`,
		oldHash); err != nil {
		t.Fatal(err)
	}
	var adminID int64
	db.QueryRow(`SELECT id FROM users WHERE username = 'admin'`).Scan(&adminID)
	newHash, _ := hashPassword("new-password-456")

	// Normal invite (created_by = a real admin) must NOT override.
	raw, _ := createInviteRow(db, RoleAdmin, "owner", adminID, time.Hour)
	inv, _ := inviteByToken(db, raw)
	if err := redeemInvite(db, inv, "owner", newHash); err != errUsernameTaken {
		t.Fatalf("normal invite should not override an existing user, got %v", err)
	}

	// Platform invite (created_by NULL) reclaims it.
	raw2, _ := createInviteRow(db, RoleAdmin, "owner", 0, time.Hour)
	inv2, _ := inviteByToken(db, raw2)
	if !inv2.Platform {
		t.Fatal("invite with NULL created_by should be Platform")
	}
	if err := redeemInvite(db, inv2, "owner", newHash); err != nil {
		t.Fatalf("platform invite should reclaim the account, got %v", err)
	}
	var got string
	db.QueryRow(`SELECT password_hash FROM users WHERE username = 'owner'`).Scan(&got)
	if got != newHash {
		t.Fatal("reclaim did not reset the password")
	}
}

func TestProvisionRejectsBadToken(t *testing.T) {
	srv, _ := newMultiServer(t)
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t=not-a-token", url.Values{}); code != http.StatusForbidden {
		t.Fatalf("garbage token: want 403, got %d", code)
	}
	surveyTok, _ := token.Encrypt(multiSecret, "+15551234567", 0, "en", "acme.com")
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t="+surveyTok, url.Values{}); code != http.StatusForbidden {
		t.Fatalf("survey token: want 403, got %d", code)
	}
	expired, _ := token.Encrypt(multiSecret, ProvisionSubject, time.Now().Add(-time.Hour).Unix(), "", "acme.com")
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t="+expired, url.Values{}); code != http.StatusForbidden {
		t.Fatalf("expired token: want 403, got %d", code)
	}
}

func TestProvisionRejectedInSingleTenant(t *testing.T) {
	srv, _ := newServer(t)
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t="+provisionToken(t, "acme.com", time.Hour), url.Values{}); code != http.StatusBadRequest {
		t.Fatalf("single-tenant provision: want 400, got %d", code)
	}
}
