package admin

import (
	"database/sql"
	"time"

	"github.com/ronpinkas/csat/internal/surveydef"
)

// RangeInfo echoes the resolved query range.
type RangeInfo struct {
	From string `json:"from"`
	To   string `json:"to"`
	TZ   string `json:"tz"`
}

// Distribution is a histogram over integer buckets.
type Distribution struct {
	Labels []int `json:"labels"`
	Data   []int `json:"data"`
}

// Breakdown is a categorical count (choice/multichoice).
type Breakdown struct {
	Labels []string `json:"labels"`
	Data   []int    `json:"data"`
}

// NumTrend is a per-day average series for a numeric question.
type NumTrend struct {
	Labels []string  `json:"labels"`
	Avg    []float64 `json:"avg"`
}

// QStat holds the computed analytics for one question (only the fields relevant
// to its type are populated).
type QStat struct {
	Key          string        `json:"key"`
	Type         string        `json:"type"`
	Label        string        `json:"label"`
	Min          int           `json:"min,omitempty"`
	Max          int           `json:"max,omitempty"`
	Count        int           `json:"count"`
	Avg          *float64      `json:"avg,omitempty"`
	TopBoxPct    *float64      `json:"top_box_pct,omitempty"`
	NPS          *float64      `json:"nps,omitempty"`
	Distribution *Distribution `json:"distribution,omitempty"`
	Breakdown    *Breakdown    `json:"breakdown,omitempty"`
	Trend        *NumTrend     `json:"trend,omitempty"`
}

// ResponsesTrend is the overall per-day response count.
type ResponsesTrend struct {
	Labels    []string `json:"labels"`
	Responses []int    `json:"responses"`
}

// AnalyticsResult is the full dashboard payload.
type AnalyticsResult struct {
	Range     RangeInfo      `json:"range"`
	Responses int            `json:"responses"`
	Questions []QStat        `json:"questions"`
	Trend     ResponsesTrend `json:"trend"`
}

// computeAnalytics builds the full dashboard payload for the date range, scoped
// to a single question set (defID) so the question columns and the responses
// counted always belong to the same survey version.
func computeAnalytics(db *sql.DB, def *surveydef.Definition, defID, from, to int64, loc *time.Location, info RangeInfo) (AnalyticsResult, error) {
	out := AnalyticsResult{Range: info}

	if err := db.QueryRow(
		`SELECT COUNT(*) FROM responses WHERE submitted_at >= ? AND submitted_at < ? AND definition_id = ?`, from, to, defID,
	).Scan(&out.Responses); err != nil {
		return out, err
	}

	for _, q := range def.Questions {
		s := QStat{Key: q.Key, Type: q.Type, Label: q.LabelFor("en"), Min: q.Min, Max: q.Max}
		var err error
		switch q.Type {
		case surveydef.TypeStars, surveydef.TypeScale, surveydef.TypeNPS:
			err = fillNumeric(db, &s, q, defID, from, to, loc)
		case surveydef.TypeChoice, surveydef.TypeMultiChoice:
			err = fillBreakdown(db, &s, q, defID, from, to)
		case surveydef.TypeText:
			err = db.QueryRow(
				`SELECT COUNT(*) FROM answers a JOIN responses r ON a.response_id = r.id
				 WHERE a.question_key = ? AND a.text IS NOT NULL AND a.text <> '' AND r.submitted_at >= ? AND r.submitted_at < ? AND r.definition_id = ?`,
				q.Key, from, to, defID,
			).Scan(&s.Count)
		}
		if err != nil {
			return out, err
		}
		out.Questions = append(out.Questions, s)
	}

	trend, err := responsesTrend(db, defID, from, to, loc)
	if err != nil {
		return out, err
	}
	out.Trend = trend
	return out, nil
}

