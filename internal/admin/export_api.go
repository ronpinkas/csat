package admin

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/surveydef"
)

// exportRow is one survey response with its answers keyed by question key
// (multichoice values joined with ";"). Both exports — the CSV download and the
// JSON read API — are built from this one shape so they can never disagree.
type exportRow struct {
	ID             int64             `json:"id"`
	SubmittedAt    int64             `json:"submitted_at"`
	SubmittedUTC   string            `json:"submitted_at_utc"`
	Subject        string            `json:"subject"`
	SubjectTime    int64             `json:"subject_time"`
	SubjectTimeUTC string            `json:"subject_time_utc"`
	Lang           string            `json:"lang"`
	Incomplete     bool              `json:"incomplete"`
	SetID          int64             `json:"set_id"`
	Answers        map[string]string `json:"answers"`
}

// exportQuestion describes one question of the resolved set, enough for a
// consumer to find the rating and the free-text comment without guessing at
// column names.
type exportQuestion struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Label string `json:"label"`
	Min   int    `json:"min,omitempty"`
	Max   int    `json:"max,omitempty"`
}

// collectResponses returns the responses submitted in [from, to) — scoped to
// defID unless allSets — each with its answers folded in, ordered by id.
func collectResponses(db *sql.DB, from, to, defID int64, allSets, includeIncomplete bool) ([]exportRow, error) {
	q := `SELECT r.id, r.submitted_at, r.subject, r.subject_time, r.lang, r.incomplete, r.definition_id,
	             a.question_key, a.num, a.text
	      FROM responses r LEFT JOIN answers a ON a.response_id = r.id
	      WHERE r.submitted_at >= ? AND r.submitted_at < ?`
	args := []any{from, to}
	if !allSets {
		q += ` AND r.definition_id = ?`
		args = append(args, defID)
	}
	q += draftFilter("r", includeIncomplete) + ` ORDER BY r.id, a.id`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []exportRow{}
	for rows.Next() {
		var (
			id, sAt, sTime int64
			subj, lng      string
			inc            int
			setID          sql.NullInt64
			qkey, txt      *string
			num            *int64
		)
		if err := rows.Scan(&id, &sAt, &subj, &sTime, &lng, &inc, &setID, &qkey, &num, &txt); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, exportRow{
				ID:             id,
				SubmittedAt:    sAt,
				SubmittedUTC:   time.Unix(sAt, 0).UTC().Format(time.RFC3339),
				Subject:        subj,
				SubjectTime:    sTime,
				SubjectTimeUTC: time.Unix(sTime, 0).UTC().Format(time.RFC3339),
				Lang:           lng,
				Incomplete:     inc != 0,
				SetID:          setID.Int64,
				Answers:        map[string]string{},
			})
		}
		if qkey == nil {
			continue
		}
		v := ""
		if num != nil {
			v = strconv.FormatInt(*num, 10)
		} else if txt != nil {
			v = *txt
		}
		cur := &out[len(out)-1]
		if existing, ok := cur.Answers[*qkey]; ok && existing != "" {
			cur.Answers[*qkey] = existing + ";" + v // multichoice
		} else {
			cur.Answers[*qkey] = v
		}
	}
	return out, rows.Err()
}

// describeQuestions lists a set's answerable questions (sections carry no answer).
func describeQuestions(def *surveydef.Definition) []exportQuestion {
	out := []exportQuestion{}
	for _, q := range def.Questions {
		if q.Type == surveydef.TypeSection {
			continue
		}
		out = append(out, exportQuestion{Key: q.Key, Type: q.Type, Label: q.LabelFor("en"), Min: q.Min, Max: q.Max})
	}
	return out
}

// allowCORS lets an allow-listed platform page (server.cors_origins) read a
// token-gated response cross-origin. Reports whether the origin was allowed.
func (a *Admin) allowCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, o := range a.cfg.Server.CorsOrigins {
		if o == origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			return true
		}
	}
	return false
}

// exportJSON is the read API behind the CSV export: the responses of a date
// range as JSON, for another application that attaches surveys to the
// conversations they rate (the platform's Chat Dashboard). Like /sso it is
// gated by a platform appliance token (?t= or Authorization: Bearer) — whoever
// holds the deployment secret already has every survey — rather than a browser
// session, so a page on another origin can call it; the response is readable
// cross-origin only from server.cors_origins.
//
//	GET /api/export.json?t=<token>&from=YYYY-MM-DD&to=YYYY-MM-DD[&tz=UTC][&set=<id>|all][&incomplete=1]
//
// set=all returns every response in the range regardless of question set (a
// survey edited mid-range would otherwise hide the older responses); the
// questions array still describes the resolved (default/latest) set.
func (a *Admin) exportJSON(w http.ResponseWriter, r *http.Request) {
	a.allowCORS(w, r)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	tok := r.URL.Query().Get("t")
	if auth := r.Header.Get("Authorization"); auth != "" {
		tok = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	sec, _, err := parseAppliance(a.secret, tok)
	if err != nil {
		http.Error(w, "invalid or expired token", http.StatusForbidden)
		return
	}
	db, err := a.provider.DB(sec.Ref)
	if err != nil {
		http.Error(w, "invalid tenant", http.StatusBadRequest)
		return
	}
	from, to, info, _ := a.parseRange(r)
	def, defID, versions, err := a.resolveSet(db, r)
	if err != nil {
		log.Printf("admin: export.json resolve set: %v", err)
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	allSets := r.URL.Query().Get("set") == "all"
	list, err := collectResponses(db, from, to, defID, allSets, wantIncomplete(r))
	if err != nil {
		log.Printf("admin: export.json: %v", err)
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	questions := describeQuestions(def)
	if allSets {
		// Responses from other sets may name their rating or comment differently, so
		// describe those sets' questions too — the resolved set first, then the rest,
		// each key once. A consumer can then resolve the rating per response.
		seen := map[string]bool{}
		for _, q := range questions {
			seen[q.Key] = true
		}
		for _, v := range versions {
			if v.ID == defID {
				continue
			}
			d, derr := defstore.ByID(db, v.ID)
			if derr != nil {
				continue
			}
			for _, q := range describeQuestions(d) {
				if !seen[q.Key] {
					seen[q.Key] = true
					questions = append(questions, q)
				}
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{
		"range":     info,
		"set":       defID,
		"questions": questions,
		"responses": list,
	})
}
