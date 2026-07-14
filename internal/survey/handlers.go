// Package survey serves the public, tokenized survey form (defined by a
// survey.json) and records responses.
package survey

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/brandstore"
	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/csrf"
	"github.com/ronpinkas/csat/internal/defstore"
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
	Ref      string // tenant ref, so the layout's theme.css link is tenant-scoped
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
	Value       string // prefilled/default value (all types)
	Widget      string // choice: "select" -> dropdown
	Locked      bool   // render read-only (only when a prefill value is present)
	IsSection   bool   // display-only heading
	ShowIfKey   string // controlling question key ("" if unconditional)
	ShowIfVals  string // JSON-encoded list of matching values, for the data attribute
}

type optView struct {
	Value    string
	Label    string
	Selected bool
}

type surveyData struct {
	pageBase
	Token     string
	CSRF      string
	SetID     int64 // the question set this form was rendered with
	Intro     string
	Submit    string
	Questions []qView

	// Save-progress UI. Only populated when the survey has allow_save.
	AllowSave    bool
	ForceConfirm bool
	SaveLabel    string
	Saved        bool // flash shown after a no-JS save round-trip
	SavedMsg     string
	ConfirmTitle string
	Cancel       string
	Saving       string
	SavedNote    string
	SaveFailed   string

	// Required-question affordances.
	HasRequired    bool
	RequiredLegend string
	RequiredLeft   string
	// Resumed means this form was rebuilt from a saved draft. The respondent has
	// already saved once, so the questions still to answer are flagged on sight
	// rather than waiting for them to go looking.
	Resumed bool
}

// hasRequired reports whether any answerable question must be answered, so the
// form only shows the "* Required" key when it actually means something.
func hasRequired(def *surveydef.Definition) bool {
	for _, q := range def.Questions {
		if q.Required && q.Type != surveydef.TypeSection {
			return true
		}
	}
	return false
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
		h.renderInvalid(w, nil, "", "en")
		return
	}
	db, err := h.provider.DB(ref)
	if err != nil {
		h.renderInvalid(w, nil, "", lang)
		return
	}
	def, setID, err := h.resolveFormDef(db, r)
	if err != nil {
		h.renderInvalid(w, db, ref, lang)
		return
	}
	if h.alreadyUsed(db, subject, subjectTime) {
		t := stringsFor(lang)
		h.renderDone(w, db, ref, lang, t.DoneTitle, t.AlreadyMsg)
		return
	}
	// A server-side draft (allow_save surveys) is authoritative: it beats the
	// browser's local copy, which is what makes resuming on another device work.
	var draft map[string][]string
	if def.AllowSave {
		draft = loadDraft(db, subject, subjectTime)
	}

	t := stringsFor(lang)
	h.render(w, "survey.tmpl", surveyData{
		pageBase:     h.base(lang, ref, db),
		Token:        r.URL.Query().Get("t"),
		CSRF:         csrf.Issue(w, h.secure),
		SetID:        setID,
		Intro:        def.IntroFor(lang),
		Submit:       t.Submit,
		Questions:    h.questions(def, lang, r.URL.Query(), draft),
		AllowSave:    def.AllowSave,
		ForceConfirm: def.ForceConfirm,
		SaveLabel:    t.SaveProgress,
		Saved:        r.URL.Query().Get("saved") != "",
		SavedMsg:     t.SavedMsg,
		ConfirmTitle: t.ConfirmTitle,
		Cancel:       t.Cancel,
		Saving:       t.Saving,
		SavedNote:    t.SavedNote,
		SaveFailed:   t.SaveFailed,

		HasRequired:    hasRequired(def),
		RequiredLegend: t.RequiredLegend,
		RequiredLeft:   t.RequiredLeft,
		Resumed:        len(draft) > 0,
	})
}

// resolveFormDef picks the question set for a survey form: an explicit &set=<id>
// when valid, otherwise the latest set (seeded on first touch).
func (h *Handlers) resolveFormDef(db *sql.DB, r *http.Request) (*surveydef.Definition, int64, error) {
	if s := r.URL.Query().Get("set"); s != "" {
		// Numeric -> an exact set id; otherwise treat it as a survey NAME and
		// follow the newest set with that name (so a phone's CSAT Survey targets
		// the current version even after re-publishing). Either miss -> default.
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			if d, derr := defstore.ByID(db, id); derr == nil {
				return d, id, nil
			}
		} else if d, id, derr := defstore.ByName(db, s); derr == nil {
			return d, id, nil
		}
	}
	return defstore.Resolve(db, h.def, time.Now().Unix())
}

