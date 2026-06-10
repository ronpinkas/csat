package survey

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/db"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/token"
	"github.com/ronpinkas/csat/internal/web"
)

const secret = "integration-test-secret-32bytes-minimum!"

func newTestHandlers(t *testing.T) *Handlers {
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
	return New(tenant.WrapSingle(database), tmpl, cfg, surveydef.Default(), secret, false)
}

func TestSurveyFlow(t *testing.T) {
	h := newTestHandlers(t)
	tok, err := token.Encrypt(secret, "+15551234567", 1717286400, "en", "")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// GET the form
	req := httptest.NewRequest(http.MethodGet, "/s?t="+url.QueryEscape(tok), nil)
	rec := httptest.NewRecorder()
	h.Form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET form: status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Submit feedback") {
		t.Fatal("GET form: missing form markup")
	}

	// POST a valid submission (double-submit CSRF: cookie value == form value)
	rec = postSubmit(t, h, tok, "csrf-xyz", url.Values{
		"csrf":       {"csrf-xyz"},
		"csat":       {"5"},
		"resolution": {"yes"},
		"ces":        {"6"},
		"comment":    {"Great help!"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "recorded") {
		t.Fatalf("POST submit: status %d body=%q", rec.Code, rec.Body.String())
	}
	if got := countResponses(t, h); got != 1 {
		t.Fatalf("expected 1 response, got %d", got)
	}

	// POST again with the same token -> one-time blocks it
	rec = postSubmit(t, h, tok, "csrf-2", url.Values{
		"csrf": {"csrf-2"}, "csat": {"1"}, "resolution": {"no"}, "ces": {"1"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already") {
		t.Fatalf("second submit: expected already-submitted, got status %d body=%q", rec.Code, rec.Body.String())
	}
	if got := countResponses(t, h); got != 1 {
		t.Fatalf("after duplicate, expected still 1 response, got %d", got)
	}
}

func TestSpanishForm(t *testing.T) {
	h := newTestHandlers(t)
	tok, _ := token.Encrypt(secret, "+5999123456", 1717286400, "es", "")
	req := httptest.NewRequest(http.MethodGet, "/s?t="+url.QueryEscape(tok), nil)
	rec := httptest.NewRecorder()
	h.Form(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(body, `lang="es"`) {
		t.Error("expected <html lang=\"es\">")
	}
	if !strings.Contains(body, "Enviar comentarios") { // ES submit button
		t.Error("expected Spanish submit button")
	}
	if strings.Contains(body, "Submit feedback") {
		t.Error("English string leaked into Spanish form")
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	h := newTestHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551234567", 1717286400, "en", "")
	bad := tok[:len(tok)-2] + "AA"
	req := httptest.NewRequest(http.MethodGet, "/s?t="+url.QueryEscape(bad), nil)
	rec := httptest.NewRecorder()
	h.Form(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tampered token: expected 400, got %d", rec.Code)
	}
}

func TestMissingFieldRejected(t *testing.T) {
	h := newTestHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551234567", 1717286400, "en", "")
	rec := postSubmit(t, h, tok, "c", url.Values{
		"csrf": {"c"}, "csat": {"5"}, "resolution": {"yes"}, // missing ces
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing ces: expected 400, got %d", rec.Code)
	}
	if got := countResponses(t, h); got != 0 {
		t.Fatalf("expected 0 responses, got %d", got)
	}
}

func TestBadCSRFRejected(t *testing.T) {
	h := newTestHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551234567", 1717286400, "en", "")
	// cookie and form value differ
	body := url.Values{"csrf": {"form-val"}, "csat": {"5"}, "resolution": {"yes"}, "ces": {"6"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/s?t="+url.QueryEscape(tok), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "cookie-val"})
	rec := httptest.NewRecorder()
	h.Submit(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad csrf: expected 403, got %d", rec.Code)
	}
}

func postSubmit(t *testing.T, h *Handlers, tok, csrfCookie string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/s?t="+url.QueryEscape(tok), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfCookie})
	rec := httptest.NewRecorder()
	h.Submit(rec, req)
	return rec
}

// TestMultiTenantSurveyRouting: the tenant ref encoded in the token routes the
// response to that tenant's database only.
func TestMultiTenantSurveyRouting(t *testing.T) {
	prov, err := tenant.NewMulti(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prov.Close() })
	tmpl, err := web.LoadTemplates(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Site.Name = "Multi Co"
	h := New(prov, tmpl, cfg, surveydef.Default(), secret, false)

	tok, _ := token.Encrypt(secret, "+15551234567", 1717286400, "en", "acme")
	rec := postSubmit(t, h, tok, "c", url.Values{
		"csrf": {"c"}, "csat": {"5"}, "resolution": {"yes"}, "ces": {"6"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "recorded") {
		t.Fatalf("submit: %d %q", rec.Code, rec.Body.String())
	}

	acme, _ := prov.DB("acme")
	globex, _ := prov.DB("globex")
	if n := countIn(t, acme); n != 1 {
		t.Fatalf("acme should have the response, got %d", n)
	}
	if n := countIn(t, globex); n != 0 {
		t.Fatalf("globex must be empty, got %d", n)
	}
}

func countIn(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM responses`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countResponses(t *testing.T, h *Handlers) int {
	t.Helper()
	db, _ := h.provider.DB("")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM responses`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
