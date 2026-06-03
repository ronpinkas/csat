// Package admin implements the authenticated dashboard: login/sessions, user
// management with invites, analytics, settings, and CSV export.
package admin

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/httpx"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/web"
)

// Admin holds dependencies for the authenticated area.
type Admin struct {
	db         *sql.DB
	tmpl       *web.Templates
	cfg        *config.Config
	def        *surveydef.Definition
	secret     string
	secure     bool
	sessionTTL time.Duration
	inviteTTL  time.Duration

	loginLimiter *httpx.Limiter
	throttle     *loginThrottle
}

const sessionCookie = "sid"

// New constructs the Admin area, seeding the bootstrap admin on first run and
// starting a background session sweeper.
func New(db *sql.DB, tmpl *web.Templates, cfg *config.Config, def *surveydef.Definition, secret string, secure bool) (*Admin, error) {
	a := &Admin{
		db:           db,
		tmpl:         tmpl,
		cfg:          cfg,
		def:          def,
		secret:       secret,
		secure:       secure,
		sessionTTL:   time.Duration(cfg.Security.SessionTTLHours) * time.Hour,
		inviteTTL:    time.Duration(cfg.Security.InviteTTLHours) * time.Hour,
		loginLimiter: httpx.NewLimiter(20, 10),
		throttle:     newLoginThrottle(),
	}
	if err := a.bootstrap(); err != nil {
		return nil, err
	}
	go a.sweepLoop()
	return a, nil
}

// bootstrap seeds a single admin user if the table is empty.
func (a *Admin) bootstrap() error {
	n, err := countUsers(a.db)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw := a.cfg.Admin.InitialPassword
	if pw == "" {
		pw = randToken()[:16]
		log.Printf("no admin.initial_password set — generated a temporary one for %q:", a.cfg.Admin.Username)
		log.Printf("     %s   (you must change it on first login)", pw)
	}
	hash, err := hashPassword(pw)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(
		`INSERT INTO users(username, password_hash, role, must_change_pw, active, created_at)
		 VALUES(?, ?, ?, 1, 1, ?)`,
		a.cfg.Admin.Username, hash, RoleAdmin, time.Now().Unix(),
	)
	return err
}

func (a *Admin) sweepLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		sweepSessions(a.db)
		sweepInvites(a.db)
	}
}

// ---- request context ----

type ctxKey int

const (
	userKey ctxKey = iota
	sessKey
)

func userFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
}

func sessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessKey).(*Session)
	return s
}

// ---- rendering ----

// base is embedded in every admin page's view model so the layout and nav can
// render consistently.
type base struct {
	SiteName string
	User     *User
	CSRF     string
	Wide     bool
	Lang     string
	LogoURL  string
}

// publicBase is for pre-session pages (login, invite redeem) that have no user.
func (a *Admin) publicBase() base {
	return base{SiteName: a.cfg.Site.Name, Lang: "en", LogoURL: a.cfg.Branding.LogoURL()}
}

func (a *Admin) newBase(r *http.Request) base {
	b := base{SiteName: a.cfg.Site.Name, Lang: "en", LogoURL: a.cfg.Branding.LogoURL()}
	if u := userFrom(r.Context()); u != nil {
		b.User = u
	}
	if s := sessionFrom(r.Context()); s != nil {
		b.CSRF = s.CSRF
	}
	return b
}

func (a *Admin) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := a.tmpl.Render(&buf, name, data); err != nil {
		log.Printf("admin: render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
