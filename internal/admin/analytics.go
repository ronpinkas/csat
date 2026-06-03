package admin

import (
	"database/sql"
	"time"
)

// KPIs are the headline metrics for a date range.
type KPIs struct {
	Responses      int     `json:"responses"`
	CSATAvg        float64 `json:"csat_avg"`
	CSATPct        float64 `json:"csat_pct"` // top-2-box %
	CESAvg         float64 `json:"ces_avg"`
	ResolutionRate float64 `json:"resolution_rate"`
}

// Distribution is a histogram over integer buckets.
type Distribution struct {
	Labels []int `json:"labels"`
	Data   []int `json:"data"`
}

// Breakdown is a categorical count (resolution).
type Breakdown struct {
	Labels []string `json:"labels"`
	Data   []int    `json:"data"`
}

// Trend is the per-day series.
type Trend struct {
	Labels         []string  `json:"labels"`
	Responses      []int     `json:"responses"`
	CSATAvg        []float64 `json:"csat_avg"`
	CESAvg         []float64 `json:"ces_avg"`
	CSATPct        []float64 `json:"csat_pct"`
	ResolutionRate []float64 `json:"resolution_rate"`
}

// RangeInfo echoes the resolved query range.
type RangeInfo struct {
	From string `json:"from"`
	To   string `json:"to"`
	TZ   string `json:"tz"`
}

// AnalyticsResult is the full dashboard payload.
type AnalyticsResult struct {
	Range            RangeInfo    `json:"range"`
	KPIs             KPIs         `json:"kpis"`
	CSATDistribution Distribution `json:"csat_distribution"`
	CESDistribution  Distribution `json:"ces_distribution"`
	Resolution       Breakdown    `json:"resolution"`
	Trend            Trend        `json:"trend"`
}

func queryKPIs(db *sql.DB, from, to int64) (KPIs, error) {
	var k KPIs
	err := db.QueryRow(
		`SELECT
		   COUNT(*),
		   COALESCE(AVG(csat), 0),
		   COALESCE(AVG(CASE WHEN csat >= 4 THEN 1.0 ELSE 0 END), 0),
		   COALESCE(AVG(ces), 0),
		   COALESCE(AVG(CASE WHEN resolution = 'yes' THEN 1.0 ELSE 0 END), 0)
		 FROM responses WHERE submitted_at >= ? AND submitted_at < ?`,
		from, to,
	).Scan(&k.Responses, &k.CSATAvg, &k.CSATPct, &k.CESAvg, &k.ResolutionRate)
	if err != nil {
		return k, err
	}
	k.CSATPct *= 100
	return k, nil
}

// queryDistribution returns counts per integer bucket lo..hi for the given
// column. col is a trusted constant ("csat" or "ces"), never user input.
func queryDistribution(db *sql.DB, from, to int64, col string, lo, hi int) (Distribution, error) {
	rows, err := db.Query(
		`SELECT `+col+`, COUNT(*) FROM responses
		 WHERE submitted_at >= ? AND submitted_at < ? GROUP BY `+col, from, to)
	if err != nil {
		return Distribution{}, err
	}
	defer rows.Close()

	counts := map[int]int{}
	for rows.Next() {
		var bucket, n int
		if err := rows.Scan(&bucket, &n); err != nil {
			return Distribution{}, err
		}
		counts[bucket] = n
	}
	d := Distribution{}
	for v := lo; v <= hi; v++ {
		d.Labels = append(d.Labels, v)
		d.Data = append(d.Data, counts[v])
	}
	return d, rows.Err()
}

func queryResolution(db *sql.DB, from, to int64) (Breakdown, error) {
	rows, err := db.Query(
		`SELECT resolution, COUNT(*) FROM responses
		 WHERE submitted_at >= ? AND submitted_at < ? GROUP BY resolution`, from, to)
	if err != nil {
		return Breakdown{}, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return Breakdown{}, err
		}
		counts[k] = n
	}
	order := []string{"yes", "partial", "no"}
	b := Breakdown{Labels: []string{"Resolved", "Partial", "Not resolved"}}
	for _, k := range order {
		b.Data = append(b.Data, counts[k])
	}
	return b, rows.Err()
}

type dayAgg struct {
	n       int
	csatSum int
	cesSum  int
	yes     int
	top2    int
}

// queryTrend buckets responses by local calendar day (in loc), filling empty
// days with zeros. SQLite can't bucket by an IANA zone, so we do it in Go.
func queryTrend(db *sql.DB, from, to int64, loc *time.Location) (Trend, error) {
	rows, err := db.Query(
		`SELECT submitted_at, csat, ces, resolution FROM responses
		 WHERE submitted_at >= ? AND submitted_at < ?`, from, to)
	if err != nil {
		return Trend{}, err
	}
	defer rows.Close()

	aggs := map[string]*dayAgg{}
	for rows.Next() {
		var ts int64
		var csat, ces int
		var res string
		if err := rows.Scan(&ts, &csat, &ces, &res); err != nil {
			return Trend{}, err
		}
		key := time.Unix(ts, 0).In(loc).Format("2006-01-02")
		a := aggs[key]
		if a == nil {
			a = &dayAgg{}
			aggs[key] = a
		}
		a.n++
		a.csatSum += csat
		a.cesSum += ces
		if res == "yes" {
			a.yes++
		}
		if csat >= 4 {
			a.top2++
		}
	}
	if err := rows.Err(); err != nil {
		return Trend{}, err
	}

	var t Trend
	for _, day := range daysInRange(from, to, loc) {
		t.Labels = append(t.Labels, day)
		a := aggs[day]
		if a == nil || a.n == 0 {
			t.Responses = append(t.Responses, 0)
			t.CSATAvg = append(t.CSATAvg, 0)
			t.CESAvg = append(t.CESAvg, 0)
			t.CSATPct = append(t.CSATPct, 0)
			t.ResolutionRate = append(t.ResolutionRate, 0)
			continue
		}
		t.Responses = append(t.Responses, a.n)
		t.CSATAvg = append(t.CSATAvg, round2(float64(a.csatSum)/float64(a.n)))
		t.CESAvg = append(t.CESAvg, round2(float64(a.cesSum)/float64(a.n)))
		t.CSATPct = append(t.CSATPct, round2(100*float64(a.top2)/float64(a.n)))
		t.ResolutionRate = append(t.ResolutionRate, round2(float64(a.yes)/float64(a.n)))
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
		if len(out) > 366 { // safety bound
			break
		}
	}
	return out
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
