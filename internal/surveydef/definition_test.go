package surveydef

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	d := Default()
	if len(d.Questions) != 4 {
		t.Fatalf("default: expected 4 questions, got %d", len(d.Questions))
	}
	if d.Questions[0].Key != "csat" || d.Questions[0].Type != TypeStars || d.Questions[0].Max != 5 {
		t.Fatalf("default csat question wrong: %+v", d.Questions[0])
	}
	if d.IntroFor("es") == "" || d.IntroFor("es") == d.IntroFor("en") {
		t.Fatal("expected distinct Spanish intro")
	}
	// fallback to en for an unknown language
	if d.IntroFor("fr") != d.IntroFor("en") {
		t.Fatal("unknown language should fall back to en")
	}
}

func TestNormalizeDefaults(t *testing.T) {
	d, err := parse([]byte(`{"questions":[
		{"key":"a","type":"stars","label":{"en":"A"}},
		{"key":"b","type":"nps","label":{"en":"B"}},
		{"key":"c","type":"text","label":{"en":"C"}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Questions[0].Max != 5 || d.Questions[0].Min != 1 {
		t.Fatalf("stars defaults: %+v", d.Questions[0])
	}
	if d.Questions[1].Min != 0 || d.Questions[1].Max != 10 {
		t.Fatalf("nps forced 0..10: %+v", d.Questions[1])
	}
	if d.Questions[2].MaxLen != 2000 {
		t.Fatalf("text default maxlen: %+v", d.Questions[2])
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []string{
		`{"questions":[]}`, // no questions
		`{"questions":[{"key":"Bad Key","type":"text","label":{"en":"x"}}]}`,                                        // bad key
		`{"questions":[{"key":"a","type":"bogus","label":{"en":"x"}}]}`,                                             // bad type
		`{"questions":[{"key":"a","type":"choice","label":{"en":"x"}}]}`,                                            // choice w/o options
		`{"questions":[{"key":"a","type":"text","label":{}}]}`,                                                      // missing label
		`{"questions":[{"key":"a","type":"text","label":{"en":"x"}},{"key":"a","type":"text","label":{"en":"y"}}]}`, // dup key
	}
	for i, c := range cases {
		if _, err := parse([]byte(c)); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "survey.json")
	if err := os.WriteFile(p, []byte(`{"questions":[{"key":"q1","type":"scale","min":0,"max":10,"label":{"en":"How likely?"},"ends":{"low":{"en":"No"},"high":{"en":"Yes"}}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	q := d.Questions[0]
	if q.Type != TypeScale || q.EndHigh("en") != "Yes" {
		t.Fatalf("loaded question wrong: %+v", q)
	}
	if got := q.Scale(); len(got) != 11 || got[0] != 0 || got[10] != 10 {
		t.Fatalf("scale 0..10 wrong: %v", got)
	}
	// empty path -> embedded default
	def, err := Load("")
	if err != nil || def.Questions[0].Key != "csat" {
		t.Fatalf("empty path should load default, got %v err=%v", def, err)
	}
}
