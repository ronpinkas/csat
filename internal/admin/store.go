package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// roles
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

var errNotFound = errors.New("not found")

// User is an admin/viewer account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	MustChangePW bool
	Active       bool
}

// Session is a server-side session.
type Session struct {
	ID        string // sha256(raw token), hex
	UserID    int64
	CSRF      string
	ExpiresAt int64
}

// randToken returns a 256-bit URL-safe random string.
func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken returns the hex sha256 of a raw token (what we store at rest).
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ---- users ----

func userByUsername(db *sql.DB, username string) (*User, error) {
	return scanUser(db.QueryRow(
		`SELECT id, username, password_hash, role, must_change_pw, active FROM users WHERE username = ?`, username))
}

func userByID(db *sql.DB, id int64) (*User, error) {
	return scanUser(db.QueryRow(
		`SELECT id, username, password_hash, role, must_change_pw, active FROM users WHERE id = ?`, id))
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.MustChangePW, &u.Active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &u, nil
}

func countUsers(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func listUsers(db *sql.DB) ([]User, error) {
	rows, err := db.Query(
		`SELECT id, username, password_hash, role, must_change_pw, active FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.MustChangePW, &u.Active); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func setPassword(db *sql.DB, userID int64, hash string) error {
	_, err := db.Exec(`UPDATE users SET password_hash = ?, must_change_pw = 0 WHERE id = ?`, hash, userID)
	return err
}

func setLastLogin(db *sql.DB, userID int64) {
	_, _ = db.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), userID)
}

func deactivateUser(db *sql.DB, userID int64) error {
	_, err := db.Exec(`UPDATE users SET active = 0 WHERE id = ?`, userID)
	return err
}

// ---- sessions ----

func createSession(db *sql.DB, userID int64, ttl time.Duration) (rawToken string, csrf string, err error) {
	rawToken = randToken()
	csrf = randToken()
	now := time.Now()
	_, err = db.Exec(
		`INSERT INTO sessions(id, user_id, csrf_token, created_at, expires_at) VALUES(?, ?, ?, ?, ?)`,
		hashToken(rawToken), userID, csrf, now.Unix(), now.Add(ttl).Unix(),
	)
	if err != nil {
		return "", "", err
	}
	return rawToken, csrf, nil
}

// lookupSession resolves a raw cookie token to its session and user, rejecting
// expired sessions and inactive users.
func lookupSession(db *sql.DB, rawToken string) (*Session, *User, error) {
	var s Session
	err := db.QueryRow(
		`SELECT id, user_id, csrf_token, expires_at FROM sessions WHERE id = ?`, hashToken(rawToken),
	).Scan(&s.ID, &s.UserID, &s.CSRF, &s.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, errNotFound
		}
		return nil, nil, err
	}
	if time.Now().Unix() >= s.ExpiresAt {
		_, _ = db.Exec(`DELETE FROM sessions WHERE id = ?`, s.ID)
		return nil, nil, errNotFound
	}
	u, err := userByID(db, s.UserID)
	if err != nil || !u.Active {
		return nil, nil, errNotFound
	}
	return &s, u, nil
}

func deleteSession(db *sql.DB, rawToken string) {
	_, _ = db.Exec(`DELETE FROM sessions WHERE id = ?`, hashToken(rawToken))
}

func sweepSessions(db *sql.DB) {
	_, _ = db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
}
