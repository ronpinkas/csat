package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/token"
)

// SSOPrefix marks a token as an SSO sign-in (vs. a survey or provision
// token). The subject is SSOPrefix + username, lang carries the role,
// subjectTime the not-after expiry, and ref the tenant. The platform mints these
// with the shared secret to sign a user straight into the dashboard — no
// password — the counterpart to /provision's standalone admin.
const SSOPrefix = "__sso__:"

func (a *Admin) sso(w http.ResponseWriter, r *http.Request) {
	subject, expiry, role, ref, err := token.Decrypt(a.secret, r.URL.Query().Get("t"))
	if err != nil || !strings.HasPrefix(subject, SSOPrefix) || ref == "" {
		http.Error(w, "invalid sign-in link", http.StatusForbidden)
		return
	}
	if expiry != 0 && time.Now().Unix() > expiry {
		http.Error(w, "sign-in link expired", http.StatusForbidden)
		return
	}
	username := strings.TrimPrefix(subject, SSOPrefix)
	if username == "" {
		http.Error(w, "invalid sign-in link", http.StatusForbidden)
		return
	}
	if role != RoleAdmin && role != RoleViewer {
		role = RoleViewer
	}

	db, err := a.provider.DB(ref)
	if err != nil {
		http.Error(w, "invalid tenant", http.StatusBadRequest)
		return
	}
	if a.provider.Multi() {
		a.ensureTenant(db, ref)
	}

	user, err := userByUsername(db, username)
	if errors.Is(err, errNotFound) {
		// Auto-provision the SSO user (empty password — sign-in via sso only,
		// until an admin issues a reset to set one).
		if _, ierr := db.Exec(
			`INSERT INTO users(username, password_hash, role, must_change_pw, active, created_at)
			 VALUES(?, '', ?, 0, 1, ?)`,
			username, role, time.Now().Unix(),
		); ierr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		user, err = userByUsername(db, username)
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
	a.setSessionCookie(w, ref, raw)
	setLastLogin(db, user.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
