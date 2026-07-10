package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/token"
)

const testSecret = "integration-secret-32bytes-minimum-aaa"

func TestLinksGenerate(t *testing.T) {
	srv, _ := newServer(t)
	admin := loginAdmin(t, srv)

	// The Links page renders and carries a CSRF token.
	code, page := getBody(t, admin, srv.URL+"/links")
	if code != 200 || !strings.Contains(page, "Generate survey links") {
		t.Fatalf("links page: code=%d body=%s", code, first(page, 200))
	}
	csrf := csrfFrom(t, page)

	// Mint a link with a prefill param.
	payload := `{"set":1,"lang":"en","entries":[{"subject":"jane@example.com","params":{"name":"Jane Doe"}}]}`
	code, body := postForm(t, admin, srv.URL+"/api/links", url.Values{
		"csrf": {csrf}, "payload": {payload},
	})
	if code != 200 {
		t.Fatalf("generate: code=%d body=%s", code, first(body, 200))
	}

	var out struct {
		Results []struct{ Subject, URL, Error string }
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(out.Results) != 1 || out.Results[0].URL == "" {
		t.Fatalf("expected one link, got %s", body)
	}

	u, err := url.Parse(out.Results[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("set") != "1" {
		t.Fatalf("set param = %q, want 1", q.Get("set"))
	}
	if q.Get("name") != "Jane Doe" {
		t.Fatalf("name prefill = %q, want Jane Doe", q.Get("name"))
	}
	subj, _, lang, ref, derr := token.Decrypt(testSecret, q.Get("t"))
	if derr != nil || subj != "jane@example.com" || lang != "en" || ref != "" {
		t.Fatalf("token: subj=%q lang=%q ref=%q err=%v", subj, lang, ref, derr)
	}
}

func TestLinksGenerateRejectsBadCSRF(t *testing.T) {
	srv, _ := newServer(t)
	admin := loginAdmin(t, srv)
	code, _ := postForm(t, admin, srv.URL+"/api/links", url.Values{
		"csrf":    {"wrong-token"},
		"payload": {`{"set":1,"entries":[{"subject":"x"}]}`},
	})
	if code != http.StatusForbidden {
		t.Fatalf("bad CSRF should be 403, got %d", code)
	}
}