// Submit handles POST /s — re-validate the token, validate input, store once.
func (h *Handlers) Submit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderInvalid(w, nil, "", "en")
		return
	}
	subject, subjectTime, lang, ref, ok := h.decode(r)
	if !ok {
		h.renderInvalid(w, nil, "", "en")
		return
	}
	db, err := h.provider.DB(ref)
	if err != nil {
		h.renderInvalid(w, nil, "", lang)
		return
	}
	t := stringsFor(lang)
	if !csrf.Check(r) {
		h.renderError(w, db, ref, http.StatusForbidden, lang, t.ErrGenericHeading, t.ErrSessionMsg)
		return
	}
	// Validate + tag against the exact set this form was rendered with.
	def, setID := h.resolveSubmitDef(db, r)

	// No-JavaScript fallback for the Save button: store a draft (partial answers
	// allowed) and return to the form. The link is NOT consumed.
	if r.PostFormValue("action") == "save" && def.AllowSave {
		answers, valid := parseAnswers(r.PostForm, def, false)
		if !valid {
			h.renderError(w, db, ref, http.StatusBadRequest, lang, t.ErrFormHeading, t.ErrFormMsg)
			return
		}
		if err := h.saveDraft(db, setID, subject, subjectTime, lang, answers); err != nil {
			log.Printf("survey: save draft: %v", err)
			h.renderError(w, db, ref, http.StatusInternalServerError, lang, t.ErrGenericHeading, t.ErrSaveMsg)
			return
		}
		http.Redirect(w, r, "/s?t="+url.QueryEscape(r.URL.Query().Get("t"))+"&saved=1", http.StatusSeeOther)
		return
	}

	answers, valid := parseAnswers(r.PostForm, def, true) // submit: required questions must be answered
	if !valid {
		h.renderError(w, db, ref, http.StatusBadRequest, lang, t.ErrFormHeading, t.ErrFormMsg)
		return
	}

	switch h.store(db, setID, subject, subjectTime, lang, answers) {
	case storeOK:
		h.renderDone(w, db, ref, lang, t.DoneTitle, def.ThanksFor(lang))
	case storeAlreadyUsed:
		h.renderDone(w, db, ref, lang, t.DoneTitle, t.AlreadyMsg)
	default:
		h.renderError(w, db, ref, http.StatusInternalServerError, lang, t.ErrGenericHeading, t.ErrSaveMsg)
	}
}

