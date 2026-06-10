package admin

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/db"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/web"
)

const initialPW = "bootstrap-initial-pw"

var csrfRE = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func newServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tmpl, err := web.LoadTemplates(nil)
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	cfg := &config.Config{}
	cfg.Site.Name = "Test Co"
	cfg.Site.DisplayTimezone = "UTC"
	cfg.Admin.Username = "admin"
	cfg.Admin.InitialPassword = initialPW
	cfg.Security.SessionTTLHours, cfg.Security.InviteTTLHours = 12, 168

	a, err := New(tenant.WrapSingle(database), tmpl, cfg, surveydef.Default(), "integration-secret-32bytes-minimum-aaa", false)
	if err != nil {
		t.Fatalf("admin.New: %v", err)
	}
	mux := http.NewServeMux()
	a.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, database
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func getBody(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func csrfFrom(t *testing.T, body string) string {
	t.Helper()
	m := csrfRE.FindStringSubmatch(body)
	if len(m) < 2 {
		t.Fatal("no csrf token found in page")
	}
	return m[1]
}

func postForm(t *testing.T, c *http.Client, target string, form url.Values) (int, string) {
	t.Helper()
	resp, err := c.PostForm(target, form)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAdminFullFlow(t *testing.T) {
	srv, database := newServer(t)
	seedResponses(t, database, 6)

	admin := newClient(t)

	// 1. Login (double-submit CSRF on the pre-session form)
	_, loginPage := getBody(t, admin, srv.URL+"/login")
	_, body := postForm(t, admin, srv.URL+"/login", url.Values{
		"csrf": {csrfFrom(t, loginPage)}, "username": {"admin"}, "password": {initialPW},
	})
	// forced password change page
	if !strings.Contains(body, "Set a new password") {
		t.Fatalf("expected forced password change, got: %s", first(body, 200))
	}

	// 2. Change password (session CSRF synchronizer token)
	_, body = postForm(t, admin, srv.URL+"/account/password", url.Values{
		"csrf": {csrfFrom(t, body)}, "current": {initialPW},
		"new": {"a-brand-new-password"}, "confirm": {"a-brand-new-password"},
	})
	if !strings.Contains(body, "Recent comments") {
		t.Fatalf("expected dashboard after password change, got: %s", first(body, 200))
	}

	// 3. Analytics JSON reflects the seeded rows
	today := time.Now().UTC().Format("2006-01-02")
	code, aj := getBody(t, admin, srv.URL+"/api/analytics?from="+today+"&to="+today+"&tz=UTC")
	if code != 200 || !strings.Contains(aj, `"responses":6`) {
		t.Fatalf("analytics: code=%d body=%s", code, aj)
	}

	// 4. Create an invite for a viewer
	_, usersPage := getBody(t, admin, srv.URL+"/users")
	_, body = postForm(t, admin, srv.URL+"/users/invite", url.Values{
		"csrf": {csrfFrom(t, usersPage)}, "role": {"viewer"}, "username": {"viewer1"},
	})
	link := extractInviteLink(t, body, srv.URL)

	// 5. Redeem the invite as a new user (fresh client)
	invitee := newClient(t)
	_, redeemPage := getBody(t, invitee, link)
	if !strings.Contains(redeemPage, "Accept your invitation") {
		t.Fatalf("invite page missing: %s", first(redeemPage, 200))
	}
	tok := link[strings.Index(link, "t=")+2:]
	_, _ = postForm(t, invitee, srv.URL+"/invite?t="+tok, url.Values{
		"csrf": {csrfFrom(t, redeemPage)}, "t": {tok},
		"new": {"viewer-password-123"}, "confirm": {"viewer-password-123"},
	})

	// 6. Log in as the viewer and confirm role enforcement (no /users access)
	viewer := newClient(t)
	_, vlogin := getBody(t, viewer, srv.URL+"/login")
	postForm(t, viewer, srv.URL+"/login", url.Values{
		"csrf": {csrfFrom(t, vlogin)}, "username": {"viewer1"}, "password": {"viewer-password-123"},
	})
	code, _ = getBody(t, viewer, srv.URL+"/users")
	if code != http.StatusForbidden {
		t.Fatalf("viewer should be forbidden from /users, got %d", code)
	}
	// but the viewer can see the dashboard
	if code, _ := getBody(t, viewer, srv.URL+"/dashboard"); code != 200 {
		t.Fatalf("viewer should access dashboard, got %d", code)
	}
}

func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	srv, _ := newServer(t)
	c := newClient(t)
	// don't follow redirects
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("expected redirect to /login, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func seedResponses(t *testing.T, database *sql.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		insertResponse(t, database, "+1555000000"+string(rune('0'+i)),
			(i%5)+1, []string{"yes", "partial", "no"}[i%3], (i%7)+1,
			map[bool]string{true: "nice", false: ""}[i%2 == 0])
	}
}

// insertResponse writes a response + its answers using the default CSAT keys
// (csat=stars, resolution=choice, ces=scale, comment=text).
func insertResponse(t *testing.T, database *sql.DB, subject string, csat int, resolution string, ces int, comment string) {
	t.Helper()
	now := time.Now().Unix()
	// definition_id = 1: newServer seeds the first question set as id 1, and the
	// analytics/export now scope responses to a set.
	res, err := database.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id) VALUES(?, ?, 'en', ?, 1)`,
		subject, now, now)
	if err != nil {
		t.Fatalf("seed response: %v", err)
	}
	id, _ := res.LastInsertId()
	addNum := func(key string, v int) {
		if _, err := database.Exec(`INSERT INTO answers(response_id, question_key, num) VALUES(?, ?, ?)`, id, key, v); err != nil {
			t.Fatalf("seed answer %s: %v", key, err)
		}
	}
	addText := func(key, v string) {
		if _, err := database.Exec(`INSERT INTO answers(response_id, question_key, text) VALUES(?, ?, ?)`, id, key, v); err != nil {
			t.Fatalf("seed answer %s: %v", key, err)
		}
	}
	addNum("csat", csat)
	addText("resolution", resolution)
	addNum("ces", ces)
	if comment != "" {
		addText("comment", comment)
	}
}

func extractInviteLink(t *testing.T, body, base string) string {
	t.Helper()
	re := regexp.MustCompile(`(/invite\?t=[A-Za-z0-9_\-]+)`)
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		t.Fatalf("no invite link in page: %s", first(body, 300))
	}
	return base + m[1]
}

func first(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
