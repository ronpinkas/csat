package admin

import (
	"net/http"

	"github.com/ronpinkas/csat/internal/csrf"
)

type loginView struct {
	base
	FormCSRF string
	Error    string
	// Multi shows a "Domain" field (multi-tenant standalone login); RefLocked
	// hides it because the tenant arrived via a ?ref= sign-in link.
	Multi     bool
	RefLocked bool
}

func (a *Admin) loginForm(w http.ResponseWriter, r *http.Request) {
	// Multi-tenant with no ?ref: show the form with a Domain field so a
	// password admin can sign in directly (no sign-in link needed).
	if a.provider.Multi() && r.URL.Query().Get("ref") == "" {
		a.render(w, http.StatusOK, "login.tmpl", loginView{
			base:     a.publicBase(""),
			FormCSRF: csrf.Issue(w, a.secure),
			Multi:    true,
		})
		return
	}
	db, ref, ok := a.tenantFor(r)
	if !ok {
		a.render(w, http.StatusOK, "login.tmpl", loginView{
			base:  a.publicBase(ref),
			Error: "Please use the sign-in link provided for your account.",
		})
		return
	}
	if a.provider.Multi() {
		a.ensureTenant(db, ref) // seed this tenant's admin on first touch
	}
	a.render(w, http.StatusOK, "login.tmpl", loginView{
		base:      a.publicBase(ref),
		FormCSRF:  csrf.Issue(w, a.secure),
		Multi:     a.provider.Multi(),
		RefLocked: a.provider.Multi(),
	})
}

func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db, ref, ok := a.tenantFor(r)
	renderErr := func(msg string) {
		a.render(w, http.StatusOK, "login.tmpl", loginView{
			base:      a.publicBase(ref),
			FormCSRF:  csrf.Issue(w, a.secure),
			Error:     msg,
			Multi:     a.provider.Multi(),
			RefLocked: a.provider.Multi() && r.URL.Query().Get("ref") != "",
		})
	}
	if !ok {
		renderErr("Please use the sign-in link provided for your account.")
		return
	}
	if a.provider.Multi() {
		a.ensureTenant(db, ref)
	}

	if !csrf.Check(r) {
		renderErr("Your session expired. Please try again.")
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")

	if a.throttle.blocked(ref + "\x00" + username) {
		renderErr("Too many attempts. Please wait a few minutes and try again.")
		return
	}

	user, err := userByUsername(db, username)
	if err != nil {
		// keep timing uniform for unknown users
		verifyPassword(dummyHash, password)
		a.throttle.fail(ref + "\x00" + username)
		renderErr("Invalid username or password.")
		return
	}
	if !user.Active || !verifyPassword(user.PasswordHash, password) {
		a.throttle.fail(ref + "\x00" + username)
		renderErr("Invalid username or password.")
		return
	}

	a.throttle.reset(ref + "\x00" + username)
	raw, _, err := createSession(db, user.ID, a.sessionTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.setSessionCookie(w, ref, raw)
	setLastLogin(db, user.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	ref := refFrom(r.Context())
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, raw := a.decodeSession(c.Value); raw != "" {
			if db, derr := a.provider.DB(ref); derr == nil {
				deleteSession(db, raw)
			}
		}
	}
	a.clearSessionCookie(w)
	http.Redirect(w, r, withRef("/login", ref), http.StatusSeeOther)
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

	// A forced first-login change does not ask for the current password (the user
	// just authenticated with it). A voluntary change still requires it.
	if !u.MustChangePW && !verifyPassword(u.PasswordHash, current) {
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
	if err := setPassword(tenantDB(r.Context()), u.ID, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
