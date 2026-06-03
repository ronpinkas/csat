package admin

import (
	"net/http"

	"github.com/instantaiguru/csat/internal/csrf"
)

type loginView struct {
	base
	FormCSRF string
	Error    string
}

func (a *Admin) loginForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusOK, "login.tmpl", loginView{
		base:     a.publicBase(),
		FormCSRF: csrf.Issue(w, a.secure),
	})
}

func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	renderErr := func(msg string) {
		a.render(w, http.StatusOK, "login.tmpl", loginView{
			base:     a.publicBase(),
			FormCSRF: csrf.Issue(w, a.secure),
			Error:    msg,
		})
	}

	if !csrf.Check(r) {
		renderErr("Your session expired. Please try again.")
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	if a.throttle.blocked(username) {
		renderErr("Too many attempts. Please wait a few minutes and try again.")
		return
	}

	user, err := userByUsername(a.db, username)
	if err != nil {
		// keep timing uniform for unknown users
		verifyPassword(dummyHash, password)
		a.throttle.fail(username)
		renderErr("Invalid username or password.")
		return
	}
	if !user.Active || !verifyPassword(user.PasswordHash, password) {
		a.throttle.fail(username)
		renderErr("Invalid username or password.")
		return
	}

	a.throttle.reset(username)
	raw, _, err := createSession(a.db, user.ID, a.sessionTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.setSessionCookie(w, raw)
	setLastLogin(a.db, user.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		deleteSession(a.db, c.Value)
	}
	a.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *Admin) home(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

type changePWView struct {
	base
	Forced bool
	Error  string
}

func (a *Admin) changePasswordForm(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	a.render(w, http.StatusOK, "force_change.tmpl", changePWView{
		base:   a.newBase(r),
		Forced: u != nil && u.MustChangePW,
	})
}

func (a *Admin) changePassword(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	_ = r.ParseForm()
	renderErr := func(msg string) {
		a.render(w, http.StatusOK, "force_change.tmpl", changePWView{
			base:   a.newBase(r),
			Forced: u != nil && u.MustChangePW,
			Error:  msg,
		})
	}

	current := r.PostFormValue("current")
	next := r.PostFormValue("new")
	confirm := r.PostFormValue("confirm")

	if !verifyPassword(u.PasswordHash, current) {
		renderErr("Your current password is incorrect.")
		return
	}
	if len(next) < minPasswordLen {
		renderErr("New password must be at least 12 characters.")
		return
	}
	if next != confirm {
		renderErr("The new passwords don't match.")
		return
	}
	hash, err := hashPassword(next)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := setPassword(a.db, u.ID, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
