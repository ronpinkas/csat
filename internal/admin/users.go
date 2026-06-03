package admin

import (
	"net/http"
	"strconv"
	"strings"
)

type usersView struct {
	base
	Users      []User
	InviteLink string
	Error      string
}

func (a *Admin) usersPage(w http.ResponseWriter, r *http.Request) {
	a.renderUsers(w, r, "", "")
}

func (a *Admin) renderUsers(w http.ResponseWriter, r *http.Request, inviteLink, errMsg string) {
	users, err := listUsers(a.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.render(w, http.StatusOK, "users.tmpl", usersView{
		base:       a.newBase(r),
		Users:      users,
		InviteLink: inviteLink,
		Error:      errMsg,
	})
}

func (a *Admin) createInvite(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	role := r.PostFormValue("role")
	if role != RoleAdmin && role != RoleViewer {
		role = RoleViewer
	}
	username := strings.TrimSpace(r.PostFormValue("username"))

	raw, err := createInviteRow(a.db, role, username, u.ID, a.inviteTTL)
	if err != nil {
		a.renderUsers(w, r, "", "Could not create invite. Please try again.")
		return
	}
	link := requestBaseURL(a, r) + "/invite?t=" + raw
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
	target, err := userByID(a.db, id)
	if err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.Role == RoleAdmin && a.activeAdminCount() <= 1 {
		a.renderUsers(w, r, "", "You can't deactivate the last active admin.")
		return
	}
	if err := deactivateUser(a.db, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (a *Admin) activeAdminCount() int {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ? AND active = 1`, RoleAdmin).Scan(&n)
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
