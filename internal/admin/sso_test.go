package admin

import (
	"net/http"
	"testing"
	"time"

	"github.com/ronpinkas/csat/internal/token"
)

// TestSSOSignsInAndAutoProvisions: a platform-signed sso token signs a
// user straight into the dashboard (auto-creating them), and a non-sso token
// (e.g. a survey token) cannot.
func TestSSOSignsInAndAutoProvisions(t *testing.T) {
	srv, prov := newMultiServer(t)

	tok, err := token.Encrypt(multiSecret, SSOPrefix+"owner", time.Now().Add(time.Hour).Unix(), "admin", "acme.com")
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t) // cookie jar, follows redirects
	resp, err := c.Get(srv.URL + "/sso?t=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sso should land on the dashboard, got %d", resp.StatusCode)
	}

	db, _ := prov.DB("acme.com")
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE username = 'owner'`).Scan(&role); err != nil {
		t.Fatalf("owner not auto-provisioned: %v", err)
	}
	if role != RoleAdmin {
		t.Fatalf("owner role: want admin, got %q", role)
	}

	// A survey token (no sso prefix) must not sign anyone in.
	bad, _ := token.Encrypt(multiSecret, "+15551234567", 0, "en", "acme.com")
	nc := newClient(t)
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r2, err := nc.Get(srv.URL + "/sso?t=" + bad)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusForbidden {
		t.Fatalf("non-sso token: want 403, got %d", r2.StatusCode)
	}
}
