package survey

// messages holds the system (non-question) strings for one language. Question
// wording, intro, and thank-you copy come from the survey definition; these are
// the fixed UI strings around it.
type messages struct {
	Submit     string
	DoneTitle  string
	AlreadyMsg string

	// Save-progress UI (only rendered when the survey has allow_save).
	SaveProgress string
	SavedMsg     string
	ConfirmTitle string
	Cancel       string
	Saving       string
	SavedNote    string
	SaveFailed   string

	// Required-question affordances. {n} is replaced with a count.
	RequiredLegend string
	RequiredLeft   string

	ErrInvalidHeading string
	ErrInvalidMsg     string
	ErrFormHeading    string
	ErrFormMsg        string
	ErrGenericHeading string
	ErrSessionMsg     string
	ErrSaveMsg        string
}

var supportedLangs = map[string]bool{"en": true, "es": true}

// normalizeLang maps an arbitrary tag to a supported language, defaulting to en.
func normalizeLang(lang string) string {
	if len(lang) >= 2 {
		lang = lang[:2]
	}
	if supportedLangs[lang] {
		return lang
	}
	return "en"
}

func stringsFor(lang string) messages {
	if s, ok := catalog[normalizeLang(lang)]; ok {
		return s
	}
	return catalog["en"]
}

var catalog = map[string]messages{
	"en": {
		Submit:            "Submit feedback",
		DoneTitle:         "Thank you!",
		AlreadyMsg:        "We've already recorded your feedback. Thank you!",
		SaveProgress:      "Save progress",
		SavedMsg:          "Your progress has been saved. You can close this page and continue later from the same link, on any device.",
		ConfirmTitle:      "What would you like to do?",
		Cancel:            "Cancel",
		Saving:            "Saving…",
		SavedNote:         "Progress saved — not submitted yet.",
		SaveFailed:        "We couldn't save just now. Check your connection and try again.",
		RequiredLegend:    "Required",
		RequiredLeft:      "{n} required question(s) still to answer — show me",
		ErrInvalidHeading: "This link isn't valid",
		ErrInvalidMsg:     "The feedback link is invalid or has expired. If you'd still like to share feedback, please contact us.",
		ErrFormHeading:    "Please complete the form",
		ErrFormMsg:        "An answer was missing or out of range. Please reopen the link and try again.",
		ErrGenericHeading: "Something went wrong",
		ErrSessionMsg:     "Your session expired. Please reopen the link and try again.",
		ErrSaveMsg:        "We couldn't save your feedback right now. Please try again shortly.",
	},
	"es": {
		Submit:            "Enviar comentarios",
		DoneTitle:         "¡Gracias!",
		AlreadyMsg:        "Ya registramos sus comentarios. ¡Gracias!",
		SaveProgress:      "Guardar progreso",
		SavedMsg:          "Su progreso se ha guardado. Puede cerrar esta página y continuar más tarde con el mismo enlace, desde cualquier dispositivo.",
		ConfirmTitle:      "¿Qué desea hacer?",
		Cancel:            "Cancelar",
		Saving:            "Guardando…",
		SavedNote:         "Progreso guardado: aún no se ha enviado.",
		SaveFailed:        "No pudimos guardar en este momento. Revise su conexión e inténtelo de nuevo.",
		RequiredLegend:    "Obligatorio",
		RequiredLeft:      "{n} pregunta(s) obligatoria(s) sin responder — mostrar",
		ErrInvalidHeading: "Este enlace no es válido",
		ErrInvalidMsg:     "El enlace de comentarios no es válido o ha expirado. Si aún desea compartir su opinión, por favor contáctenos.",
		ErrFormHeading:    "Por favor complete el formulario",
		ErrFormMsg:        "Faltó una respuesta o estaba fuera de rango. Vuelva a abrir el enlace e inténtelo de nuevo.",
		ErrGenericHeading: "Algo salió mal",
		ErrSessionMsg:     "Su sesión expiró. Vuelva a abrir el enlace e inténtelo de nuevo.",
		ErrSaveMsg:        "No pudimos guardar sus comentarios en este momento. Inténtelo de nuevo en breve.",
	},
}
