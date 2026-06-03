package survey

// messages holds the system (non-question) strings for one language. Question
// wording, intro, and thank-you copy come from the survey definition; these are
// the fixed UI strings around it.
type messages struct {
	Submit     string
	DoneTitle  string
	AlreadyMsg string

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
		ErrInvalidHeading: "Este enlace no es válido",
		ErrInvalidMsg:     "El enlace de comentarios no es válido o ha expirado. Si aún desea compartir su opinión, por favor contáctenos.",
		ErrFormHeading:    "Por favor complete el formulario",
		ErrFormMsg:        "Faltó una respuesta o estaba fuera de rango. Vuelva a abrir el enlace e inténtelo de nuevo.",
		ErrGenericHeading: "Algo salió mal",
		ErrSessionMsg:     "Su sesión expiró. Vuelva a abrir el enlace e inténtelo de nuevo.",
		ErrSaveMsg:        "No pudimos guardar sus comentarios en este momento. Inténtelo de nuevo en breve.",
	},
}