func fillNumeric(db *sql.DB, s *QStat, q surveydef.Question, defID, from, to int64, loc *time.Location) error {
	// distribution + count, then derive avg / top-box / nps in Go
	rows, err := db.Query(
		`SELECT a.num, COUNT(*) FROM answers a JOIN responses r ON a.response_id = r.id
		 WHERE a.question_key = ? AND a.num IS NOT NULL AND r.submitted_at >= ? AND r.submitted_at < ? AND r.definition_id = ?
		 GROUP BY a.num`, q.Key, from, to, defID)
	if err != nil {
		return err
	}
	counts := map[int]int{}
	for rows.Next() {
		var v, n int
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return err
		}
		counts[v] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	dist := &Distribution{}
	total, sum, topBox, promoters, detractors := 0, 0, 0, 0, 0
	for v := q.Min; v <= q.Max; v++ {
		dist.Labels = append(dist.Labels, v)
		dist.Data = append(dist.Data, counts[v])
		total += counts[v]
		sum += v * counts[v]
		if v >= q.Max-1 { // top-2-box
			topBox += counts[v]
		}
		if q.Type == surveydef.TypeNPS {
			if v >= 9 {
				promoters += counts[v]
			} else if v <= 6 {
				detractors += counts[v]
			}
		}
	}
	s.Count = total
	s.Distribution = dist
	if total > 0 {
		avg := round2(float64(sum) / float64(total))
		s.Avg = &avg
		if q.Type == surveydef.TypeStars {
			tb := round2(100 * float64(topBox) / float64(total))
			s.TopBoxPct = &tb
		}
		if q.Type == surveydef.TypeNPS {
			nps := round2(100 * float64(promoters-detractors) / float64(total))
			s.NPS = &nps
		}
	}

	trend, err := numericTrend(db, q.Key, defID, from, to, loc)
	if err != nil {
		return err
	}
	s.Trend = trend
	return nil
}

func fillBreakdown(db *sql.DB, s *QStat, q surveydef.Question, defID, from, to int64) error {
	rows, err := db.Query(
		`SELECT a.text, COUNT(*) FROM answers a JOIN responses r ON a.response_id = r.id
		 WHERE a.question_key = ? AND a.text IS NOT NULL AND r.submitted_at >= ? AND r.submitted_at < ? AND r.definition_id = ?
		 GROUP BY a.text`, q.Key, from, to, defID)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for rows.Next() {
		var v string
		var n int
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return err
		}
		counts[v] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	b := &Breakdown{}
	total := 0
	for _, o := range q.Options {
		b.Labels = append(b.Labels, o.LabelFor("en"))
		b.Data = append(b.Data, counts[o.Value])
		total += counts[o.Value]
	}
	s.Breakdown = b
	s.Count = total
	return nil
}

type dayAvg struct {
	n   int
	sum int
}

func numericTrend(db *sql.DB, key string, defID, from, to int64, loc *time.Location) (*NumTrend, error) {
	rows, err := db.Query(
		`SELECT r.submitted_at, a.num FROM answers a JOIN responses r ON a.response_id = r.id
		 WHERE a.question_key = ? AND a.num IS NOT NULL AND r.submitted_at >= ? AND r.submitted_at < ? AND r.definition_id = ?`,
		key, from, to, defID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agg := map[string]*dayAvg{}
	for rows.Next() {
		var ts int64
		var num int
		if err := rows.Scan(&ts, &num); err != nil {
			return nil, err
		}
		day := time.Unix(ts, 0).In(loc).Format("2006-01-02")
		a := agg[day]
		if a == nil {
			a = &dayAvg{}
			agg[day] = a
		}
		a.n++
		a.sum += num
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	t := &NumTrend{}
	for _, day := range daysInRange(from, to, loc) {
		t.Labels = append(t.Labels, day)
		if a := agg[day]; a != nil && a.n > 0 {
			t.Avg = append(t.Avg, round2(float64(a.sum)/float64(a.n)))
		} else {
			t.Avg = append(t.Avg, 0)
		}
	}
	return t, nil
}

func responsesTrend(db *sql.DB, defID, from, to int64, loc *time.Location) (ResponsesTrend, error) {
	rows, err := db.Query(
		`SELECT submitted_at FROM responses WHERE submitted_at >= ? AND submitted_at < ? AND definition_id = ?`, from, to, defID)
	if err != nil {
		return ResponsesTrend{}, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return ResponsesTrend{}, err
		}
		counts[time.Unix(ts, 0).In(loc).Format("2006-01-02")]++
	}
	if err := rows.Err(); err != nil {
		return ResponsesTrend{}, err
	}
	var t ResponsesTrend
	for _, day := range daysInRange(from, to, loc) {
		t.Labels = append(t.Labels, day)
		t.Responses = append(t.Responses, counts[day])
	}
	return t, nil
}

// daysInRange lists local YYYY-MM-DD dates covered by [from, to).
func daysInRange(from, to int64, loc *time.Location) []string {
	start := time.Unix(from, 0).In(loc)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	end := time.Unix(to, 0).In(loc)
	var out []string
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
		if len(out) > 366 {
			break
		}
	}
	return out
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
