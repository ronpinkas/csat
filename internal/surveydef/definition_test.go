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
		`{"questions":[{"key":"a","type":"text","label":{"en":"x"}},{"key":"a","type":"text","label":{"en":"y"}}]}`,                       // dup key
		`{"questions":[{"key":"a","type":"text","label":{"en":"x"},"show_if":{"key":"b","in":["y"]}},{"key":"b","type":"text","label":{"en":"y"}}]}`, // show_if forward reference
		`{"questions":[{"key":"a","type":"text","label":{"en":"x"},"show_if":{"key":"missing","in":["y"]}}]}`,                              // show_if unknown key
		`{"questions":[{"key":"a","type":"stars","label":{"en":"x"},"widget":"select"}]}`,                                                 // widget on non-choice
		`{"questions":[{"key":"a","type":"number","min":5,"max":2,"label":{"en":"x"}}]}`,                                                  // number max<min
		`{"questions":[{"key":"a","type":"choice","label":{"en":"x"},"options":[{"value":"yes","label":{"en":"Y"}}],"default":"nope"}]}`, // invalid default
		`{"questions":[{"key":"a","type":"choice","label":{"en":"x"},"options":[{"value":"yes","label":{"en":"Y"}}]},{"key":"b","type":"text","label":{"en":"y"},"show_if":{"key":"a","in":["maybe"]}}]}`, // show_if value not an option
	}
	for i, c := range cases {
		if _, err := parse([]byte(c)); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestShowIfAndNewTypes(t *testing.T) {
	d, err := parse([]byte(`{"questions":[
		{"key":"sec","type":"section","label":{"en":"Section"}},
		{"key":"gate","type":"choice","label":{"en":"G"},"options":[{"value":"yes","label":{"en":"Y"}},{"value":"no","label":{"en":"N"}}]},
		{"key":"follow","type":"text","label":{"en":"F"},"show_if":{"key":"gate","in":["yes"]}},
		{"key":"store","type":"choice","widget":"select","label":{"en":"S"},"options":[{"value":"1","label":{"en":"One"}}]},
		{"key":"hours","type":"number","min":0,"max":80,"label":{"en":"H"}},
		{"key":"day","type":"date","label":{"en":"D"}}
	]}`))
	if err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	if d.Questions[2].ShowIf == nil || d.Questions[2].ShowIf.Key != "gate" {
		t.Fatalf("show_if not parsed: %+v", d.Questions[2])
	}
	if d.Questions[3].Widget != "select" {
		t.Fatalf("widget not parsed: %+v", d.Questions[3])
	}
}

func TestAcceptsValue(t *testing.T) {
	num := Question{Type: TypeNumber, Min: 0, Max: 80}
	if !num.AcceptsValue("40") || num.AcceptsValue("999") || num.AcceptsValue("x") {
		t.Fatal("number AcceptsValue wrong")
	}
	date := Question{Type: TypeDate}
	if !date.AcceptsValue("2026-01-15") || date.AcceptsValue("nope") {
		t.Fatal("date AcceptsValue wrong")
	}
	ch := Question{Type: TypeChoice, Options: []Option{{Value: "yes"}}}
	if !ch.AcceptsValue("yes") || ch.AcceptsValue("no") {
		t.Fatal("choice AcceptsValue wrong")
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
