package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/token"
)

// prefillField is one auto-detected prefill input for a survey (a question that
// carries a prefill_param).
type prefillField struct {
	Param string `json:"param"`
	Label string `json:"label"`
}

// setInfo is a survey the link generator can target, with its prefill fields.
type setInfo struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Prefills []prefillField `json:"prefills"`
}

type linksConfig struct {
	Base string    `json:"base"`
	Sets []setInfo `json:"sets"`
}

type linksView struct {
	base
	ConfigJSON string
	Sets       []defstore.Version
}

// publicBaseURL derives the scheme+host the public survey is served from, so
// generated links are absolute and copy/paste-ready. Honors a TLS-terminating
// reverse proxy via X-Forwarded-Proto.
func (a *Admin) publicBaseURL(r *http.Request) string {
	proto := "https"
	if r.TLS == nil && !a.secure && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		proto = "http"
	}
	return proto + "://" + r.Host
}

// linksPage renders the admin link generator, embedding each survey's
// auto-detected prefill fields so the form adapts to the chosen survey.
func (a *Admin) linksPage(w http.ResponseWriter, r *http.Request) {
	db := tenantDB(r.Context())
	versions, err := defstore.List(db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	cfg := linksConfig{Base: a.publicBaseURL(r)}
	for _, v := range versions {
		def, derr := defstore.ByID(db, v.ID)
		if derr != nil {
			continue
		}
		si := setInfo{ID: v.ID, Name: v.Name, Prefills: []prefillField{}}
		if si.Name == "" {
			si.Name = fmt.Sprintf("Set #%d", v.ID)
		}
		for _, q := range def.Questions {
			if q.PrefillParam != "" {
				si.Prefills = append(si.Prefills, prefillField{Param: q.PrefillParam, Label: q.LabelFor("en")})
			}
		}
		cfg.Sets = append(cfg.Sets, si)
	}
	js, err := json.Marshal(cfg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.render(w, http.StatusOK, "links.tmpl", linksView{
		base: a.newBase(r), ConfigJSON: string(js), Sets: versions,
	})
}

// linksGenerate mints a survey link per posted entry, server-side (the crypto
// secret never leaves the box). Minting has no side effects — a token is only
// consumed when a survey is submitted — so this is a pure read operation.
func (a *Admin) linksGenerate(w http.ResponseWriter, r *http.Request) {
	db := tenantDB(r.Context())
	ref := refFrom(r.Context())

	var payload struct {
		Set     int64  `json:"set"`
		Lang    string `json:"lang"`
		Entries []struct {
			Subject string            `json:"subject"`
			Params  map[string]string `json:"params"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(r.PostFormValue("payload")), &payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, derr := defstore.ByID(db, payload.Set); derr != nil {
		http.Error(w, "unknown survey", http.StatusBadRequest)
		return
	}
	lang := payload.Lang
	if lang == "" {
		lang = "en"
	}
	base := a.publicBaseURL(r)
	now := time.Now().Unix()

	type result struct {
		Subject string `json:"subject"`
		URL     string `json:"url,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	out := make([]result, 0, len(payload.Entries))
	seen := map[string]bool{}
	for _, e := range payload.Entries {
		subj := strings.TrimSpace(e.Subject)
		if subj == "" {
			out = append(out, result{Error: "missing subject"})
			continue
		}
		if seen[strings.ToLower(subj)] {
			out = append(out, result{Subject: subj, Error: "duplicate subject"})
			continue
		}
		seen[strings.ToLower(subj)] = true

		tok, terr := token.Encrypt(a.secret, subj, now, lang, ref)
		if terr != nil {
			out = append(out, result{Subject: subj, Error: terr.Error()})
			continue
		}
		q := url.Values{}
		q.Set("t", tok)
		q.Set("set", strconv.FormatInt(payload.Set, 10))
		for k, v := range e.Params {
			if v = strings.TrimSpace(v); v != "" {
				q.Set(k, v)
			}
		}
		out = append(out, result{Subject: subj, URL: base + "/s?" + q.Encode()})
	}
	writeJSON(w, map[string]any{"results": out})
}
