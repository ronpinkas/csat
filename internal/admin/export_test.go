package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestExportCSV guards against the scan-arity bug (8 columns -> all fields must
// be scanned) and verifies formula-injection escaping.
func TestExportCSV(t *testing.T) {
	srv, database := newServer(t)

	insertResponse(t, database, "+15550000001", 5, "yes", 6, "Great help")
	insertResponse(t, database, "+15550000002", 3, "partial", 4, "=DANGER()+1") // formula injection attempt

	admin := loginAdmin(t, srv)
	today := time.Now().UTC().Format("2006-01-02")
	code, body := getBody(t, admin, srv.URL+"/export.csv?from="+today+"&to="+today+"&tz=UTC")
	if code != 200 {
		t.Fatalf("export status %d", code)
	}

	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("expected header + 2 data rows, got %d lines:\n%s", len(lines), body)
	}
	if !strings.Contains(lines[0], "ces") {
		t.Fatalf("header missing ces column: %s", lines[0])
	}
	// the ces value 6 must appear in the first data row (proves ces is scanned)
	if !strings.Contains(lines[1], ",6,") {
		t.Fatalf("ces value not exported in row 1: %s", lines[1])
	}
	// formula-injection escaping: the dangerous comment is prefixed with a quote
	if !strings.Contains(body, "'=DANGER") {
		t.Fatalf("formula injection not neutralized:\n%s", body)
	}
}

// loginAdmin performs the bootstrap login + forced password change and returns
// an authenticated client.
func loginAdmin(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := newClient(t)
	_, page := getBody(t, c, srv.URL+"/login")
	_, after := postForm(t, c, srv.URL+"/login", url.Values{
		"csrf": {csrfFrom(t, page)}, "username": {"admin"}, "password": {initialPW},
	})
	postForm(t, c, srv.URL+"/account/password", url.Values{
		"csrf": {csrfFrom(t, after)}, "current": {initialPW},
		"new": {"a-brand-new-password"}, "confirm": {"a-brand-new-password"},
	})
	return c
}
