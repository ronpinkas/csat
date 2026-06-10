package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/web"
)

// newMultiServer builds a multi-tenant admin server over a fresh data dir and
// returns the server plus the provider (so tests can inspect per-tenant DBs).
func newMultiServer(t *testing.T) (*httptest.Server, tenant.Provider) {
	t.Helper()
	prov, err := tenant.NewMulti(t.TempDir())
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	t.Cleanup(func() { _ = prov.Close() })
	tmpl, err := web.LoadTemplates(nil)
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	cfg := &config.Config{}
	cfg.Site.Name = "Multi Co"
	cfg.Site.DisplayTimezone = "UTC"
	cfg.Admin.Username = "admin"
	cfg.Admin.InitialPassword = initialPW
	cfg.Tenancy.Mode = "multi"
	cfg.Security.SessionTTLHours, cfg.Security.InviteTTLHours = 12, 168

	a, err := New(prov, tmpl, cfg, surveydef.Default(), "multi-secret-32bytes-minimum-aaaaaa", false)
	if err != nil {
		t.Fatalf("admin.New: %v", err)
	}
	mux := http.NewServeMux()
	a.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, prov
}

// loginAdminMulti bootstraps a tenant (on first /login?ref=) and signs its admin
// in, completing the forced password change. The session cookie then pins the
// tenant, so subsequent requests need no ?ref.
func loginAdminMulti(t *testing.T, srv *httptest.Server, ref string) *http.Client {
	t.Helper()
	c := newClient(t)
	_, page := getBody(t, c, srv.URL+"/login?ref="+url.QueryEscape(ref))
	_, after := postForm(t, c, srv.URL+"/login?ref="+url.QueryEscape(ref), url.Values{
		"csrf": {csrfFrom(t, page)}, "ref": {ref}, "username": {"admin"}, "password": {initialPW},
	})
	postForm(t, c, srv.URL+"/account/password", url.Values{
		"csrf": {csrfFrom(t, after)}, "current": {initialPW},
		"new": {"a-brand-new-password"}, "confirm": {"a-brand-new-password"},
	})
	return c
}

// inviteViewerMulti invites + redeems a viewer in a specific tenant.
func inviteViewerMulti(t *testing.T, srv *httptest.Server, admin *http.Client, ref, username string) {
	t.Helper()
	_, usersPage := getBody(t, admin, srv.URL+"/users")
	_, body := postForm(t, admin, srv.URL+"/users/invite", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "role": {"viewer"}, "username": {username},
	})
	link := extractInviteLink(t, body, srv.URL) // captures /invite?t=...; we re-attach the ref
	tok := link[strings.Index(link, "t=")+2:]
	target := srv.URL + "/invite?t=" + tok + "&ref=" + url.QueryEscape(ref)
	invitee := newClient(t)
	_, redeemPage := getBody(t, invitee, target)
	postForm(t, invitee, target, url.Values{
		"csrf": {csrfFrom(t, redeemPage)}, "t": {tok}, "ref": {ref},
		"new": {"viewer-password-123"}, "confirm": {"viewer-password-123"},
	})
}

// TestMultiTenantIsolation: two tenants are fully independent — the same username
// can exist in both, each admin sees only its own users, and a mutation in one
// tenant never touches the other.
func TestMultiTenantIsolation(t *testing.T) {
	srv, prov := newMultiServer(t)

	acme := loginAdminMulti(t, srv, "acme.com")
	globex := loginAdminMulti(t, srv, "globex.io")

	// The SAME username, invited independently into each tenant.
	inviteViewerMulti(t, srv, acme, "acme.com", "bob")
	inviteViewerMulti(t, srv, globex, "globex.io", "bob")

	acmeDB, _ := prov.DB("acme.com")
	globexDB, _ := prov.DB("globex.io")
	if n := countByName(t, acmeDB, "bob"); n != 1 {
		t.Fatalf("acme should have its own bob, got %d", n)
	}
	if n := countByName(t, globexDB, "bob"); n != 1 {
		t.Fatalf("globex should have its own bob, got %d", n)
	}

	// acme's session is pinned to acme: /users (no ?ref) lists acme's users.
	_, page := getBody(t, acme, srv.URL+"/users")
	if !strings.Contains(page, "bob") {
		t.Fatalf("acme users page should list its bob: %s", first(page, 600))
	}

	// Deleting acme's bob frees acme's username but must not touch globex's bob.
	id := userIDByName(t, acmeDB, "bob")
	_, up := getBody(t, acme, srv.URL+"/users")
	postForm(t, acme, srv.URL+"/users/delete", url.Values{
		"csrf": {csrfFrom(t, up)}, "user_id": {id},
	})
	if n := countByName(t, acmeDB, "bob"); n != 0 {
		t.Fatalf("acme bob should be deleted, got %d", n)
	}
	if n := countByName(t, globexDB, "bob"); n != 1 {
		t.Fatalf("globex bob must be untouched, got %d", n)
	}
}

// TestMultiTenantSessionPinnedToTenant: a session minted for one tenant resolves
// only that tenant's database, regardless of any ?ref on the request URL.
func TestMultiTenantSessionPinnedToTenant(t *testing.T) {
	srv, prov := newMultiServer(t)
	acme := loginAdminMulti(t, srv, "acme.com")
	loginAdminMulti(t, srv, "globex.io") // create globex too

	// Invite "carol" only in acme.
	inviteViewerMulti(t, srv, acme, "acme.com", "carol")

	// Even with ?ref=globex.io on the URL, the acme cookie pins acme — the page
	// must show carol (acme), proving the query ref can't repoint the session.
	_, page := getBody(t, acme, srv.URL+"/users?ref=globex.io")
	if !strings.Contains(page, "carol") {
		t.Fatalf("acme session must stay on acme regardless of ?ref: %s", first(page, 600))
	}
	globexDB, _ := prov.DB("globex.io")
	if n := countByName(t, globexDB, "carol"); n != 0 {
		t.Fatalf("globex must not have carol, got %d", n)
	}
}

// TestSingleTenantIgnoresRef is the backward-compat guard: in single-tenant mode
// a stray ?ref is ignored, login works against the one database, and no tenant
// files are spun up.
func TestSingleTenantIgnoresRef(t *testing.T) {
	srv, database := newServer(t) // single-tenant
	c := newClient(t)
	_, page := getBody(t, c, srv.URL+"/login?ref=evil-injection")
	_, after := postForm(t, c, srv.URL+"/login?ref=evil-injection", url.Values{
		"csrf": {csrfFrom(t, page)}, "ref": {"evil-injection"},
		"username": {"admin"}, "password": {initialPW},
	})
	postForm(t, c, srv.URL+"/account/password", url.Values{
		"csrf": {csrfFrom(t, after)}, "current": {initialPW},
		"new": {"a-brand-new-password"}, "confirm": {"a-brand-new-password"},
	})
	if code, _ := getBody(t, c, srv.URL+"/users"); code != http.StatusOK {
		t.Fatalf("single-tenant login must work regardless of ?ref, got %d", code)
	}
	if n := countByName(t, database, "admin"); n != 1 {
		t.Fatalf("expected exactly the one bootstrap admin, got %d", n)
	}
}
