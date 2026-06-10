package admin

import (
	"errors"
	"net/http"
	"time"
)

// sso signs a user straight into the dashboard from a platform appliance token
// (see appliance.go). The trusted SEC context names the tenant, user, and role;
// the user is auto-provisioned (empty password) if new — sign-in via SSO only,
// the standalone counterpart being /provision's invite.
func (a *Admin) sso(w http.ResponseWriter, r *http.Request) {
	sec, _, err := parseAppliance(a.secret, r.URL.Query().Get("t"))
	if err != nil || sec.User == "" {
		http.Error(w, "invalid sign-in link", http.StatusForbidden)
		return
	}
	role := sec.Role
	if role != RoleAdmin && role != RoleViewer {
		role = RoleViewer
	}

	db, err := a.provider.DB(sec.Ref)
	if err != nil {
		http.Error(w, "invalid tenant", http.StatusBadRequest)
		return
	}
	if a.provider.Multi() {
		a.ensureTenant(db, sec.Ref)
	}

	user, err := userByUsername(db, sec.User)
	if errors.Is(err, errNotFound) {
		if _, ierr := db.Exec(
			`INSERT INTO users(username, password_hash, role, must_change_pw, active, created_at)
			 VALUES(?, '', ?, 0, 1, ?)`,
			sec.User, role, time.Now().Unix(),
		); ierr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		user, err = userByUsername(db, sec.User)
	}
	if err != nil || !user.Active {
		http.Error(w, "invalid sign-in link", http.StatusForbidden)
		return
	}

	raw, _, err := createSession(db, user.ID, a.sessionTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.setSessionCookie(w, sec.Ref, raw)
	setLastLogin(db, user.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
