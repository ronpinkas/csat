// Package surveydef defines the configurable survey schema (loaded from a
// survey.json file, or the embedded default CSAT instrument) shared by the
// public form renderer and the admin analytics.
package surveydef

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// Question types.
const (
	TypeStars       = "stars"       // integer 1..Max, rendered as stars
	TypeScale       = "scale"       // integer Min..Max, numbered buttons + end labels
	TypeNPS         = "nps"         // integer 0..10, Net Promoter Score
	TypeChoice      = "choice"      // single choice from Options
	TypeMultiChoice = "multichoice" // multiple choices from Options
	TypeText        = "text"        // free text
	TypeNumber      = "number"      // numeric free entry (stored as text)
	TypeDate        = "date"        // ISO (YYYY-MM-DD) date (stored as text)
	TypeSection     = "section"     // display-only heading, carries no answer
)

var (
	numberRE = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	dateRE   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

//go:embed default.json
var defaultJSON []byte

// Definition is a complete survey.
type Definition struct {
	Version int               `json:"version"`
	Name    string            `json:"name"`   // human label for the multi-survey picker (optional)
	Intro   map[string]string `json:"intro"`  // lang -> text
	Thanks  map[string]string `json:"thanks"` // lang -> text

	// AllowSave lets a respondent save progress before finishing. The draft is
	// stored server-side against the link, so it resumes on any device and takes
	// priority over the browser's local copy.
	AllowSave bool `json:"allow_save,omitempty"`
	// ForceConfirm makes the save/submit action ask for an explicit choice
	// instead of acting immediately. Only meaningful when AllowSave is set;
	// normalization clears it otherwise.
	ForceConfirm bool `json:"force_confirm,omitempty"`

	Questions []Question `json:"questions"`
}

// Question is one survey item.
type Question struct {
	Key         string            `json:"key"`
	Type        string            `json:"type"`
	Label       map[string]string `json:"label"`
	Required    bool              `json:"required"`
	Min         int               `json:"min"`         // scale
	Max         int               `json:"max"`         // stars/scale
	MaxLen      int               `json:"maxlen"`      // text
	Placeholder map[string]string `json:"placeholder"` // text
	Options     []Option          `json:"options"`     // choice/multichoice
	Ends        *Ends             `json:"ends"`        // scale/nps end labels

	Widget       string  `json:"widget,omitempty"`        // choice: "select" renders a dropdown (default: radios)
	PrefillParam string  `json:"prefill_param,omitempty"` // URL query-param name feeding the default value
	Default      string  `json:"default,omitempty"`       // static default value (used when no prefill param)
	Locked       bool    `json:"locked,omitempty"`        // when prefilled, render read-only (value still submits)
	ShowIf       *ShowIf `json:"show_if,omitempty"`       // conditional display gate
}

// ShowIf gates a question's display on an earlier question's answer: the
// question is shown only when the answer to Key is one of In. The referenced
// key MUST appear before this question so the server can evaluate it inline.
type ShowIf struct {
	Key string   `json:"key"`
	In  []string `json:"in"`
}

// Option is a choice value with localized labels.
type Option struct {
	Value string            `json:"value"`
	Label map[string]string `json:"label"`
}

// Ends holds the low/high anchor labels of a scale.
type Ends struct {
	Low  map[string]string `json:"low"`
	High map[string]string `json:"high"`
}

var keyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// Default returns the embedded CSAT instrument.
func Default() *Definition {
	d, err := parse(defaultJSON)
	if err != nil {
		panic("surveydef: invalid embedded default: " + err.Error())
	}
	return d
}

// Load reads a survey.json from path, or returns the embedded default when path
// is empty.
func Load(path string) (*Definition, error) {
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read survey definition %s: %w", path, err)
	}
	d, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("survey definition %s: %w", path, err)
	}
	return d, nil
}

// Parse decodes and validates a survey definition from JSON bytes (used by the
// admin editor and the DB-backed definition store).
func Parse(data []byte) (*Definition, error) {
	return parse(data)
}

// JSON returns the definition as indented JSON, for storage and the editor.
func (d *Definition) JSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

func parse(data []byte) (*Definition, error) {
	var d Definition
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if err := d.normalizeAndValidate(); err != nil {
		return nil, err
	}
	return &d, nil
}

