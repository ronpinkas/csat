package survey

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

// submission holds the validated survey answers.
type submission struct {
	csat       int
	resolution string
	ces        int
	comment    string
}

var errInvalidField = errors.New("invalid submission")

// parseSubmission validates and bounds-checks the posted form values.
func parseSubmission(csatStr, resolution, cesStr, comment string, csatMax, cesMax, commentMax int) (submission, error) {
	var s submission

	csat, err := strconv.Atoi(strings.TrimSpace(csatStr))
	if err != nil || csat < 1 || csat > csatMax {
		return s, errInvalidField
	}
	s.csat = csat

	switch resolution {
	case "yes", "partial", "no":
		s.resolution = resolution
	default:
		return s, errInvalidField
	}

	ces, err := strconv.Atoi(strings.TrimSpace(cesStr))
	if err != nil || ces < 1 || ces > cesMax {
		return s, errInvalidField
	}
	s.ces = ces

	s.comment = sanitizeComment(comment, commentMax)
	return s, nil
}

// sanitizeComment strips control characters and clamps length.
func sanitizeComment(in string, max int) string {
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
