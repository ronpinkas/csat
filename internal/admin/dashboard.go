package admin

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/brandstore"
	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/surveydef"
)

// resolveSet picks the question set for an admin view: ?set=<id> when valid,
// otherwise the latest set (seeded if absent). It also returns the full set list
// for the dashboard selector.
func (a *Admin) resolveSet(db *sql.DB, r *http.Request) (*surveydef.Definition, int64, []defstore.Version, error) {
	versions, err := defstore.List(db)
	if err != nil {
		return nil, 0, nil, err
	}
	if s := r.URL.Query().Get("set"); s != "" {
		if id, perr := strconv.ParseInt(s, 10, 64); perr == nil {
			if d, derr := defstore.ByID(db, id); derr == nil {
				return d, id, versions, nil
			}
		}
	}
	d, id, err := defstore.Resolve(db, a.def, time.Now().Unix())
	return d, id, versions, err
}

type dashboardView struct {
	base
	From  string
	To    string
	TZ    string
	Sets  []defstore.Version
	SetID int64
}

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	_, _, info, _ := a.parseRange(r)
	_, setID, sets, _ := a.resolveSet(tenantDB(r.Context()), r)
	b := a.newBase(r)
	b.Wide = true
	a.render(w, http.StatusOK, "dashboard.tmpl", dashboardView{
		base: b, From: info.From, To: info.To, TZ: info.TZ, Sets: sets, SetID: setID,
	})
}

