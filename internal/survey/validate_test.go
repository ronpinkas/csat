package survey

import (
	"net/url"
	"testing"

	"github.com/ronpinkas/csat/internal/surveydef"
)

func mustDef(t *testing.T, js string) *surveydef.Definition {
	t.Helper()
	d, err := surveydef.Parse([]byte(js))
	if err != nil {
		t.Fatalf("parse def: %v", err)
	}
	return d
}

func hasKey(answers []answer, key string) bool {
	for _, a := range answers {
		if a.key == key {
			return true
		}
	}
	return false
}

// gateDef: a Y/N choice gating a required follow-up text field.
const gateDef = `{"questions":[
	{"key":"witness","type":"choice","label":{"en":"?"},"options":[{"value":"yes","label":{"en":"Y"}},{"value":"no","label":{"en":"N"}}]},
	{"key":"detail","type":"text","required":true,"label":{"en":"Describe"},"show_if":{"key":"witness","in":["yes"]}}
]}`

func TestGatingSkipsRequiredWhenHidden(t *testing.T) {
	def := mustDef(t, gateDef)
	answers, ok := parseAnswers(url.Values{"witness": {"no"}}, def)
	if !ok {
		t.Fatal("expected ok: hidden required follow-up must not be enforced")
	}
	if hasKey(answers, "detail") {
		t.Fatal("hidden follow-up should not produce an answer")
	}
	if !hasKey(answers, "witness") {
		t.Fatal("controller answer missing")
	}
}

func TestGatingEnforcesRequiredWhenShown(t *testing.T) {
	def := mustDef(t, gateDef)
	if _, ok := parseAnswers(url.Values{"witness": {"yes"}}, def); ok {
		t.Fatal("expected failure: shown required follow-up was left blank")
	}
}

func TestGatingDropsStaleHiddenValue(t *testing.T) {
	def := mustDef(t, gateDef)
	// Condition not met, but a stale value is posted for the hidden field.
	answers, ok := parseAnswers(url.Values{"witness": {"no"}, "detail": {"stale text"}}, def)
	if !ok {
		t.Fatal("expected ok")
	}
	if hasKey(answers, "detail") {
		t.Fatal("stale hidden value must be dropped, not stored")
	}
}

func TestGatingStoresBothWhenShown(t *testing.T) {
	def := mustDef(t, gateDef)
	answers, ok := parseAnswers(url.Values{"witness": {"yes"}, "detail": {"a real answer"}}, def)
	if !ok || !hasKey(answers, "witness") || !hasKey(answers, "detail") {
		t.Fatalf("expected both answers stored, ok=%v answers=%+v", ok, answers)
	}
}

func TestNumberAndDateValidation(t *testing.T) {
	def := mustDef(t, `{"questions":[
		{"key":"hours","type":"number","min":0,"max":80,"required":true,"label":{"en":"H"}},
		{"key":"day","type":"date","label":{"en":"D"}}
	]}`)
	if _, ok := parseAnswers(url.Values{"hours": {"40"}, "day": {"2026-01-15"}}, def); !ok {
		t.Fatal("valid number/date rejected")
	}
	if _, ok := parseAnswers(url.Values{"hours": {"abc"}}, def); ok {
		t.Fatal("non-numeric hours accepted")
	}
	if _, ok := parseAnswers(url.Values{"hours": {"999"}}, def); ok {
		t.Fatal("out-of-range hours accepted")
	}
	if _, ok := parseAnswers(url.Values{"hours": {"40"}, "day": {"2026-13-40"}}, def); ok {
		t.Fatal("impossible date accepted")
	}
}

func TestSectionProducesNoAnswer(t *testing.T) {
	def := mustDef(t, `{"questions":[
		{"key":"intro","type":"section","label":{"en":"Section"}},
		{"key":"csat","type":"stars","required":true,"label":{"en":"C"}}
	]}`)
	answers, ok := parseAnswers(url.Values{"csat": {"5"}}, def)
	if !ok {
		t.Fatal("expected ok")
	}
	if hasKey(answers, "intro") {
		t.Fatal("section must not produce an answer")
	}
}
