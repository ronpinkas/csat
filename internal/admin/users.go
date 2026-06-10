package admin

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

type usersView struct {
	base
	Users      []User
	InviteLink string
	ResetLink  string
	Error      string
}

func (a *Admin) usersPage(w http.ResponseWriter, r *http.Request) {
	a.renderUsers(w, r, "", "")
}

func (a *Admin) renderUsers(w http.ResponseWriter, r *http.Request, inviteLink, errMsg string) {
	a.renderUsersView(w, r, usersView{InviteLink: inviteLink, Error: errMsg})
}

// renderUsersWithReset re-renders the page showing a freshly minted reset link.
func (a *Admin) renderUsersWithReset(w http.ResponseWriter, r *http.Request, resetLink string) {
	a.renderUsersView(w, r, usersView{ResetLink: resetLink})
}

func (a *Admin) renderUsersView(w http.ResponseWriter, r *http.Request, v usersView) {
	users, err := listUsers(tenantDB(r.Context()))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	v.base = a.newBase(r)
	v.Users = users
	a.render(w, http.StatusOK, "users.tmpl", v)
}

func (a *Admin) createInvite(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	role := r.PostFormValue("role")
	if role != RoleAdmin && role != RoleViewer {
		role = RoleViewer
	}
	username := strings.TrimSpace(r.PostFormValue("username"))

	raw, err := createInviteRow(tenantDB(r.Context()), role, username, u.ID, a.inviteTTL)
	if err != nil {
		a.renderUsers(w, r, "", "Could not create invite. Please try again.")
		return
	}
	link := requestBaseURL(a, r) + withRef("/invite?t="+raw, refFrom(r.Context()))
	a.renderUsers(w, r, link, "")
}

func (a *Admin) deactivate(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PostFormValue("user_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if id == me.ID {
		a.renderUsers(w, r, "", "You can't deactivate your own account.")
		return
	}
	db := tenantDB(r.Context())
	target, err := userByID(db, id)
	if err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.Role == RoleAdmin && activeAdminCount(db) <= 1 {
		a.renderUsers(w, r, "", "You can't deactivate the last active admin.")
		return
	}
	if err := deactivateUser(db, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func activeAdminCount(db *sql.DB) int {
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ? AND active = 1`, RoleAdmin).Scan(&n)
	return n
}

// requestBaseURL reconstructs the externally-visible base URL for building
// absolute links (e.g. invites), honoring proxy headers only when trusted.
func requestBaseURL(a *Admin, r *http.Request) string {
	scheme := "http"
	if a.secure {
		scheme = "https"
	}
	host := r.Host
	if a.cfg.Server.TrustProxy {
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
			scheme = strings.TrimSpace(strings.Split(p, ",")[0])
		}
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			host = strings.TrimSpace(strings.Split(h, ",")[0])
		}
	}
	return scheme + "://" + host
}
