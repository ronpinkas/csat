// Package survey serves the public, tokenized survey form (defined by a
// survey.json) and records responses.
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
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/token"
	"github.com/ronpinkas/csat/internal/web"
)

// Handlers serves the public survey + branding routes.
type Handlers struct {
	provider tenant.Provider
	tmpl     *web.Templates
	cfg      *config.Config
	def      *surveydef.Definition
	secret   string
	secure   bool
}

// New builds the survey handlers for the given survey definition.
func New(provider tenant.Provider, tmpl *web.Templates, cfg *config.Config, def *surveydef.Definition, secret string, secure bool) *Handlers {
	return &Handlers{provider: provider, tmpl: tmpl, cfg: cfg, def: def, secret: secret, secure: secure}
}

type pageBase struct {
	SiteName string
	Wide     bool
	Lang     string
	LogoURL  string
}

// qView is one question prepared for rendering.
type qView struct {
	Key         string
	Type        string
	Label       string
	Required    bool
	Stars       []int // descending (high->low) for the reverse-DOM star widget
	Scale       []int // ascending min..max for scale/nps
	EndLow      string
	EndHigh     string
	Options     []optView
	MaxLen      int
	Placeholder string
}

type optView struct {
	Value string
	Label string
}

type surveyData struct {
	pageBase
	Token     string
	CSRF      string
	Intro     string
	Submit    string
	Questions []qView
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
	subject, subjectTime, lang, ref, ok := h.decode(r)
	if !ok {
		h.renderInvalid(w, "en")
		return
	}
	db, err := h.provider.DB(ref)
	if err != nil {
		h.renderInvalid(w, lang)
		return
	}
	if h.alreadyUsed(db, subject, subjectTime) {
		t := stringsFor(lang)
		h.renderDone(w, lang, t.DoneTitle, t.AlreadyMsg)
		return
	}
	t := stringsFor(lang)
	h.render(w, "survey.tmpl", surveyData{
		pageBase:  h.base(lang),
		Token:     r.URL.Query().Get("t"),
		CSRF:      csrf.Issue(w, h.secure),
		Intro:     h.def.IntroFor(lang),
		Submit:    t.Submit,
		Questions: h.questions(lang),
	})
}

// Submit handles POST /s — re-validate the token, validate input, store once.
func (h *Handlers) Submit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderInvalid(w, "en")
		return
	}
	subject, subjectTime, lang, ref, ok := h.decode(r)
	if !ok {
		h.renderInvalid(w, "en")
		return
	}
	db, err := h.provider.DB(ref)
	if err != nil {
		h.renderInvalid(w, lang)
		return
	}
	t := stringsFor(lang)
	if !csrf.Check(r) {
		h.renderError(w, http.StatusForbidden, lang, t.ErrGenericHeading, t.ErrSessionMsg)
		return
	}
	answers, valid := parseAnswers(r.PostForm, h.def)
	if !valid {
		h.renderError(w, http.StatusBadRequest, lang, t.ErrFormHeading, t.ErrFormMsg)
		return
	}

	switch h.store(db, subject, subjectTime, lang, answers) {
	case storeOK:
		h.renderDone(w, lang, t.DoneTitle, h.def.ThanksFor(lang))
	case storeAlreadyUsed:
		h.renderDone(w, lang, t.DoneTitle, t.AlreadyMsg)
	default:
		h.renderError(w, http.StatusInternalServerError, lang, t.ErrGenericHeading, t.ErrSaveMsg)
	}
}

// questions builds the localized render models for the form.
func (h *Handlers) questions(lang string) []qView {
	out := make([]qView, 0, len(h.def.Questions))
	for _, q := range h.def.Questions {
		v := qView{
			Key:         q.Key,
			Type:        q.Type,
			Label:       q.LabelFor(lang),
			Required:    q.Required,
			EndLow:      q.EndLow(lang),
			EndHigh:     q.EndHigh(lang),
			MaxLen:      q.MaxLen,
			Placeholder: q.PlaceholderFor(lang),
		}
		switch q.Type {
		case surveydef.TypeStars:
			v.Stars = descend(q.Max)
		case surveydef.TypeScale, surveydef.TypeNPS:
			v.Scale = q.Scale()
		case surveydef.TypeChoice, surveydef.TypeMultiChoice:
			for _, o := range q.Options {
				v.Options = append(v.Options, optView{Value: o.Value, Label: o.LabelFor(lang)})
			}
		}
		out = append(out, v)
	}
	return out
}

// decode extracts and validates the token from the request query, returning the
// tenant ref it carries ("" for a single-tenant/legacy token).
func (h *Handlers) decode(r *http.Request) (subject string, subjectTime int64, lang, ref string, ok bool) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		return "", 0, "", "", false
	}
	subj, ts, lng, rf, err := token.Decrypt(h.secret, tok)
	if err != nil {
		return "", 0, "", "", false
	}
	if len(subj) == 0 || len(subj) > 128 {
		return "", 0, "", "", false
	}
	return subj, ts, normalizeLang(lng), rf, true
}

func (h *Handlers) alreadyUsed(db *sql.DB, subject string, subjectTime int64) bool {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM used_tokens WHERE subject = ? AND subject_time = ?`,
		subject, subjectTime,
	).Scan(&n)
	return err == nil && n > 0
}

type storeResult int

const (
	storeOK storeResult = iota
	storeAlreadyUsed
	storeError
)

// store inserts the response, its answers, and the used-token marker in one
// transaction. The used_tokens PRIMARY KEY enforces single submission.
func (h *Handlers) store(db *sql.DB, subject string, subjectTime int64, lang string, answers []answer) storeResult {
	now := time.Now().Unix()
	tx, err := db.Begin()
	if err != nil {
		log.Printf("survey: begin tx: %v", err)
		return storeError
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at) VALUES(?, ?, ?, ?)`,
		subject, subjectTime, lang, now,
	)
	if err != nil {
		log.Printf("survey: insert response: %v", err)
		return storeError
	}
	respID, _ := res.LastInsertId()

	for _, a := range answers {
		if _, err := tx.Exec(
			`INSERT INTO answers(response_id, question_key, num, text) VALUES(?, ?, ?, ?)`,
			respID, a.key, numArg(a.num), textArg(a.text),
		); err != nil {
			log.Printf("survey: insert answer %q: %v", a.key, err)
			return storeError
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO used_tokens(subject, subject_time, used_at, response_id) VALUES(?, ?, ?, ?)`,
		subject, subjectTime, now, respID,
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

func numArg(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}
func textArg(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "CONSTRAINT FAILED")
}

// ---- branding ----

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

// Logo serves the resolved logo file (or 404 when none is available), resolved
// per request so dropping/replacing a logo takes effect without a restart.
func (h *Handlers) Logo(w http.ResponseWriter, r *http.Request) {
	path := h.cfg.Branding.ResolveLogo()
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

// ThemeCSS serves a tiny stylesheet setting the brand color as a CSS variable
// (a same-origin stylesheet, so the strict no-inline-styles CSP holds).
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

// descend returns hi, hi-1, ... 1 (for the reverse-DOM star widget).
func descend(hi int) []int {
	out := make([]int, 0, hi)
	for i := hi; i >= 1; i-- {
		out = append(out, i)
	}
	return out
}