func (a *Admin) analytics(w http.ResponseWriter, r *http.Request) {
	from, to, info, loc := a.parseRange(r)
	db := tenantDB(r.Context())
	def, defID, _, err := a.resolveSet(db, r)
	if err != nil {
		log.Printf("admin: resolve set: %v", err)
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	res, err := computeAnalytics(db, def, defID, from, to, loc, info)
	if err != nil {
		log.Printf("admin: analytics: %v", err)
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

type commentRow struct {
	SubmittedAt int64  `json:"submitted_at"`
	Lang        string `json:"lang"`
	Question    string `json:"question"`
	Text        string `json:"text"`
}

func (a *Admin) comments(w http.ResponseWriter, r *http.Request) {
	from, to, _, _ := a.parseRange(r)
	db := tenantDB(r.Context())
	def, defID, _, err := a.resolveSet(db, r)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	const limit = 25

	labels := map[string]string{}
	var keys []string
	for _, q := range def.Questions {
		if q.Type == surveydef.TypeText {
			keys = append(keys, q.Key)
			labels[q.Key] = q.LabelFor("en")
		}
	}
	out := []commentRow{}
	if len(keys) == 0 {
		writeJSON(w, map[string]any{"page": page, "comments": out})
		return
	}

	ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys)+5)
	for _, k := range keys {
		args = append(args, k)
	}
	args = append(args, from, to, defID, limit, page*limit)
	rows, err := db.Query(
		`SELECT r.submitted_at, r.lang, a.question_key, a.text
		 FROM answers a JOIN responses r ON a.response_id = r.id
		 WHERE a.question_key IN (`+ph+`) AND a.text IS NOT NULL AND a.text <> ''
		   AND r.submitted_at >= ? AND r.submitted_at < ? AND r.definition_id = ?
		 ORDER BY r.submitted_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var c commentRow
		var key string
		if err := rows.Scan(&c.SubmittedAt, &c.Lang, &key, &c.Text); err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		c.Question = labels[key]
		out = append(out, c)
	}
	writeJSON(w, map[string]any{"page": page, "comments": out})
}

type settingsView struct {
	base
	Secret     string
	KeyPath    string
	SiteName   string
	ThemeColor string
	HasLogo    bool
	Error      string
	Saved      bool
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func (a *Admin) settings(w http.ResponseWriter, r *http.Request) {
	a.renderSettings(w, r, "", false)
}

func (a *Admin) renderSettings(w http.ResponseWriter, r *http.Request, errMsg string, saved bool) {
	db := tenantDB(r.Context())
	b := brandstore.Resolve(db, a.cfg.Site.Name, a.cfg.Branding.ThemeColor)
	_, hasLogo := brandstore.LogoVersion(db)
	a.render(w, http.StatusOK, "settings.tmpl", settingsView{
		base:       a.newBase(r),
		Secret:     a.secret,
		KeyPath:    a.cfg.Security.CryptoKeyPath,
		SiteName:   b.SiteName,
		ThemeColor: b.ThemeColor,
		HasLogo:    hasLogo,
		Error:      errMsg,
		Saved:      saved,
	})
}

// saveSettings updates the tenant's branding (name, theme color, optional logo
// upload). Multipart form.
func (a *Admin) saveSettings(w http.ResponseWriter, r *http.Request) {
	db := tenantDB(r.Context())
	_ = r.ParseMultipartForm(2 << 20) // 2 MiB
	name := strings.TrimSpace(r.PostFormValue("site_name"))
	color := strings.TrimSpace(r.PostFormValue("theme_color"))
	if color != "" && !hexColorRE.MatchString(color) {
		a.renderSettings(w, r, "Theme color must be a hex value like #2563eb.", false)
		return
	}
	now := time.Now().Unix()
	if err := brandstore.Save(db, name, color, now); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if f, hdr, err := r.FormFile("logo"); err == nil {
		defer f.Close()
		blob, rerr := io.ReadAll(io.LimitReader(f, 1<<20)) // 1 MiB cap
		if rerr != nil {
			a.renderSettings(w, r, "Could not read the uploaded logo.", false)
			return
		}
		if len(blob) > 0 {
			ctype := hdr.Header.Get("Content-Type")
			if !strings.HasPrefix(ctype, "image/") {
				a.renderSettings(w, r, "The logo must be an image file (PNG, SVG, JPG, …).", false)
				return
			}
			if err := brandstore.SaveLogo(db, blob, ctype, now); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}
	}
	a.renderSettings(w, r, "", true)
}

func (a *Admin) exportCSV(w http.ResponseWriter, r *http.Request) {
	from, to, info, _ := a.parseRange(r)
	db := tenantDB(r.Context())
	def, defID, _, err := a.resolveSet(db, r)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	rows, err := db.Query(
		`SELECT r.id, r.submitted_at, r.subject, r.subject_time, r.lang, a.question_key, a.num, a.text
		 FROM responses r LEFT JOIN answers a ON a.response_id = r.id
		 WHERE r.submitted_at >= ? AND r.submitted_at < ? AND r.definition_id = ? ORDER BY r.id`, from, to, defID)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"survey-"+info.From+"_"+info.To+".csv\"")
	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{"id", "submitted_at_utc", "subject", "subject_time_utc", "lang"}
	for _, q := range def.Questions {
		header = append(header, q.Key)
	}
	_ = cw.Write(header)

	var (
		curID                 int64
		haveRow               bool
		submittedAt, subjTime int64
		subject, lang         string
		vals                  map[string]string
	)
	flush := func() {
		if !haveRow {
			return
		}
		rec := []string{
			strconv.FormatInt(curID, 10),
			time.Unix(submittedAt, 0).UTC().Format(time.RFC3339),
			csvSafe(subject),
			time.Unix(subjTime, 0).UTC().Format(time.RFC3339),
			lang,
		}
		for _, q := range def.Questions {
			rec = append(rec, csvSafe(vals[q.Key]))
		}
		_ = cw.Write(rec)
	}

	for rows.Next() {
		var id, sAt, sTime int64
		var subj, lng string
		var qkey, txt *string
		var num *int64
		if err := rows.Scan(&id, &sAt, &subj, &sTime, &lng, &qkey, &num, &txt); err != nil {
			log.Printf("admin: export scan: %v", err)
			return
		}
		if !haveRow || id != curID {
			flush()
			curID, submittedAt, subjTime, subject, lang = id, sAt, sTime, subj, lng
			vals = map[string]string{}
			haveRow = true
		}
		if qkey != nil {
			v := ""
			if num != nil {
				v = strconv.FormatInt(*num, 10)
			} else if txt != nil {
				v = *txt
			}
			if existing, ok := vals[*qkey]; ok && existing != "" {
				vals[*qkey] = existing + ";" + v // multichoice
			} else {
				vals[*qkey] = v
			}
		}
	}
	flush()
}

// ---- helpers ----

func (a *Admin) parseRange(r *http.Request) (from, to int64, info RangeInfo, loc *time.Location) {
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		tz = a.cfg.Site.DisplayTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, tz = time.UTC, "UTC"
	}
	now := time.Now().In(loc)
	toDate := parseDate(r.URL.Query().Get("to"), loc, dateOf(now))
	fromDate := parseDate(r.URL.Query().Get("from"), loc, dateOf(now.AddDate(0, 0, -29)))
	if toDate.Before(fromDate) {
		fromDate, toDate = toDate, fromDate
	}
	from = fromDate.Unix()
	to = toDate.AddDate(0, 0, 1).Unix()
	info = RangeInfo{From: fromDate.Format("2006-01-02"), To: toDate.Format("2006-01-02"), TZ: tz}
	return
}

func parseDate(s string, loc *time.Location, def time.Time) time.Time {
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t
	}
	return def
}

func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// csvSafe neutralizes CSV/formula injection by prefixing risky leading chars.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