func (d *Definition) normalizeAndValidate() error {
	if d.Version < 1 {
		d.Version = 1
	}
	if len(d.Questions) == 0 {
		return errors.New("survey has no questions")
	}
	// Force-confirm is a modifier on saving; it means nothing on its own.
	if !d.AllowSave {
		d.ForceConfirm = false
	}
	seen := map[string]bool{}
	byKey := make(map[string]*Question, len(d.Questions))
	for i := range d.Questions {
		byKey[d.Questions[i].Key] = &d.Questions[i]
	}
	for i := range d.Questions {
		q := &d.Questions[i]
		if !keyRE.MatchString(q.Key) {
			return fmt.Errorf("question %d: invalid key %q (want [a-z][a-z0-9_]*)", i, q.Key)
		}
		if seen[q.Key] {
			return fmt.Errorf("duplicate question key %q", q.Key)
		}
		seen[q.Key] = true
		if len(q.Label) == 0 {
			return fmt.Errorf("question %q: missing label", q.Key)
		}
		switch q.Type {
		case TypeStars:
			if q.Max == 0 {
				q.Max = 5
			}
			if q.Max < 2 {
				return fmt.Errorf("question %q: stars max must be >= 2", q.Key)
			}
			q.Min = 1
		case TypeScale:
			if q.Max == 0 {
				q.Max = 5
			}
			if q.Max <= q.Min {
				return fmt.Errorf("question %q: scale max must be > min", q.Key)
			}
		case TypeNPS:
			q.Min, q.Max = 0, 10
		case TypeChoice, TypeMultiChoice:
			if len(q.Options) == 0 {
				return fmt.Errorf("question %q: %s needs options", q.Key, q.Type)
			}
			ov := map[string]bool{}
			for _, o := range q.Options {
				if o.Value == "" {
					return fmt.Errorf("question %q: option with empty value", q.Key)
				}
				if ov[o.Value] {
					return fmt.Errorf("question %q: duplicate option value %q", q.Key, o.Value)
				}
				ov[o.Value] = true
			}
		case TypeText:
			if q.MaxLen <= 0 {
				q.MaxLen = 2000
			}
		case TypeNumber:
			if q.Max != 0 && q.Max < q.Min {
				return fmt.Errorf("question %q: number max must be >= min", q.Key)
			}
		case TypeDate:
			// no extra configuration
		case TypeSection:
			// display-only heading; carries no answer
		default:
			return fmt.Errorf("question %q: unknown type %q", q.Key, q.Type)
		}

		// Cross-cutting field validation (widget / default / show_if).
		if q.Widget != "" && q.Widget != "radio" && q.Widget != "select" {
			return fmt.Errorf("question %q: invalid widget %q", q.Key, q.Widget)
		}
		if q.Widget == "select" && q.Type != TypeChoice {
			return fmt.Errorf("question %q: widget %q is only valid for choice", q.Key, q.Widget)
		}
		if q.Default != "" && !q.AcceptsValue(q.Default) {
			return fmt.Errorf("question %q: default %q is not valid for this question", q.Key, q.Default)
		}
		if q.ShowIf != nil {
			si := q.ShowIf
			if si.Key == q.Key {
				return fmt.Errorf("question %q: show_if cannot reference itself", q.Key)
			}
			ctrl, ok := byKey[si.Key]
			if !ok {
				return fmt.Errorf("question %q: show_if references unknown key %q", q.Key, si.Key)
			}
			if !seen[si.Key] {
				return fmt.Errorf("question %q: show_if key %q must appear before it", q.Key, si.Key)
			}
			if len(si.In) == 0 {
				return fmt.Errorf("question %q: show_if needs at least one value", q.Key)
			}
			if ctrl.Type == TypeChoice || ctrl.Type == TypeMultiChoice {
				for _, v := range si.In {
					if !optionExists(ctrl, v) {
						return fmt.Errorf("question %q: show_if value %q is not an option of %q", q.Key, v, si.Key)
					}
				}
			}
		}
	}
	return nil
}

// optionExists reports whether val is one of q's option values.
func optionExists(q *Question, val string) bool {
	for _, o := range q.Options {
		if o.Value == val {
			return true
		}
	}
	return false
}

// AcceptsValue reports whether v is a valid answer/default for this question's
// type (used to validate static defaults and runtime URL prefills). Text always
// accepts; section/unknown never do.
func (q Question) AcceptsValue(v string) bool {
	switch q.Type {
	case TypeStars, TypeScale, TypeNPS:
		n, err := strconv.Atoi(v)
		return err == nil && n >= q.Min && n <= q.Max
	case TypeChoice, TypeMultiChoice:
		return optionExists(&q, v)
	case TypeNumber:
		if !numberRE.MatchString(v) {
			return false
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return false
		}
		if q.Min != 0 && f < float64(q.Min) {
			return false
		}
		if q.Max != 0 && f > float64(q.Max) {
			return false
		}
		return true
	case TypeDate:
		return dateRE.MatchString(v)
	case TypeText:
		return true
	}
	return false
}

// ---- helpers ----

// Numeric reports whether the question stores a numeric answer.
func (q Question) Numeric() bool {
	return q.Type == TypeStars || q.Type == TypeScale || q.Type == TypeNPS
}

// Range returns the inclusive numeric bounds for a numeric question.
func (q Question) Range() (lo, hi int) { return q.Min, q.Max }

// Scale returns the list of integer values lo..hi (for rendering numeric widgets).
func (q Question) Scale() []int {
	out := make([]int, 0, q.Max-q.Min+1)
	for i := q.Min; i <= q.Max; i++ {
		out = append(out, i)
	}
	return out
}

// LabelFor returns the localized question label.
func (q Question) LabelFor(lang string) string { return loc(q.Label, lang) }

// PlaceholderFor returns the localized text placeholder.
func (q Question) PlaceholderFor(lang string) string { return loc(q.Placeholder, lang) }

// LabelFor returns the localized option label.
func (o Option) LabelFor(lang string) string { return loc(o.Label, lang) }

// IntroFor / ThanksFor return localized survey copy.
func (d *Definition) IntroFor(lang string) string  { return loc(d.Intro, lang) }
func (d *Definition) ThanksFor(lang string) string { return loc(d.Thanks, lang) }

// EndLow / EndHigh return localized scale anchors ("" if none).
func (q Question) EndLow(lang string) string {
	if q.Ends == nil {
		return ""
	}
	return loc(q.Ends.Low, lang)
}
func (q Question) EndHigh(lang string) string {
	if q.Ends == nil {
		return ""
	}
	return loc(q.Ends.High, lang)
}

// loc resolves a localized string, falling back to English then any value.
func loc(m map[string]string, lang string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[lang]; ok && v != "" {
		return v
	}
	if v, ok := m["en"]; ok && v != "" {
		return v
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}
