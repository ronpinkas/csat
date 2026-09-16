package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/db"
	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/web"
)

const singleSecret = "integration-secret-32bytes-minimum-aaa" // what newServer signs with

// newServerCORS is newServer with server.cors_origins set, for the read API.
func newServerCORS(t *testing.T, origins ...string) (*httptest.Server, *sql.DB) {
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
	cfg.Server.CorsOrigins = origins

	a, err := New(tenant.WrapSingle(database), tmpl, cfg, surveydef.Default(), singleSecret, false)
	if err != nil {
		t.Fatalf("admin.New: %v", err)
	}
	mux := http.NewServeMux()
	a.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, database
}

type exportJSONBody struct {
	Set       int64            `json:"set"`
	Questions []exportQuestion `json:"questions"`
	Responses []exportRow      `json:"responses"`
}

func exportToken(t *testing.T, ttl time.Duration) string {
	t.Helper()
	tok, err := MintApplianceToken(singleSecret, "acme.com", "", "", "export", ttl)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestExportJSON: a platform-signed token reads the range as JSON (answers
// folded in, questions described); anything else is refused.
func TestExportJSON(t *testing.T) {
	srv, database := newServer(t)
	insertResponse(t, database, "+15550000001", 5, "yes", 6, "Great help")
	insertResponse(t, database, "+15550000002", 1, "no", 2, "")

	today := time.Now().UTC().Format("2006-01-02")
	rangeQS := "&from=" + today + "&to=" + today + "&tz=UTC"

	code, body := getBody(t, newClient(t), srv.URL+"/api/export.json?t="+exportToken(t, time.Hour)+rangeQS)
	if code != http.StatusOK {
		t.Fatalf("export.json: want 200, got %d: %s", code, first(body, 200))
	}
	var out exportJSONBody
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, first(body, 300))
	}
	if len(out.Responses) != 2 {
		t.Fatalf("want 2 responses, got %d", len(out.Responses))
	}
	var first1 *exportRow
	for i := range out.Responses {
		if out.Responses[i].Subject == "+15550000001" {
			first1 = &out.Responses[i]
		}
	}
	if first1 == nil {
		t.Fatalf("subject +15550000001 missing: %s", first(body, 300))
	}
	if first1.Answers["csat"] != "5" || first1.Answers["comment"] != "Great help" || first1.Answers["ces"] != "6" {
		t.Fatalf("answers not folded in: %+v", first1.Answers)
	}
	if first1.SubmittedUTC == "" || !strings.HasSuffix(first1.SubmittedUTC, "Z") {
		t.Fatalf("submitted_at_utc should be RFC3339 UTC, got %q", first1.SubmittedUTC)
	}
	if first1.Incomplete {
		t.Fatalf("a submitted response must not be marked incomplete")
	}
	var stars *exportQuestion
	for i := range out.Questions {
		if out.Questions[i].Key == "csat" {
			stars = &out.Questions[i]
		}
	}
	if stars == nil || stars.Type != surveydef.TypeStars || stars.Max != 5 {
		t.Fatalf("questions must describe the rating: %+v", out.Questions)
	}

	// Bearer header works the same as ?t=.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/export.json?from="+today+"&to="+today+"&tz=UTC", nil)
	req.Header.Set("Authorization", "Bearer "+exportToken(t, time.Hour))
	resp, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bearer: want 200, got %d", resp.StatusCode)
	}

	// No token, a garbage token, an expired token, and a survey link token are all refused.
	for _, qs := range []string{"", "?t=garbage", "?t=" + exportToken(t, -time.Minute)} {
		u := srv.URL + "/api/export.json" + qs
		if qs == "" {
			u += "?from=" + today
		} else {
			u += rangeQS
		}
		code, _ := getBody(t, newClient(t), u)
		if code != http.StatusForbidden {
			t.Fatalf("%q: want 403, got %d", qs, code)
		}
	}
}

