package admin

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// Drafts (saved but never submitted) are real rows, so they must be excluded
// from the dashboard by default and only appear when explicitly included.
func TestAnalyticsExcludesDraftsByDefault(t *testing.T) {
	srv, database := newServer(t)
	seedResponses(t, database, 6) // complete
	seedDraft(t, database)        // saved, never submitted

	admin := loginAdmin(t, srv)
	today := time.Now().UTC().Format("2006-01-02")
	base := srv.URL + "/api/analytics?from=" + today + "&to=" + today + "&tz=UTC"

	code, body := getBody(t, admin, base)
	if code != 200 || !strings.Contains(body, `"responses":6`) {
		t.Fatalf("default should exclude the draft (want 6): code=%d body=%s", code, first(body, 200))
	}

	code, body = getBody(t, admin, base+"&incomplete=1")
	if code != 200 || !strings.Contains(body, `"responses":7`) {
		t.Fatalf("incomplete=1 should include the draft (want 7): code=%d body=%s", code, first(body, 200))
	}
}

func seedDraft(t *testing.T, database *sql.DB) {
	t.Helper()
	now := time.Now().Unix()
	res, err := database.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id, incomplete)
		 VALUES('+15559999999', ?, 'en', ?, 1, 1)`, now, now)
	if err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := database.Exec(
		`INSERT INTO answers(response_id, question_key, num) VALUES(?, 'csat', 3)`, id); err != nil {
		t.Fatalf("seed draft answer: %v", err)
	}
}