// Save handles POST /s/save — store a partial response for this link WITHOUT
// consuming its one-time token, so it can be resumed later on any device. Used
// by the explicit Save button and by the background autosave. Returns 204 on
// success; the client surfaces failures rather than silently losing work.
func (h *Handlers) Save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	subject, subjectTime, lang, ref, ok := h.decode(r)
	if !ok {
		http.Error(w, "invalid link", http.StatusBadRequest)
		return
	}
	db, err := h.provider.DB(ref)
	if err != nil {
		http.Error(w, "invalid link", http.StatusBadRequest)
		return
	}
	if !csrf.Check(r) {
		http.Error(w, "invalid session", http.StatusForbidden)
		return
	}
	// A submitted link is finished — there is nothing left to save into.
	if h.alreadyUsed(db, subject, subjectTime) {
		http.Error(w, "already submitted", http.StatusConflict)
		return
	}
	def, setID := h.resolveSubmitDef(db, r)
	if !def.AllowSave {
		http.Error(w, "saving is not enabled for this survey", http.StatusForbidden)
		return
	}
	// Lenient: a draft may be partial. Values that ARE present are still fully
	// validated, so a draft can never carry junk into the database.
	answers, valid := parseAnswers(r.PostForm, def, false)
	if !valid {
		http.Error(w, "invalid answer", http.StatusBadRequest)
		return
	}
	if err := h.saveDraft(db, setID, subject, subjectTime, lang, answers); err != nil {
		log.Printf("survey: save draft: %v", err)
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// questions builds the localized render models for the form. query carries the
// request URL parameters (for per-question URL prefills) and draft carries a
// saved server-side draft, if any.
//
// Value priority: saved draft > URL prefill > static default. The draft wins
// because it is the respondent's own work; a prefill is only a starting point.
func (h *Handlers) questions(def *surveydef.Definition, lang string, query url.Values, draft map[string][]string) []qView {
	out := make([]qView, 0, len(def.Questions))
	for _, q := range def.Questions {
		if q.Type == surveydef.TypeSection {
			out = append(out, qView{Key: q.Key, Type: q.Type, Label: q.LabelFor(lang), IsSection: true})
			continue
		}
		v := qView{
			Key:         q.Key,
			Type:        q.Type,
			Label:       q.LabelFor(lang),
			Required:    q.Required,
			EndLow:      q.EndLow(lang),
			EndHigh:     q.EndHigh(lang),
			MaxLen:      q.MaxLen,
			Placeholder: q.PlaceholderFor(lang),
			Widget:      q.Widget,
		}
		if q.ShowIf != nil {
			v.ShowIfKey = q.ShowIf.Key
			if b, err := json.Marshal(q.ShowIf.In); err == nil {
				v.ShowIfVals = string(b)
			}
		}

		// Saved answers first (already validated when they were saved); a
		// multichoice question can carry several.
		vals := draft[q.Key]
		if len(vals) == 0 {
			pv := q.Default
			if q.PrefillParam != "" {
				if p := strings.TrimSpace(query.Get(q.PrefillParam)); p != "" {
					pv = p
				}
			}
			if pv != "" && q.AcceptsValue(pv) {
				vals = []string{pv}
			}
		}
		if len(vals) > 0 {
			v.Value = vals[0]
		}
		v.Locked = q.Locked && len(vals) > 0 // lock only when there's a value to lock to

		switch q.Type {
		case surveydef.TypeStars:
			v.Stars = descend(q.Max)
		case surveydef.TypeScale, surveydef.TypeNPS:
			v.Scale = q.Scale()
		case surveydef.TypeChoice, surveydef.TypeMultiChoice:
			for _, o := range q.Options {
				v.Options = append(v.Options, optView{
					Value:    o.Value,
					Label:    o.LabelFor(lang),
					Selected: containsValue(vals, o.Value),
				})
			}
		}
		out = append(out, v)
	}
	return out
}

func containsValue(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
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
// resolveSubmitDef loads the set the form carried in its hidden "set" field
// (the exact one rendered), falling back to the latest set.
func (h *Handlers) resolveSubmitDef(db *sql.DB, r *http.Request) (*surveydef.Definition, int64) {
	if s := r.PostFormValue("set"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			if d, derr := defstore.ByID(db, id); derr == nil {
				return d, id
			}
		}
	}
	d, id, _ := defstore.Resolve(db, h.def, time.Now().Unix())
	return d, id
}

func (h *Handlers) store(db *sql.DB, defID int64, subject string, subjectTime int64, lang string, answers []answer) storeResult {
	now := time.Now().Unix()
	tx, err := db.Begin()
	if err != nil {
		log.Printf("survey: begin tx: %v", err)
		return storeError
	}
	defer tx.Rollback()

	// Reuse this link's open draft, if it has one, so save-then-submit leaves
	// exactly one response row (never a draft plus a duplicate final row).
	respID := draftID(tx, subject, subjectTime)
	if respID == 0 {
		res, err := tx.Exec(
			`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id, incomplete)
			 VALUES(?, ?, ?, ?, ?, 0)`,
			subject, subjectTime, lang, now, defID,
		)
		if err != nil {
			log.Printf("survey: insert response: %v", err)
			return storeError
		}
		respID, _ = res.LastInsertId()
	} else {
		if _, err := tx.Exec(
			`UPDATE responses SET lang = ?, submitted_at = ?, definition_id = ?, incomplete = 0 WHERE id = ?`,
			lang, now, defID, respID,
		); err != nil {
			log.Printf("survey: promote draft: %v", err)
			return storeError
		}
		if _, err := tx.Exec(`DELETE FROM answers WHERE response_id = ?`, respID); err != nil {
			log.Printf("survey: clear draft answers: %v", err)
			return storeError
		}
	}

	if err := insertAnswers(tx, respID, answers); err != nil {
		log.Printf("survey: insert answers: %v", err)
		return storeError
	}

	// The used_tokens PRIMARY KEY is what makes a link one-time. It is written
	// ONLY here, on final submit — saving a draft leaves the link reusable.
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

// ---- drafts (allow_save surveys) ----

// draftID returns the id of this link's open draft response, or 0 if it has none.
func draftID(tx *sql.Tx, subject string, subjectTime int64) int64 {
	var id int64
	err := tx.QueryRow(
		`SELECT id FROM responses WHERE subject = ? AND subject_time = ? AND incomplete = 1 ORDER BY id DESC LIMIT 1`,
		subject, subjectTime,
	).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}

// loadDraft returns the saved answers for this link's open draft, keyed by
// question, so the form can be re-rendered exactly as the respondent left it.
func loadDraft(db *sql.DB, subject string, subjectTime int64) map[string][]string {
	var respID int64
	if err := db.QueryRow(
		`SELECT id FROM responses WHERE subject = ? AND subject_time = ? AND incomplete = 1 ORDER BY id DESC LIMIT 1`,
		subject, subjectTime,
	).Scan(&respID); err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT question_key, num, text FROM answers WHERE response_id = ?`, respID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var key string
		var num sql.NullInt64
		var text sql.NullString
		if err := rows.Scan(&key, &num, &text); err != nil {
			return nil
		}
		switch {
		case num.Valid:
			out[key] = append(out[key], strconv.FormatInt(num.Int64, 10))
		case text.Valid:
			out[key] = append(out[key], text.String)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

// saveDraft inserts or updates this link's draft response (incomplete = 1) and
// replaces its answers. It deliberately does NOT touch used_tokens, so the link
// stays open and can be resumed — on this device or any other.
func (h *Handlers) saveDraft(db *sql.DB, defID int64, subject string, subjectTime int64, lang string, answers []answer) error {
	now := time.Now().Unix()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	id := draftID(tx, subject, subjectTime)
	if id == 0 {
		res, err := tx.Exec(
			`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id, incomplete)
			 VALUES(?, ?, ?, ?, ?, 1)`,
			subject, subjectTime, lang, now, defID,
		)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
	} else {
		if _, err := tx.Exec(
			`UPDATE responses SET lang = ?, submitted_at = ?, definition_id = ? WHERE id = ?`,
			lang, now, defID, id,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM answers WHERE response_id = ?`, id); err != nil {
			return err
		}
	}
	if err := insertAnswers(tx, id, answers); err != nil {
		return err
	}
	return tx.Commit()
}

