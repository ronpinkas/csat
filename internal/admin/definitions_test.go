package admin

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/db"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/web"
)

// TestUpgradeSeedsAndBackfills is the backward-compat guard: a pre-existing
// single-tenant database (responses, no survey_definitions) must, on New(),
// auto-create set #1 from the default and backfill existing responses to it so
// historical analytics keep working.
func TestUpgradeSeedsAndBackfills(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Simulate a legacy DB: migrate, then insert a response with NO definition_id
	// (as the pre-feature code did).
	legacy, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at) VALUES('+1555', 100, 'en', 100)`,
	); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	// Now boot the admin app over that DB (the "upgrade").
	prov, err := tenant.NewSingle(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prov.Close() })
	tmpl, _ := web.LoadTemplates(nil)
	cfg := &config.Config{}
	cfg.Site.Name = "Legacy Co"
	cfg.Admin.Username = "admin"
	cfg.Admin.InitialPassword = initialPW
	cfg.Security.SessionTTLHours, cfg.Security.InviteTTLHours = 12, 168
	if _, err := New(prov, tmpl, cfg, surveydef.Default(), "secret-32bytes-minimum-aaaaaaaaaaaa", false); err != nil {
		t.Fatalf("New (upgrade): %v", err)
	}

	database := prov.Handles()[0]
	// A set was auto-created.
	var sets int
	if err := database.QueryRow(`SELECT COUNT(*) FROM survey_definitions`).Scan(&sets); err != nil {
		t.Fatal(err)
	}
	if sets != 1 {
		t.Fatalf("expected 1 auto-seeded set, got %d", sets)
	}
	// The pre-existing response was backfilled to set #1.
	var defID int64
	if err := database.QueryRow(`SELECT definition_id FROM responses WHERE subject = '+1555'`).Scan(&defID); err != nil {
		t.Fatal(err)
	}
	if defID != 1 {
		t.Fatalf("legacy response should be backfilled to set 1, got %d", defID)
	}
}

// TestSurveyEditorPublishesNewSet: the admin Survey tab publishes a new set
// (never mutating the old one), and it shows up as latest.
func TestSurveyEditorPublishesNewSet(t *testing.T) {
	srv, database := newServer(t)
	admin := loginAdmin(t, srv)

	// The editor renders the current set as JSON.
	_, page := getBody(t, admin, srv.URL+"/survey")
	if !strings.Contains(page, "Definition (JSON)") {
		t.Fatalf("survey editor missing: %s", first(page, 300))
	}

	// Publish an edited (but valid) definition.
	edited := surveydef.Default()
	edited.Intro = map[string]string{"en": "Brand new intro"}
	js, _ := edited.JSON()
	postForm(t, admin, srv.URL+"/survey", url.Values{
		"csrf": {csrfFrom(t, page)}, "definition": {string(js)},
	})

	// Two sets now exist; set #2 is latest and carries the edit.
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM survey_definitions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 sets after publish, got %d", n)
	}
	var latestJSON string
	if err := database.QueryRow(
		`SELECT json FROM survey_definitions ORDER BY id DESC LIMIT 1`).Scan(&latestJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(latestJSON, "Brand new intro") {
		t.Fatal("latest set should carry the edited intro")
	}
}

// TestInvalidDefinitionRejected: bad JSON re-renders with an error and creates
// no new set.
func TestInvalidDefinitionRejected(t *testing.T) {
	srv, database := newServer(t)
	admin := loginAdmin(t, srv)
	_, page := getBody(t, admin, srv.URL+"/survey")

	_, body := postForm(t, admin, srv.URL+"/survey", url.Values{
		"csrf": {csrfFrom(t, page)}, "definition": {"{ not valid json"},
	})
	if !strings.Contains(body, "Invalid survey definition") {
		t.Fatalf("expected validation error, got: %s", first(body, 300))
	}
	var n int
	database.QueryRow(`SELECT COUNT(*) FROM survey_definitions`).Scan(&n)
	if n != 1 {
		t.Fatalf("invalid publish must not create a set, have %d", n)
	}
}

// TestAnalyticsScopedToSet: analytics filter responses to the selected set, and
// default to the latest.
func TestAnalyticsScopedToSet(t *testing.T) {
	srv, database := newServer(t)
	admin := loginAdmin(t, srv)

	// Publish a second set so we have sets 1 and 2.
	_, page := getBody(t, admin, srv.URL+"/survey")
	js, _ := surveydef.Default().JSON()
	postForm(t, admin, srv.URL+"/survey", url.Values{
		"csrf": {csrfFrom(t, page)}, "definition": {string(js)},
	})

	// 3 responses under set 1, 2 under set 2 (today, so they fall in range).
	insertN(t, database, 3, 1)
	insertN(t, database, 2, 2)

	if got := analyticsResponses(t, srv, admin, "1"); got != 3 {
		t.Fatalf("set 1 should have 3 responses, got %d", got)
	}
	if got := analyticsResponses(t, srv, admin, "2"); got != 2 {
		t.Fatalf("set 2 should have 2 responses, got %d", got)
	}
	// No ?set -> default to latest (set 2).
	if got := analyticsResponses(t, srv, admin, ""); got != 2 {
		t.Fatalf("default analytics should use latest set (2 responses), got %d", got)
	}
}

func insertN(t *testing.T, database *sql.DB, n int, defID int64) {
	t.Helper()
	now := time.Now().Unix()
	for i := 0; i < n; i++ {
		if _, err := database.Exec(
			`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id) VALUES(?, ?, 'en', ?, ?)`,
			"+1555", now, now, defID); err != nil {
			t.Fatal(err)
		}
	}
}

func analyticsResponses(t *testing.T, srv *httptest.Server, c *http.Client, set string) int {
	t.Helper()
	today := time.Now().UTC().Format("2006-01-02")
	u := srv.URL + "/api/analytics?from=" + today + "&to=" + today + "&tz=UTC"
	if set != "" {
		u += "&set=" + set
	}
	_, body := getBody(t, c, u)
	// crude extract of "responses":N
	i := strings.Index(body, `"responses":`)
	if i < 0 {
		t.Fatalf("no responses field: %s", first(body, 200))
	}
	rest := body[i+len(`"responses":`):]
	j := strings.IndexAny(rest, ",}")
	n, _ := strconv.Atoi(strings.TrimSpace(rest[:j]))
	return n
}
