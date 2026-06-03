package survey

import (
	"net/url"
	"strconv"
	"strings"
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
func parseAnswers(form url.Values, def *surveydef.Definition) (answers []answer, ok bool) {
	for _, q := range def.Questions {
		switch q.Type {
		case surveydef.TypeStars, surveydef.TypeScale, surveydef.TypeNPS:
			raw := strings.TrimSpace(form.Get(q.Key))
			if raw == "" {
				if q.Required {
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

		case surveydef.TypeChoice:
			raw := form.Get(q.Key)
			if raw == "" {
				if q.Required {
					return nil, false
				}
				continue
			}
			if !validOption(q, raw) {
				return nil, false
			}
			v := raw
			answers = append(answers, answer{key: q.Key, text: &v})

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
				if q.Required {
					return nil, false
				}
				continue
			}
			for _, v := range chosen {
				vv := v
				answers = append(answers, answer{key: q.Key, text: &vv})
			}

		case surveydef.TypeText:
			t := sanitizeText(form.Get(q.Key), q.MaxLen)
			if t == "" {
				if q.Required {
					return nil, false
				}
				continue
			}
			answers = append(answers, answer{key: q.Key, text: &t})
		}
	}
	return answers, true
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