func insertAnswers(tx *sql.Tx, respID int64, answers []answer) error {
	for _, a := range answers {
		if _, err := tx.Exec(
			`INSERT INTO answers(response_id, question_key, num, text) VALUES(?, ?, ?, ?)`,
			respID, a.key, numArg(a.num), textArg(a.text),
		); err != nil {
			return err
		}
	}
	return nil
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

// Logo serves the tenant's uploaded logo (from ?ref) when set, otherwise the
// deployment logo file (or 404 when none is available).
func (h *Handlers) Logo(w http.ResponseWriter, r *http.Request) {
	if db, err := h.provider.DB(r.URL.Query().Get("ref")); err == nil {
		if blob, ctype, ok := brandstore.Logo(db); ok {
			if ctype == "" {
				ctype = http.DetectContentType(blob)
			}
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "public, max-age=300")
			_, _ = w.Write(blob)
			return
		}
	}
	path := h.cfg.Branding.ResolveLogo()
	if path == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

// ThemeCSS serves a tiny stylesheet setting the brand color as a CSS variable
// (a same-origin stylesheet, so the strict no-inline-styles CSP holds). The
// tenant is taken from ?ref so the public survey page gets the tenant's color.
func (h *Handlers) ThemeCSS(w http.ResponseWriter, r *http.Request) {
	color := h.cfg.Branding.ThemeColor
	if db, err := h.provider.DB(r.URL.Query().Get("ref")); err == nil {
		color = brandstore.Resolve(db, h.cfg.Site.Name, h.cfg.Branding.ThemeColor).ThemeColor
	}
	if !hexColorRE.MatchString(color) {
		color = "#2563eb"
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, ":root{--brand:%s}", color)
}

// ---- rendering ----

func (h *Handlers) base(lang, ref string, db *sql.DB) pageBase {
	name := h.cfg.Site.Name
	if db != nil {
		name = brandstore.Resolve(db, h.cfg.Site.Name, h.cfg.Branding.ThemeColor).SiteName
	}
	return pageBase{
		SiteName: name,
		Lang:     normalizeLang(lang),
		LogoURL:  brandstore.LogoURL(db, ref, h.cfg.Branding.LogoURL()),
		Ref:      ref,
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

func (h *Handlers) renderDone(w http.ResponseWriter, db *sql.DB, ref, lang, title, msg string) {
	h.write(w, http.StatusOK, "survey_done.tmpl", doneData{h.base(lang, ref, db), title, msg})
}

func (h *Handlers) renderError(w http.ResponseWriter, db *sql.DB, ref string, status int, lang, heading, msg string) {
	h.write(w, status, "survey_error.tmpl", errData{h.base(lang, ref, db), heading, msg})
}

func (h *Handlers) renderInvalid(w http.ResponseWriter, db *sql.DB, ref, lang string) {
	t := stringsFor(lang)
	h.renderError(w, db, ref, http.StatusBadRequest, lang, t.ErrInvalidHeading, t.ErrInvalidMsg)
}

// descend returns hi, hi-1, ... 1 (for the reverse-DOM star widget).
func descend(hi int) []int {
	out := make([]int, 0, hi)
	for i := hi; i >= 1; i-- {
		out = append(out, i)
	}
	return out
}
