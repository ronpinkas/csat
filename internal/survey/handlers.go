// Package survey serves the public, tokenized CSAT form and records responses.
package survey

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/csrf"
	"github.com/ronpinkas/csat/internal/token"
	"github.com/ronpinkas/csat/internal/web"
)

// Handlers serves the public survey + branding routes.
type Handlers struct {
	db     *sql.DB
	tmpl   *web.Templates
	cfg    *config.Config
	secret string
	secure bool
}

// New builds the survey handlers. secure marks cookies Secure (set when TLS is
// terminated by/for the app).
func New(db *sql.DB, tmpl *web.Templates, cfg *config.Config, secret string, secure bool) *Handlers {
	return &Handlers{db: db, tmpl: tmpl, cfg: cfg, secret: secret, secure: secure}
}

type pageBase struct {
	SiteName string
	Wide     bool
	Lang     string
	LogoURL  string
}

type surveyData struct {
	pageBase
	T             messages
	Token         string
	CSRF          string
	CSATStars     []int // descending (high->low) for the reverse-DOM star widget
	CESScale      []int // ascending 1..max
	CommentMaxLen int
}

type doneData struct {
	pageBase
	Title   string
	Message string
}

type errData struct {
	pageBase
	Heading string
	Message string
}

// Form handles GET /s — validate the token and render the survey.
func (h *Handlers) Form(w http.ResponseWriter, r *http.Request) {
	callerID, callTime, lang, ok := h.decode(r)
	if !ok {
		h.renderInvalid(w, "en")
		return
	}
	t := stringsFor(lang)
	if h.alreadyUsed(callerID, callTime) {
		h.renderDone(w, lang, t.DoneTitle, t.AlreadyMsg)
		return
	}
	h.render(w, "survey.tmpl", surveyData{
		pageBase:      h.base(lang),
		T:             t,
		Token:         r.URL.Query().Get("t"),
		CSRF:          csrf.Issue(w, h.secure),
		CSATStars:     scaleDesc(h.cfg.Survey.CSATMax),
		CESScale:      scale(1, h.cfg.Survey.CESMax),
		CommentMaxLen: h.cfg.Survey.CommentMaxLen,
	})
}

// Submit handles POST /s — re-validate the token, validate input, store once.
func (h *Handlers) Submit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderInvalid(w, "en")
		return
	}
	callerID, callTime, lang, ok := h.decode(r)
	if !ok {
		h.renderInvalid(w, "en")
		return
	}
	t := stringsFor(lang)
	if !csrf.Check(r) {
		h.renderError(w, http.StatusForbidden, lang, t.ErrGenericHeading, t.ErrSessionMsg)
		return
	}
	sub, err := parseSubmission(
		r.PostFormValue("csat"), r.PostFormValue("resolution"), r.PostFormValue("ces"), r.PostFormValue("comment"),
		h.cfg.Survey.CSATMax, h.cfg.Survey.CESMax, h.cfg.Survey.CommentMaxLen,
	)
	if err != nil {
		h.renderError(w, http.StatusBadRequest, lang, t.ErrFormHeading, t.ErrFormMsg)
		return
	}

	switch h.store(callerID, callTime, sub) {
	case storeOK:
		h.renderDone(w, lang, t.DoneTitle, t.DoneMsg)
	case storeAlreadyUsed:
		h.renderDone(w, lang, t.DoneTitle, t.AlreadyMsg)
	default:
		h.renderError(w, http.StatusInternalServerError, lang, t.ErrGenericHeading, t.ErrSaveMsg)
	}
}

// decode extracts and validates the token from the request query.
func (h *Handlers) decode(r *http.Request) (callerID string, callTime int64, lang string, ok bool) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		return "", 0, "", false
	}
	cid, ts, lng, err := token.Decrypt(h.secret, tok)
	if err != nil {
		return "", 0, "", false
	}
	if len(cid) == 0 || len(cid) > 32 {
		return "", 0, "", false
	}
	return cid, ts, normalizeLang(lng), true
}

