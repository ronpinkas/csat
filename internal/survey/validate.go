package survey

import (
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ronpinkas/csat/internal/surveydef"
)

// answer is one validated answer to persist. Numeric questions set num; choice
// and text set text. A multichoice question yields several answers.
type answer struct {
	key  string
	num  *int
	text *string
}

// parseAnswers validates the posted form against the survey definition and
// returns the answers to store. ok is false on any missing-required or
// out-of-range value.
//
// requireComplete distinguishes the two ways answers arrive:
//   - true  (submit): every visible required question must be answered.
//   - false (save):   a partial form is fine — required questions may be blank.
//
// Either way the values that ARE present are fully validated (valid options,
// numbers in range, well-formed dates), so a draft can never carry junk.
//
// Conditional questions (ShowIf) are evaluated against the answers seen so far,
// which is sound because a ShowIf may only reference an earlier question (the
// definition validator enforces that ordering). When a question's condition is
// not met it is skipped entirely: no required-enforcement, and any posted value
// for it is dropped so stale/hidden answers never reach the database.
func parseAnswers(form url.Values, def *surveydef.Definition, requireComplete bool) (answers []answer, ok bool) {
	// missing reports whether a blank required answer should reject the form.
	missing := func(required bool) bool { return required && requireComplete }
	given := map[string][]string{} // question key -> answered value(s), for ShowIf
	for _, q := range def.Questions {
		if q.Type == surveydef.TypeSection {
			continue // display-only, no answer
		}
		if !showIfMet(q.ShowIf, given) {
			continue // gated off: skip required-enforcement and drop any value
		}
		switch q.Type {
		case surveydef.TypeStars, surveydef.TypeScale, surveydef.TypeNPS:
			raw := strings.TrimSpace(form.Get(q.Key))
			if raw == "" {
				if missing(q.Required) {
					return nil, false
				}
				continue
			}
			n, err := strconv.Atoi(raw)
			if err != nil || n < q.Min || n > q.Max {
				return nil, false
			}
			v := n
			answers = append(answers, answer{key: q.Key, num: &v})
			given[q.Key] = append(given[q.Key], raw)

		case surveydef.TypeChoice:
			raw := form.Get(q.Key)
			if raw == "" {
				if missing(q.Required) {
					return nil, false
				}
				continue
			}
			if !validOption(q, raw) {
				return nil, false
			}
			v := raw
			answers = append(answers, answer{key: q.Key, text: &v})
			given[q.Key] = append(given[q.Key], raw)

		case surveydef.TypeMultiChoice:
			var chosen []string
			for _, v := range form[q.Key] {
				if v == "" {
					continue
				}
				if !validOption(q, v) {
					return nil, false
				}
				chosen = append(chosen, v)
			}
			if len(chosen) == 0 {
				if missing(q.Required) {
					return nil, false
				}
				continue
			}
			for _, v := range chosen {
				vv := v
				answers = append(answers, answer{key: q.Key, text: &vv})
			}
			given[q.Key] = append(given[q.Key], chosen...)

		case surveydef.TypeNumber, surveydef.TypeDate:
			raw := strings.TrimSpace(form.Get(q.Key))
			if raw == "" {
				if missing(q.Required) {
					return nil, false
				}
				continue
			}
			if !q.AcceptsValue(raw) {
				return nil, false
			}
			if q.Type == surveydef.TypeDate {
				if _, err := time.Parse("2006-01-02", raw); err != nil {
					return nil, false
				}
			}
			v := raw
			answers = append(answers, answer{key: q.Key, text: &v})
			given[q.Key] = append(given[q.Key], raw)

		case surveydef.TypeText:
			t := sanitizeText(form.Get(q.Key), q.MaxLen)
			if t == "" {
				if missing(q.Required) {
					return nil, false
				}
				continue
			}
			answers = append(answers, answer{key: q.Key, text: &t})
			given[q.Key] = append(given[q.Key], t)
		}
	}
	return answers, true
}

// showIfMet reports whether a ShowIf gate is satisfied by the answers collected
// so far. A nil gate is always met.
func showIfMet(si *surveydef.ShowIf, given map[string][]string) bool {
	if si == nil {
		return true
	}
	for _, have := range given[si.Key] {
		for _, want := range si.In {
			if have == want {
				return true
			}
		}
	}
	return false
}

func validOption(q surveydef.Question, val string) bool {
	for _, o := range q.Options {
		if o.Value == val {
			return true
		}
	}
	return false
}

// sanitizeText strips control characters and clamps length.
func sanitizeText(in string, max int) string {
	in = strings.TrimSpace(in)
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, in)
	if max > 0 && len(cleaned) > max {
		cleaned = cleaned[:max]
	}
	return cleaned
}
