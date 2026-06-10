package admin

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/brandstore"
)

// TestSecretHiddenFromTenants: the deployment crypto secret (a master key that
// can forge links and provision tenants) is shown to a single-tenant operator
// but never to a tenant admin in multi-tenant mode.
func TestSecretHiddenFromTenants(t *testing.T) {
	// single-tenant: shown.
	srv, _ := newServer(t)
	admin := loginAdmin(t, srv)
	_, page := getBody(t, admin, srv.URL+"/settings")
	if !strings.Contains(page, "Survey-link secret") {
		t.Fatalf("single-tenant settings should show the survey-link secret: %s", first(page, 300))
	}

	// multi-tenant: hidden, and the value must not leak.
	msrv, _ := newMultiServer(t)
	madmin := loginAdminMulti(t, msrv, "acme.com")
	_, mpage := getBody(t, madmin, msrv.URL+"/settings")
	if strings.Contains(mpage, "Survey-link secret") {
		t.Fatalf("multi-tenant settings must not show the secret card: %s", first(mpage, 400))
	}
	if strings.Contains(mpage, multiSecret) {
		t.Fatal("the deployment secret leaked to a tenant admin")
	}
}

// TestBrandingSaved: the Settings tab saves per-tenant name + color, the admin
// pages reflect the new name, and an invalid color is rejected.
func TestBrandingSaved(t *testing.T) {
	srv, database := newServer(t)
	admin := loginAdmin(t, srv)

	_, page := getBody(t, admin, srv.URL+"/settings")
	postForm(t, admin, srv.URL+"/settings", url.Values{
		"csrf": {csrfFrom(t, page)}, "site_name": {"Acme Co"}, "theme_color": {"#ff0000"},
	})

	// The dashboard header now shows the branded name.
	_, dash := getBody(t, admin, srv.URL+"/dashboard")
	if !strings.Contains(dash, "Acme Co") {
		t.Fatalf("dashboard should show the branded name: %s", first(dash, 400))
	}

	// And it is persisted.
	b := brandstore.Resolve(database, "Default Co", "#000000")
	if b.SiteName != "Acme Co" || b.ThemeColor != "#ff0000" {
		t.Fatalf("branding not stored: %+v", b)
	}

	// An invalid color is rejected.
	_, page2 := getBody(t, admin, srv.URL+"/settings")
	_, body := postForm(t, admin, srv.URL+"/settings", url.Values{
		"csrf": {csrfFrom(t, page2)}, "site_name": {"Acme Co"}, "theme_color": {"not-a-color"},
	})
	if !strings.Contains(body, "hex value") {
		t.Fatalf("invalid color should be rejected: %s", first(body, 300))
	}
}
