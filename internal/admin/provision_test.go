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

// TestProvisionCreatesTenantAndAdminInvite: a platform-signed provisioning token
// creates the tenant and returns an admin invite link that redeems into the
// tenant's first admin.
func TestProvisionCreatesTenantAndAdminInvite(t *testing.T) {
	srv, prov := newMultiServer(t)

	_, body := postForm(t, newClient(t), srv.URL+"/provision?t="+provisionToken(t, "acme.com", time.Hour), url.Values{})
	var resp struct {
		InviteURL string `json:"invite_url"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("provision response not JSON: %s", first(body, 200))
	}
	if resp.InviteURL == "" {
		t.Fatalf("no invite_url in response: %s", body)
	}

	// Redeem the admin invite — sets the first admin's username + password.
	u, err := url.Parse(resp.InviteURL)
	if err != nil {
		t.Fatal(err)
	}
	inviteTok := u.Query().Get("t")
	invitee := newClient(t)
	_, redeemPage := getBody(t, invitee, resp.InviteURL)
	postForm(t, invitee, resp.InviteURL, url.Values{
		"csrf": {csrfFrom(t, redeemPage)}, "t": {inviteTok}, "ref": {"acme.com"},
		"username": {"owner"}, "new": {"owner-password-123"}, "confirm": {"owner-password-123"},
	})

	db, _ := prov.DB("acme.com")
	if n := countByName(t, db, "owner"); n != 1 {
		t.Fatalf("provisioned tenant should have admin 'owner', got %d", n)
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE username = 'owner'`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != RoleAdmin {
		t.Fatalf("first user should be admin, got %q", role)
	}
}

func TestProvisionRejectsBadToken(t *testing.T) {
	srv, _ := newMultiServer(t)

	// Garbage token.
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t=not-a-token", url.Values{}); code != http.StatusForbidden {
		t.Fatalf("garbage token: want 403, got %d", code)
	}
	// A valid *survey* token (wrong subject) must not provision.
	surveyTok, _ := token.Encrypt(multiSecret, "+15551234567", 0, "en", "acme.com")
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t="+surveyTok, url.Values{}); code != http.StatusForbidden {
		t.Fatalf("survey token: want 403, got %d", code)
	}
	// Expired provisioning token.
	expired, _ := token.Encrypt(multiSecret, ProvisionSubject, time.Now().Add(-time.Hour).Unix(), "", "acme.com")
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t="+expired, url.Values{}); code != http.StatusForbidden {
		t.Fatalf("expired token: want 403, got %d", code)
	}
}

func TestProvisionRejectedInSingleTenant(t *testing.T) {
	srv, _ := newServer(t) // single-tenant
	if code, _ := postForm(t, newClient(t), srv.URL+"/provision?t="+provisionToken(t, "acme.com", time.Hour), url.Values{}); code != http.StatusBadRequest {
		t.Fatalf("single-tenant provision: want 400, got %d", code)
	}
}