// TestExportJSONCORS: only an allow-listed origin gets the CORS header, and the
// preflight answers without a token.
func TestExportJSONCORS(t *testing.T) {
	srv, _ := newServerCORS(t, "https://platform.example.com")
	today := time.Now().UTC().Format("2006-01-02")
	target := srv.URL + "/api/export.json?t=" + exportToken(t, time.Hour) + "&from=" + today + "&to=" + today

	get := func(origin string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := newClient(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := get("https://platform.example.com"); r.StatusCode != 200 || r.Header.Get("Access-Control-Allow-Origin") != "https://platform.example.com" {
		t.Fatalf("allow-listed origin: status %d, ACAO %q", r.StatusCode, r.Header.Get("Access-Control-Allow-Origin"))
	}
	if r := get("https://evil.example.com"); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unknown origin must get no ACAO header, got %q", r.Header.Get("Access-Control-Allow-Origin"))
	}

	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/export.json", nil)
	req.Header.Set("Origin", "https://platform.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || !strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight: status %d, headers %v", resp.StatusCode, resp.Header)
	}
}

// TestExportCSVMatchesJSON: the CSV download and the JSON API are built from
// the same rows, so a response that appears in one appears in the other.
func TestExportCSVMatchesJSON(t *testing.T) {
	srv, database := newServer(t)
	insertResponse(t, database, "+15550000009", 4, "partial", 3, "ok")
	today := time.Now().UTC().Format("2006-01-02")

	admin := loginAdmin(t, srv)
	_, csvBody := getBody(t, admin, srv.URL+"/export.csv?from="+today+"&to="+today+"&tz=UTC")
	_, jsonBody := getBody(t, newClient(t), srv.URL+"/api/export.json?t="+exportToken(t, time.Hour)+"&from="+today+"&to="+today+"&tz=UTC")
	if !strings.Contains(csvBody, "+15550000009") || !strings.Contains(jsonBody, "+15550000009") {
		t.Fatalf("response missing from an export:\nCSV: %s\nJSON: %s", first(csvBody, 300), first(jsonBody, 300))
	}
}

// TestExportJSONAllSets: set=all returns responses of every question set and
// describes the questions of each, so a consumer can resolve the rating per
// response; without it only the resolved set is returned, as with export.csv.
func TestExportJSONAllSets(t *testing.T) {
	srv, database := newServer(t)
	insertResponse(t, database, "+15550000001", 5, "yes", 6, "current set")

	// A second set whose rating question is named differently, with one response.
	old := surveydef.Default()
	old.Questions = []surveydef.Question{{Key: "rating", Type: surveydef.TypeStars, Label: map[string]string{"en": "Rating"}, Min: 1, Max: 5}}
	oldID, err := defstore.Add(database, old, time.Now().Unix()-10)
	if err != nil {
		t.Fatalf("add old set: %v", err)
	}
	now := time.Now().Unix()
	res, err := database.Exec(`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id) VALUES(?, ?, 'en', ?, ?)`,
		"+15550000002", now, now, oldID)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := res.LastInsertId()
	if _, err := database.Exec(`INSERT INTO answers(response_id, question_key, num) VALUES(?, 'rating', 2)`, rid); err != nil {
		t.Fatal(err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	base := srv.URL + "/api/export.json?t=" + exportToken(t, time.Hour) + "&from=" + today + "&to=" + today + "&tz=UTC"

	_, body := getBody(t, newClient(t), base+"&set=1")
	var one exportJSONBody
	if err := json.Unmarshal([]byte(body), &one); err != nil || len(one.Responses) != 1 || one.Responses[0].Subject != "+15550000001" {
		t.Fatalf("set=1 should return only that set's response: %s", first(body, 300))
	}

	_, body = getBody(t, newClient(t), base+"&set=all")
	var all exportJSONBody
	if err := json.Unmarshal([]byte(body), &all); err != nil || len(all.Responses) != 2 {
		t.Fatalf("set=all should return both responses: %s", first(body, 300))
	}
	keys := map[string]string{}
	for _, q := range all.Questions {
		keys[q.Key] = q.Type
	}
	if keys["csat"] != surveydef.TypeStars || keys["rating"] != surveydef.TypeStars {
		t.Fatalf("set=all must describe the questions of every set, got %v", keys)
	}
	for _, r := range all.Responses {
		if r.Subject == "+15550000002" && (r.Answers["rating"] != "2" || r.SetID != oldID) {
			t.Fatalf("old-set response not carried with its set: %+v", r)
		}
	}
}