func (h *Handlers) alreadyUsed(callerID string, callTime int64) bool {
	var n int
	err := h.db.QueryRow(
		`SELECT COUNT(*) FROM used_tokens WHERE caller_id = ? AND call_time = ?`,
		callerID, callTime,
	).Scan(&n)
	return err == nil && n > 0
}

type storeResult int

const (
	storeOK storeResult = iota
	storeAlreadyUsed
	storeError
)

// store inserts the response and marks the token used in one transaction. The
// used_tokens PRIMARY KEY enforces single submission.
func (h *Handlers) store(callerID string, callTime int64, s submission) storeResult {
	now := time.Now().Unix()
	tx, err := h.db.Begin()
	if err != nil {
		log.Printf("survey: begin tx: %v", err)
		return storeError
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO responses(caller_id, call_time, csat, resolution, ces, comment, submitted_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		callerID, callTime, s.csat, s.resolution, s.ces, s.comment, now,
	)
	if err != nil {
		log.Printf("survey: insert response: %v", err)
		return storeError
	}
	respID, _ := res.LastInsertId()

	if _, err := tx.Exec(
		`INSERT INTO used_tokens(caller_id, call_time, used_at, response_id) VALUES(?, ?, ?, ?)`,
		callerID, callTime, now, respID,
	); err != nil {
		if isUniqueViolation(err) {
			return storeAlreadyUsed
		}
		log.Printf("survey: insert used_token: %v", err)
		return storeError
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			return storeAlreadyUsed
		}
		log.Printf("survey: commit: %v", err)
		return storeError
	}
	return storeOK
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "CONSTRAINT FAILED")
}

// ---- branding ----

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

// Logo serves the resolved logo file (or 404 when none is available). The path
// is resolved per request, so dropping/replacing a logo file takes effect
// without a restart; no-cache lets browsers pick up a replacement (via a
// conditional request) rather than serving a stale image.
func (h *Handlers) Logo(w http.ResponseWriter, r *http.Request) {
	path := h.cfg.Branding.ResolveLogo()
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

// ThemeCSS serves a tiny stylesheet that sets the brand color as a CSS variable.
// Done as a same-origin stylesheet so the strict CSP (no inline styles) holds.
func (h *Handlers) ThemeCSS(w http.ResponseWriter, r *http.Request) {
	color := h.cfg.Branding.ThemeColor
	if !hexColorRE.MatchString(color) {
		color = "#2563eb"
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, ":root{--brand:%s}", color)
}

// ---- rendering ----

func (h *Handlers) base(lang string) pageBase {
	return pageBase{
		SiteName: h.cfg.Site.Name,
		Lang:     normalizeLang(lang),
		LogoURL:  h.cfg.Branding.LogoURL(),
	}
}

// write renders a template into a buffer first, so headers/status are set
// correctly and a template error never emits a half-written page.
func (h *Handlers) write(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := h.tmpl.Render(&buf, name, data); err != nil {
		log.Printf("survey: render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (h *Handlers) render(w http.ResponseWriter, name string, data any) {
	h.write(w, http.StatusOK, name, data)
}

func (h *Handlers) renderDone(w http.ResponseWriter, lang, title, msg string) {
	h.write(w, http.StatusOK, "survey_done.tmpl", doneData{h.base(lang), title, msg})
}

func (h *Handlers) renderError(w http.ResponseWriter, status int, lang, heading, msg string) {
	h.write(w, status, "survey_error.tmpl", errData{h.base(lang), heading, msg})
}

func (h *Handlers) renderInvalid(w http.ResponseWriter, lang string) {
	t := stringsFor(lang)
	h.renderError(w, http.StatusBadRequest, lang, t.ErrInvalidHeading, t.ErrInvalidMsg)
}

func scale(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}

// scaleDesc returns hi, hi-1, ... 1 (for the reverse-DOM star widget).
func scaleDesc(hi int) []int {
	out := make([]int, 0, hi)
	for i := hi; i >= 1; i-- {
		out = append(out, i)
	}
	return out
}
