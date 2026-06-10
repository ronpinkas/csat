package admin

import (
	"net/http"
	"time"

	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/surveydef"
)

type surveyEditView struct {
	base
	JSON  string
	Sets  []defstore.Version
	SetID int64
	Error string
}

// surveyEditor shows the current (or ?set=<id>) question set as editable JSON,
// plus the list of published sets. Editing publishes a new set (it never mutates
// an existing one), so historical responses stay coherent.
func (a *Admin) surveyEditor(w http.ResponseWriter, r *http.Request) {
	db := tenantDB(r.Context())
	def, id, sets, err := a.resolveSet(db, r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	js, err := def.JSON()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.render(w, http.StatusOK, "survey_edit.tmpl", surveyEditView{
		base: a.newBase(r), JSON: string(js), Sets: sets, SetID: id,
	})
}

// surveyPublish validates the submitted JSON and stores it as a new set.
func (a *Admin) surveyPublish(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db := tenantDB(r.Context())
	raw := r.PostFormValue("definition")

	def, err := surveydef.Parse([]byte(raw))
	if err != nil {
		_, id, sets, _ := a.resolveSet(db, r)
		a.render(w, http.StatusOK, "survey_edit.tmpl", surveyEditView{
			base: a.newBase(r), JSON: raw, Sets: sets, SetID: id,
			Error: "Invalid survey definition: " + err.Error(),
		})
		return
	}
	if _, err := defstore.Add(db, def, time.Now().Unix()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/survey", http.StatusSeeOther)
}
