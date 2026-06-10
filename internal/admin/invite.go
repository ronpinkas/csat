package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/csrf"
)

// Invite is a pending invitation.
type Invite struct {
	ID       int64
	Role     string
	Username sql.NullString
	// Platform is true for a platform-minted invite (created_by IS NULL). Such
	// invites may override an existing account on redemption (the provisioning
	// break-glass: an admin who lost their password redeems with the same
	// username to reclaim that account).
	Platform bool
}

func createInviteRow(db *sql.DB, role, username string, createdBy int64, ttl time.Duration) (rawToken string, err error) {
	rawToken = randToken()
	now := time.Now()
	var uname any
	if username != "" {
		uname = username
	}
	// createdBy == 0 means "no tenant creator" (a platform-minted admin invite
	// for a brand-new tenant) — store NULL.
	var creator any
	if createdBy != 0 {
		creator = createdBy
	}
	_, err = db.Exec(
		`INSERT INTO invites(token_hash, role, username, created_by, created_at, expires_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		hashToken(rawToken), role, uname, creator, now.Unix(), now.Add(ttl).Unix(),
	)
	if err != nil {
		return "", err
	}
	return rawToken, nil
}

func inviteByToken(db *sql.DB, rawToken string) (*Invite, error) {
	var (
		inv     Invite
		creator sql.NullInt64
	)
	err := db.QueryRow(
		`SELECT id, role, username, created_by FROM invites
		 WHERE token_hash = ? AND redeemed_at IS NULL AND expires_at > ?`,
		hashToken(rawToken), time.Now().Unix(),
	).Scan(&inv.ID, &inv.Role, &inv.Username, &creator)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	inv.Platform = !creator.Valid // created_by IS NULL => platform-minted
	return &inv, nil
}

var errUsernameTaken = errors.New("username taken")

// redeemInvite creates the user and marks the invite redeemed atomically.
func redeemInvite(db *sql.DB, inv *Invite, username, passwordHash string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var newID int64
	res, err := tx.Exec(
		`INSERT INTO users(username, password_hash, role, must_change_pw, active, created_at)
		 VALUES(?, ?, ?, 0, 1, ?)`,
		username, passwordHash, inv.Role, time.Now().Unix(),
	)
	switch {
	case err == nil:
		newID, _ = res.LastInsertId()
	case !isUnique(err):
		return err
	case !inv.Platform:
		// A normal (admin-issued) invite never overrides an existing account.
		return errUsernameTaken
	default:
		// Platform break-glass: the entered username already exists — reclaim
		// that account with the invite's role and the new password, reactivate
		// it, and revoke its sessions (the invite acts as a password reset).
		if _, uerr := tx.Exec(
			`UPDATE users SET password_hash = ?, role = ?, must_change_pw = 0, active = 1, reset_requested_at = NULL
			 WHERE username = ?`, passwordHash, inv.Role, username,
		); uerr != nil {
			return uerr
		}
		if uerr := tx.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&newID); uerr != nil {
			return uerr
		}
		if _, uerr := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, newID); uerr != nil {
			return uerr
		}
	}

	r, err := tx.Exec(
		`UPDATE invites SET redeemed_at = ?, redeemed_user_id = ? WHERE id = ? AND redeemed_at IS NULL`,
		time.Now().Unix(), newID, inv.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return errNotFound // raced with another redemption
	}
	return tx.Commit()
}

func sweepInvites(db *sql.DB) {
	_, _ = db.Exec(`DELETE FROM invites WHERE redeemed_at IS NULL AND expires_at < ?`, time.Now().Unix())
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "CONSTRAINT FAILED")
}

// ---- handlers ----

type inviteView struct {
	base
	FormCSRF       string
	Token          string
	Username       string
	PresetUsername bool
	Error          string
	Invalid        bool
}

func (a *Admin) inviteRedeemForm(w http.ResponseWriter, r *http.Request) {
	db, ref, ok := a.tenantFor(r)
	tok := r.URL.Query().Get("t")
	var inv *Invite
	if ok {
		inv, _ = inviteByToken(db, tok)
	}
	if !ok || inv == nil {
		a.render(w, http.StatusOK, "invite_redeem.tmpl", inviteView{
			base: a.publicBase(ref), Invalid: true,
		})
		return
	}
	a.render(w, http.StatusOK, "invite_redeem.tmpl", inviteView{
		base:           a.publicBase(ref),
		FormCSRF:       csrf.Issue(w, a.secure),
		Token:          tok,
		Username:       inv.Username.String,
		PresetUsername: inv.Username.Valid && inv.Username.String != "",
	})
}

func (a *Admin) inviteRedeem(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db, ref, ok := a.tenantFor(r)
	tok := r.URL.Query().Get("t")
	if tok == "" {
		tok = r.PostFormValue("t")
	}

	var inv *Invite
	if ok {
		inv, _ = inviteByToken(db, tok)
	}
	if !ok || inv == nil {
		a.render(w, http.StatusOK, "invite_redeem.tmpl", inviteView{
			base: a.publicBase(ref), Invalid: true,
		})
		return
	}

	username := inv.Username.String
	if !(inv.Username.Valid && inv.Username.String != "") {
		username = strings.TrimSpace(r.PostFormValue("username"))
	}
	password := r.PostFormValue("new")
	confirm := r.PostFormValue("confirm")

	rerender := func(msg string) {
		a.render(w, http.StatusOK, "invite_redeem.tmpl", inviteView{
			base:           a.publicBase(ref),
			FormCSRF:       csrf.Issue(w, a.secure),
			Token:          tok,
			Username:       username,
			PresetUsername: inv.Username.Valid && inv.Username.String != "",
			Error:          msg,
		})
	}

	if !csrf.Check(r) {
		rerender("Your session expired. Please try again.")
		return
	}
	if username == "" {
		rerender("Please choose a username.")
		return
	}
	if len(password) < minPasswordLen {
		rerender("Password must be at least 12 characters.")
		return
	}
	if password != confirm {
		rerender("The passwords don't match.")
		return
	}
	hash, err := hashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch err := redeemInvite(db, inv, username, hash); {
	case err == nil:
		http.Redirect(w, r, withRef("/login", ref), http.StatusSeeOther)
	case errors.Is(err, errUsernameTaken):
		rerender("That username is already taken.")
	case errors.Is(err, errNotFound):
		a.render(w, http.StatusOK, "invite_redeem.tmpl", inviteView{
			base: a.publicBase(ref), Invalid: true,
		})
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
