package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/surveydef"
)

type surveyEditView struct {
	base
	JSON             string
	Sets             []defstore.Version
	SetID            int64
	CurrentIsDefault bool
	SetCount         int
	Error            string
}

// surveyView builds the Survey-tab view model, flagging whether the viewed set
// is the effective default and how many sets exist (both drive which action
// buttons the template shows).
func (a *Admin) surveyView(r *http.Request, js string, sets []defstore.Version, id int64, errMsg string) surveyEditView {
	isDefault := false
	for _, v := range sets {
		if v.ID == id && v.IsDefault {
			isDefault = true
		}
	}
	return surveyEditView{
		base: a.newBase(r), JSON: js, Sets: sets, SetID: id,
		CurrentIsDefault: isDefault, SetCount: len(sets), Error: errMsg,
	}
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
	a.render(w, http.StatusOK, "survey_edit.tmpl", a.surveyView(r, string(js), sets, id, ""))
}

// surveyEditorError re-renders the Survey tab with a message (for rejected
// actions), showing the effective default set.
func (a *Admin) surveyEditorError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	db := tenantDB(r.Context())
	def, id, sets, err := a.resolveSet(db, r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	js, _ := def.JSON()
	a.render(w, status, "survey_edit.tmpl", a.surveyView(r, string(js), sets, id, msg))
}

// surveyPublish validates the submitted JSON and stores it as a new set.
func (a *Admin) surveyPublish(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db := tenantDB(r.Context())
	raw := r.PostFormValue("definition")

	def, err := surveydef.Parse([]byte(raw))
	if err != nil {
		_, id, sets, _ := a.resolveSet(db, r)
		a.render(w, http.StatusOK, "survey_edit.tmpl",
			a.surveyView(r, raw, sets, id, "Invalid survey definition: "+err.Error()))
		return
	}
	id, err := defstore.Add(db, def, time.Now().Unix())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Optional: pin the just-published survey as the default (the checkbox by Publish).
	if r.PostFormValue("set_default") != "" {
		if err := defstore.SetDefault(db, id); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/survey", http.StatusSeeOther)
}

// surveySetDefault pins the posted set as the tenant default.
func (a *Admin) surveySetDefault(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db := tenantDB(r.Context())
	id, err := strconv.ParseInt(r.PostFormValue("set"), 10, 64)
	if err != nil {
		a.surveyEditorError(w, r, http.StatusBadRequest, "Invalid survey.")
		return
	}
	if _, derr := defstore.ByID(db, id); derr != nil {
		a.surveyEditorError(w, r, http.StatusBadRequest, "That survey no longer exists.")
		return
	}
	if err := defstore.SetDefault(db, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/survey?set="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// surveyDelete removes the posted set and every response answered under it.
// Guarded: the effective default and the last remaining set can't be deleted.
func (a *Admin) surveyDelete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db := tenantDB(r.Context())
	id, err := strconv.ParseInt(r.PostFormValue("set"), 10, 64)
	if err != nil {
		a.surveyEditorError(w, r, http.StatusBadRequest, "Invalid survey.")
		return
	}
	sets, err := defstore.List(db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(sets) <= 1 {
		a.surveyEditorError(w, r, http.StatusConflict, "You can't delete the only survey.")
		return
	}
	for _, v := range sets {
		if v.ID == id && v.IsDefault {
			a.surveyEditorError(w, r, http.StatusConflict,
				"You can't delete the default survey — set another survey as the default first.")
			return
		}
	}
	if _, err := defstore.Delete(db, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/survey", http.StatusSeeOther)
}
