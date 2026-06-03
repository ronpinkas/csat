package survey

// messages holds every customer-facing string for one language.
type messages struct {
	Lede            string
	CSATLabel       string
	ResolutionLabel string
	ResYes          string
	ResPartial      string
	ResNo           string
	CESLabel        string
	CESHard         string
	CESEasy         string
	CommentLabel    string
	CommentHint     string
	Submit          string

	DoneTitle  string
	DoneMsg    string
	AlreadyMsg string

	ErrInvalidHeading string
	ErrInvalidMsg     string
	ErrFormHeading    string
	ErrFormMsg        string
	ErrGenericHeading string
	ErrSessionMsg     string
	ErrSaveMsg        string
}

// supportedLangs lists the languages we ship.
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
		Lede:            "Thanks for your call. How did we do? It takes about 20 seconds.",
		CSATLabel:       "Overall, how satisfied were you?",
		ResolutionLabel: "Was your issue resolved?",
		ResYes:          "Yes",
		ResPartial:      "Partially",
		ResNo:           "No",
		CESLabel:        "How easy was it to get your issue handled?",
		CESHard:         "Very hard",
		CESEasy:         "Very easy",
		CommentLabel:    "Anything else you'd like to share?",
		CommentHint:     "Optional",
		Submit:          "Submit feedback",

		DoneTitle:  "Thank you!",
		DoneMsg:    "Your feedback has been recorded. We appreciate you taking the time.",
		AlreadyMsg: "We've already recorded your feedback for this call. Thank you!",

		ErrInvalidHeading: "This link isn't valid",
		ErrInvalidMsg:     "The feedback link is invalid or has expired. If you'd still like to share feedback, please contact us.",
		ErrFormHeading:    "Please complete the form",
		ErrFormMsg:        "A rating was missing or out of range. Please reopen the link and try again.",
		ErrGenericHeading: "Something went wrong",
		ErrSessionMsg:     "Your session expired. Please reopen the link and try again.",
		ErrSaveMsg:        "We couldn't save your feedback right now. Please try again shortly.",
	},
	"es": {
		Lede:            "Gracias por su llamada. ¿Cómo lo hicimos? Toma unos 20 segundos.",
		CSATLabel:       "En general, ¿qué tan satisfecho/a quedó?",
		ResolutionLabel: "¿Se resolvió su problema?",
		ResYes:          "Sí",
		ResPartial:      "Parcialmente",
		ResNo:           "No",
		CESLabel:        "¿Qué tan fácil fue resolver su asunto?",
		CESHard:         "Muy difícil",
		CESEasy:         "Muy fácil",
		CommentLabel:    "¿Algo más que quiera comentar?",
		CommentHint:     "Opcional",
		Submit:          "Enviar comentarios",

		DoneTitle:  "¡Gracias!",
		DoneMsg:    "Hemos registrado sus comentarios. Agradecemos su tiempo.",
		AlreadyMsg: "Ya registramos sus comentarios para esta llamada. ¡Gracias!",

		ErrInvalidHeading: "Este enlace no es válido",
		ErrInvalidMsg:     "El enlace de comentarios no es válido o ha expirado. Si aún desea compartir su opinión, por favor contáctenos.",
		ErrFormHeading:    "Por favor complete el formulario",
		ErrFormMsg:        "Faltó una calificación o estaba fuera de rango. Vuelva a abrir el enlace e inténtelo de nuevo.",
		ErrGenericHeading: "Algo salió mal",
		ErrSessionMsg:     "Su sesión expiró. Vuelva a abrir el enlace e inténtelo de nuevo.",
		ErrSaveMsg:        "No pudimos guardar sus comentarios en este momento. Inténtelo de nuevo en breve.",
	},
}
