package admin

import (
	"encoding/csv"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"
)

type dashboardView struct {
	base
	From string
	To   string
	TZ   string
}

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	_, _, info, _ := a.parseRange(r)
	b := a.newBase(r)
	b.Wide = true
	a.render(w, http.StatusOK, "dashboard.tmpl", dashboardView{
		base: b,
		From: info.From, To: info.To, TZ: info.TZ,
	})
}

func (a *Admin) analytics(w http.ResponseWriter, r *http.Request) {
	from, to, info, loc := a.parseRange(r)

	kpis, err := queryKPIs(a.db, from, to)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	csatDist, err := queryDistribution(a.db, from, to, "csat", 1, max(a.cfg.Survey.CSATMax, 5))
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	cesDist, err := queryDistribution(a.db, from, to, "ces", 1, max(a.cfg.Survey.CESMax, 7))
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	res, err := queryResolution(a.db, from, to)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	trend, err := queryTrend(a.db, from, to, loc)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}

	kpis.CSATAvg = round2(kpis.CSATAvg)
	kpis.CSATPct = round2(kpis.CSATPct)
	kpis.CESAvg = round2(kpis.CESAvg)
	kpis.ResolutionRate = round2(kpis.ResolutionRate * 100)

	writeJSON(w, AnalyticsResult{
		Range: info, KPIs: kpis,
		CSATDistribution: csatDist, CESDistribution: cesDist,
		Resolution: res, Trend: trend,
	})
}

type commentRow struct {
	SubmittedAt int64  `json:"submitted_at"`
	CSAT        int    `json:"csat"`
	Resolution  string `json:"resolution"`
	CES         int    `json:"ces"`
	Comment     string `json:"comment"`
}

func (a *Admin) comments(w http.ResponseWriter, r *http.Request) {
	from, to, _, _ := a.parseRange(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	const limit = 20
	rows, err := a.db.Query(
		`SELECT submitted_at, csat, resolution, ces, comment FROM responses
		 WHERE submitted_at >= ? AND submitted_at < ? AND comment <> ''
		 ORDER BY submitted_at DESC LIMIT ? OFFSET ?`,
		from, to, limit, page*limit,
	)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []commentRow{}
	for rows.Next() {
		var c commentRow
		if err := rows.Scan(&c.SubmittedAt, &c.CSAT, &c.Resolution, &c.CES, &c.Comment); err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		out = append(out, c)
	}
	writeJSON(w, map[string]any{"page": page, "comments": out})
}

type settingsView struct {
	base
	Secret  string
	KeyPath string
}

func (a *Admin) settings(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusOK, "settings.tmpl", settingsView{
		base:    a.newBase(r),
		Secret:  a.secret,
		KeyPath: a.cfg.Security.CryptoKeyPath,
	})
}

func (a *Admin) exportCSV(w http.ResponseWriter, r *http.Request) {
	from, to, info, _ := a.parseRange(r)
	rows, err := a.db.Query(
		`SELECT id, submitted_at, caller_id, call_time, csat, resolution, ces, comment
		 FROM responses WHERE submitted_at >= ? AND submitted_at < ? ORDER BY submitted_at`,
		from, to,
	)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"csat-"+info.From+"_"+info.To+".csv\"")
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"id", "submitted_at_utc", "caller_id", "call_time_utc", "csat", "resolution", "ces", "comment"})

	for rows.Next() {
		var id, submittedAt, callTime int64
		var callerID, resolution, comment string
		var csat, ces int
		if err := rows.Scan(&id, &submittedAt, &callerID, &callTime, &csat, &resolution, &ces, &comment); err != nil {
			log.Printf("admin: export scan: %v", err)
			return
		}
		_ = cw.Write([]string{
			strconv.FormatInt(id, 10),
			time.Unix(submittedAt, 0).UTC().Format(time.RFC3339),
			csvSafe(callerID),
			time.Unix(callTime, 0).UTC().Format(time.RFC3339),
			strconv.Itoa(csat),
			resolution,
			strconv.Itoa(ces),
			csvSafe(comment),
		})
	}
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
	to = toDate.AddDate(0, 0, 1).Unix() // exclusive end-of-day
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
