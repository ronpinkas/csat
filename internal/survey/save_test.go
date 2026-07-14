package survey

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/token"
)

// saveDef: allow_save, with one required question so a partial save is possible.
const saveDefJSON = `{
	"allow_save": true,
	"questions":[
		{"key":"csat","type":"stars","max":5,"required":true,"label":{"en":"Rate us"}},
		{"key":"comment","type":"text","label":{"en":"Comments"}}
	]}`

func newSaveHandlers(t *testing.T) (*Handlers, *sql.DB) {
	t.Helper()
	h := newTestHandlers(t)
	def, err := surveydef.Parse([]byte(saveDefJSON))
	if err != nil {
		t.Fatalf("parse def: %v", err)
	}
	h.def = def // the seeded set is this allow_save survey
	db, _ := h.provider.DB("")
	return h, db
}

func postSave(t *testing.T, h *Handlers, tok string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/s/save?t="+url.QueryEscape(tok), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "c"})
	rec := httptest.NewRecorder()
	h.Save(rec, req)
	return rec
}

func countRows(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A save stores a PARTIAL response as a draft and must NOT consume the link.
func TestSaveStoresDraftWithoutConsumingToken(t *testing.T) {
	h, db := newSaveHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551230000", 1717286400, "en", "")

	// "comment" only — the required "csat" is deliberately missing.
	rec := postSave(t, h, tok, url.Values{"csrf": {"c"}, "comment": {"halfway done"}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("save: expected 204, got %d", rec.Code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM responses WHERE incomplete = 1`); n != 1 {
		t.Fatalf("expected 1 draft, got %d", n)
	}
	// The link must stay usable — used_tokens is only written on submit.
	if n := countRows(t, db, `SELECT COUNT(*) FROM used_tokens`); n != 0 {
		t.Fatalf("save must not consume the token, got %d used_tokens", n)
	}

	// The form still renders (not "already submitted") and carries the draft back.
	req := httptest.NewRequest(http.MethodGet, "/s?t="+url.QueryEscape(tok), nil)
	frec := httptest.NewRecorder()
	h.Form(frec, req)
	if frec.Code != http.StatusOK {
		t.Fatalf("resume: expected 200, got %d", frec.Code)
	}
	if !strings.Contains(frec.Body.String(), "halfway done") {
		t.Fatal("resume: saved answer was not rendered back into the form")
	}
}

// Saving repeatedly updates the same draft rather than piling up rows.
func TestSaveIsIdempotent(t *testing.T) {
	h, db := newSaveHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551230001", 1717286400, "en", "")

	postSave(t, h, tok, url.Values{"csrf": {"c"}, "comment": {"first"}})
	postSave(t, h, tok, url.Values{"csrf": {"c"}, "comment": {"second"}})

	if n := countRows(t, db, `SELECT COUNT(*) FROM responses`); n != 1 {
		t.Fatalf("expected 1 response row after two saves, got %d", n)
	}
	var text string
	if err := db.QueryRow(`SELECT text FROM answers WHERE question_key = 'comment'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "second" {
		t.Fatalf("draft should hold the latest answers, got %q", text)
	}
}

// Save then submit must leave exactly ONE response, flipped to complete, with the
// token consumed. This is the invariant that stops drafts double-counting.
func TestSaveThenSubmitPromotesSameRow(t *testing.T) {
	h, db := newSaveHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551230002", 1717286400, "en", "")

	postSave(t, h, tok, url.Values{"csrf": {"c"}, "comment": {"draft text"}})
	if n := countRows(t, db, `SELECT COUNT(*) FROM responses WHERE incomplete = 1`); n != 1 {
		t.Fatal("expected a draft before submitting")
	}

	rec := postSubmit(t, h, tok, "c", url.Values{
		"csrf": {"c"}, "csat": {"5"}, "comment": {"final text"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "recorded") {
		t.Fatalf("submit: %d %q", rec.Code, rec.Body.String())
	}

	if n := countRows(t, db, `SELECT COUNT(*) FROM responses`); n != 1 {
		t.Fatalf("save+submit must leave ONE row, got %d", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM responses WHERE incomplete = 0`); n != 1 {
		t.Fatal("the row should be complete after submitting")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM used_tokens`); n != 1 {
		t.Fatal("submit must consume the token")
	}
	// Answers were replaced, not appended.
	var text string
	if err := db.QueryRow(`SELECT text FROM answers WHERE question_key = 'comment'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "final text" {
		t.Fatalf("submit should replace draft answers, got %q", text)
	}
}

// Once submitted, the link is finished — further saves are refused.
func TestSaveRejectedAfterSubmit(t *testing.T) {
	h, _ := newSaveHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551230003", 1717286400, "en", "")

	postSubmit(t, h, tok, "c", url.Values{"csrf": {"c"}, "csat": {"4"}})
	rec := postSave(t, h, tok, url.Values{"csrf": {"c"}, "comment": {"too late"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("save after submit: expected 409, got %d", rec.Code)
	}
}

// Saving is refused on surveys that don't opt in.
func TestSaveRejectedWhenNotAllowed(t *testing.T) {
	h := newTestHandlers(t) // default CSAT survey: allow_save is off
	tok, _ := token.Encrypt(secret, "+15551230004", 1717286400, "en", "")
	rec := postSave(t, h, tok, url.Values{"csrf": {"c"}, "comment": {"nope"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when allow_save is off, got %d", rec.Code)
	}
}

// A draft still can't carry junk: present values are fully validated.
func TestSaveRejectsInvalidValue(t *testing.T) {
	h, _ := newSaveHandlers(t)
	tok, _ := token.Encrypt(secret, "+15551230005", 1717286400, "en", "")
	rec := postSave(t, h, tok, url.Values{"csrf": {"c"}, "csat": {"99"}}) // out of 1..5
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an out-of-range value, got %d", rec.Code)
	}
}
