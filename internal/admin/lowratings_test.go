package admin

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

type lowRatingsResp struct {
	Page     int    `json:"page"`
	Total    int    `json:"total"`
	Rating   int    `json:"rating"`
	Question string `json:"question"`
	Rows     []struct {
		Subject  string `json:"subject"`
		Rating   int    `json:"rating"`
		Comments []struct {
			Question string `json:"question"`
			Text     string `json:"text"`
		} `json:"comments"`
		Answers []struct {
			Question string `json:"question"`
			Value    string `json:"value"`
		} `json:"answers"`
	} `json:"rows"`
}

// The lowest-rating section is response-centric: every 1-star response shows up,
// including the ones that left no written comment, so the list length matches
// the "1" bar in the distribution chart.
func TestLowRatingsListsEveryOneStarResponse(t *testing.T) {
	srv, database := newServer(t)
	insertResponse(t, database, "+15550000001", 1, "no", 2, "terrible hold time")
	insertResponse(t, database, "+15550000002", 1, "no", 1, "") // 1 star, no comment
	insertResponse(t, database, "+15550000003", 5, "yes", 7, "great")
	insertResponse(t, database, "+15550000004", 2, "partial", 3, "meh")

	admin := loginAdmin(t, srv)
	today := time.Now().UTC().Format("2006-01-02")
	code, body := getBody(t, admin, srv.URL+"/api/lowratings?from="+today+"&to="+today+"&tz=UTC")
	if code != 200 {
		t.Fatalf("lowratings: code=%d body=%s", code, first(body, 200))
	}
	var got lowRatingsResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, first(body, 200))
	}
	if got.Rating != 1 {
		t.Fatalf("rating: want 1, got %d", got.Rating)
	}
	if got.Question == "" {
		t.Fatal("question label should name the CSAT question")
	}
	if got.Total != 2 || len(got.Rows) != 2 {
		t.Fatalf("want 2 one-star responses, got total=%d rows=%d", got.Total, len(got.Rows))
	}

	bySubject := map[string]int{}
	for _, r := range got.Rows {
		bySubject[r.Subject] = len(r.Comments)
		if r.Rating != 1 {
			t.Fatalf("row rating: want 1, got %d", r.Rating)
		}
		if r.Subject == "+15550000001" {
			if len(r.Comments) != 1 || r.Comments[0].Text != "terrible hold time" {
				t.Fatalf("wrong comment attached: %+v", r.Comments)
			}
			if r.Comments[0].Question == "" {
				t.Fatal("comment should carry its question label")
			}
		}
	}

	// Context: the response's other answers, in survey order, with choice
	// values resolved to their labels and numerics shown against their max.
	// The rating question itself is omitted — the header already states it.
	for _, r := range got.Rows {
		if r.Subject != "+15550000001" {
			continue
		}
		if len(r.Answers) != 2 {
			t.Fatalf("want resolution + ces as context, got %+v", r.Answers)
		}
		if r.Answers[0].Value != "No" {
			t.Fatalf("choice should render its option label, got %q", r.Answers[0].Value)
		}
		if r.Answers[1].Value != "2 / 7" {
			t.Fatalf("scale should render against its max, got %q", r.Answers[1].Value)
		}
		for _, a := range r.Answers {
			if a.Question == "" {
				t.Fatal("context answer should carry its question label")
			}
			if a.Value == "terrible hold time" {
				t.Fatal("text answers belong in comments, not context chips")
			}
		}
	}
	if n, ok := bySubject["+15550000001"]; !ok || n != 1 {
		t.Fatalf("commented 1-star response should carry its comment, got %d (%v)", n, bySubject)
	}
	if n, ok := bySubject["+15550000002"]; !ok || n != 0 {
		t.Fatalf("silent 1-star response should be listed with no comments, got %d (%v)", n, bySubject)
	}
}

// A ?rating=N inside the question's range is honored; anything else falls back
// to the floor, so a hand-edited URL can't produce a nonsense view.
func TestLowRatingsRatingParam(t *testing.T) {
	srv, database := newServer(t)
	insertResponse(t, database, "+15550000001", 1, "no", 2, "bad")
	insertResponse(t, database, "+15550000002", 3, "partial", 4, "ok")
	insertResponse(t, database, "+15550000003", 3, "partial", 4, "")

	admin := loginAdmin(t, srv)
	today := time.Now().UTC().Format("2006-01-02")
	base := srv.URL + "/api/lowratings?from=" + today + "&to=" + today + "&tz=UTC"

	var got lowRatingsResp
	_, body := getBody(t, admin, base+"&rating=3")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Rating != 3 || got.Total != 2 {
		t.Fatalf("rating=3: want rating 3 total 2, got %d/%d", got.Rating, got.Total)
	}

	_, body = getBody(t, admin, base+"&rating=99")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Rating != 1 || got.Total != 1 {
		t.Fatalf("out-of-range rating should fall back to 1: got %d/%d", got.Rating, got.Total)
	}
}

// Drafts are excluded by default here too, matching every other dashboard query.
func TestLowRatingsExcludesDraftsByDefault(t *testing.T) {
	srv, database := newServer(t)
	insertResponse(t, database, "+15550000001", 1, "no", 2, "bad")
	seedLowDraft(t, database)

	admin := loginAdmin(t, srv)
	today := time.Now().UTC().Format("2006-01-02")
	base := srv.URL + "/api/lowratings?from=" + today + "&to=" + today + "&tz=UTC"

	var got lowRatingsResp
	_, body := getBody(t, admin, base)
	_ = json.Unmarshal([]byte(body), &got)
	if got.Total != 1 {
		t.Fatalf("default should exclude the 1-star draft (want 1), got %d", got.Total)
	}

	_, body = getBody(t, admin, base+"&incomplete=1")
	_ = json.Unmarshal([]byte(body), &got)
	if got.Total != 2 {
		t.Fatalf("incomplete=1 should include the 1-star draft (want 2), got %d", got.Total)
	}
}

func seedLowDraft(t *testing.T, database *sql.DB) {
	t.Helper()
	now := time.Now().Unix()
	res, err := database.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id, incomplete)
		 VALUES('+15558888888', ?, 'en', ?, 1, 1)`, now, now)
	if err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := database.Exec(
		`INSERT INTO answers(response_id, question_key, num) VALUES(?, 'csat', 1)`, id); err != nil {
		t.Fatalf("seed draft answer: %v", err)
	}
}
