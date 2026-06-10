package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/csrf"
)

// createResetRow mints a one-time reset token for an existing user and clears
// any pending self-service request (the admin is now fulfilling it).
func createResetRow(db *sql.DB, userID, createdBy int64, ttl time.Duration) (rawToken string, err error) {
	rawToken = randToken()
	now := time.Now()
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(
		`INSERT INTO password_resets(token_hash, user_id, created_by, created_at, expires_at)
		 VALUES(?, ?, ?, ?, ?)`,
		hashToken(rawToken), userID, createdBy, now.Unix(), now.Add(ttl).Unix(),
	); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`UPDATE users SET reset_requested_at = NULL WHERE id = ?`, userID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return rawToken, nil
}

// resetByToken resolves a raw reset token to its user id, rejecting tokens that
// are expired or already used.
func resetByToken(db *sql.DB, rawToken string) (userID int64, err error) {
	err = db.QueryRow(
		`SELECT user_id FROM password_resets
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		hashToken(rawToken), time.Now().Unix(),
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errNotFound
		}
		return 0, err
	}
	return userID, nil
}

// redeemReset sets the user's new password and consumes the token atomically.
// It also reactivates the account (so a deactivated user can recover), clears
// any pending request, and revokes all of the user's existing sessions.
func redeemReset(db *sql.DB, rawToken, passwordHash string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		resetID int64
		userID  int64
	)
	err = tx.QueryRow(
		`SELECT id, user_id FROM password_resets
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		hashToken(rawToken), time.Now().Unix(),
	).Scan(&resetID, &userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		return err
	}

	if _, err = tx.Exec(
		`UPDATE users SET password_hash = ?, must_change_pw = 0, active = 1, reset_requested_at = NULL
		 WHERE id = ?`, passwordHash, userID,
	); err != nil {
		return err
	}

	r, err := tx.Exec(
		`UPDATE password_resets SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		time.Now().Unix(), resetID,
	)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return errNotFound // raced with another redemption
	}

	// Revoke existing sessions: a password reset should invalidate any live
	// login, and any token still outstanding for this user is now moot.
	if _, err = tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func sweepResets(db *sql.DB) {
	_, _ = db.Exec(`DELETE FROM password_resets WHERE used_at IS NULL AND expires_at < ?`, time.Now().Unix())
}

// ---- public handlers: self-service "forgot password" ----

type forgotView struct {
	base
	FormCSRF string
	Sent     bool
	Error    string
}

func (a *Admin) forgotForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusOK, "forgot.tmpl", forgotView{
		base:     a.publicBase(a.refFromRequest(r)),
		FormCSRF: csrf.Issue(w, a.secure),
	})
}

func (a *Admin) forgot(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db, ref, ok := a.tenantFor(r)
	if !csrf.Check(r) {
		a.render(w, http.StatusOK, "forgot.tmpl", forgotView{
			base:     a.publicBase(ref),
			FormCSRF: csrf.Issue(w, a.secure),
			Error:    "Your session expired. Please try again.",
		})
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	if ok && username != "" {
		// Best-effort: a missing username is a no-op inside the store call.
		_ = requestPasswordReset(db, username)
	}
	// Always the same response, whether or not the account (or tenant) exists, so
	// the page can't be used to enumerate usernames.
	a.render(w, http.StatusOK, "forgot.tmpl", forgotView{
		base: a.publicBase(ref),
		Sent: true,
	})
}

// ---- public handlers: redeem an admin-issued reset link ----

type resetView struct {
	base
	FormCSRF string
	Token    string
	Error    string
	Invalid  bool
}

func (a *Admin) resetForm(w http.ResponseWriter, r *http.Request) {
	db, ref, ok := a.tenantFor(r)
	tok := r.URL.Query().Get("t")
	if !ok {
		a.render(w, http.StatusOK, "reset.tmpl", resetView{base: a.publicBase(ref), Invalid: true})
		return
	}
	if _, err := resetByToken(db, tok); err != nil {
		a.render(w, http.StatusOK, "reset.tmpl", resetView{base: a.publicBase(ref), Invalid: true})
		return
	}
	a.render(w, http.StatusOK, "reset.tmpl", resetView{
		base:     a.publicBase(ref),
		FormCSRF: csrf.Issue(w, a.secure),
		Token:    tok,
	})
}

func (a *Admin) reset(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	db, ref, ok := a.tenantFor(r)
	tok := r.URL.Query().Get("t")
	if tok == "" {
		tok = r.PostFormValue("t")
	}

	if !ok {
		a.render(w, http.StatusOK, "reset.tmpl", resetView{base: a.publicBase(ref), Invalid: true})
		return
	}
	if _, err := resetByToken(db, tok); err != nil {
		a.render(w, http.StatusOK, "reset.tmpl", resetView{base: a.publicBase(ref), Invalid: true})
		return
	}

	rerender := func(msg string) {
		a.render(w, http.StatusOK, "reset.tmpl", resetView{
			base:     a.publicBase(ref),
			FormCSRF: csrf.Issue(w, a.secure),
			Token:    tok,
			Error:    msg,
		})
	}

	if !csrf.Check(r) {
		rerender("Your session expired. Please try again.")
		return
	}
	password := r.PostFormValue("new")
	confirm := r.PostFormValue("confirm")
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
	switch err := redeemReset(db, tok, hash); {
	case err == nil:
		http.Redirect(w, r, withRef("/login", ref), http.StatusSeeOther)
	case errors.Is(err, errNotFound):
		a.render(w, http.StatusOK, "reset.tmpl", resetView{base: a.publicBase(ref), Invalid: true})
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---- admin handlers ----

// resetUser mints a reset link an admin can hand to a user who has lost access.
func (a *Admin) resetUser(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	db := tenantDB(r.Context())
	id, err := strconv.ParseInt(r.PostFormValue("user_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if _, err := userByID(db, id); err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	raw, err := createResetRow(db, id, u.ID, a.inviteTTL)
	if err != nil {
		a.renderUsers(w, r, "", "Could not create a reset link. Please try again.")
		return
	}
	link := requestBaseURL(a, r) + withRef("/reset?t="+raw, refFrom(r.Context()))
	a.renderUsersWithReset(w, r, link)
}

// deleteUser permanently removes an account (and frees its username for reuse).
func (a *Admin) deleteUser(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	db := tenantDB(r.Context())
	id, err := strconv.ParseInt(r.PostFormValue("user_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if id == me.ID {
		a.renderUsers(w, r, "", "You can't delete your own account.")
		return
	}
	target, err := userByID(db, id)
	if err != nil {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.Role == RoleAdmin && adminCount(db) <= 1 {
		a.renderUsers(w, r, "", "You can't delete the last admin account.")
		return
	}
	if err := deleteUser(db, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
