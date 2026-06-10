package admin

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/brandstore"
)

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
