// Package admin implements the authenticated dashboard: login/sessions, user
// management with invites, analytics, settings, and CSV export.
package admin

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/httpx"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/web"
)

// Admin holds dependencies for the authenticated area.
type Admin struct {
	provider   tenant.Provider
	tmpl       *web.Templates
	cfg        *config.Config
	def        *surveydef.Definition
	secret     string
	secure     bool
	sessionTTL time.Duration
	inviteTTL  time.Duration

	loginLimiter *httpx.Limiter
	throttle     *loginThrottle

	// bootstrapped guards lazy per-tenant admin seeding in multi-tenant mode.
	bootstrapped sync.Map
}

const sessionCookie = "sid"

// New constructs the Admin area and starts a background session sweeper. In
// single-tenant mode it seeds the bootstrap admin immediately; in multi-tenant
// mode each tenant's admin is seeded lazily on its first /login (ensureTenant).
func New(provider tenant.Provider, tmpl *web.Templates, cfg *config.Config, def *surveydef.Definition, secret string, secure bool) (*Admin, error) {
	a := &Admin{
		provider:     provider,
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
	if !provider.Multi() {
		db, err := provider.DB("")
		if err != nil {
			return nil, err
		}
		if err := a.bootstrap(db); err != nil {
			return nil, err
		}
	}
	go a.sweepLoop()
	return a, nil
}

// bootstrap seeds a single admin user into db if its users table is empty. It is
// idempotent (a non-empty table is a no-op), so it is safe to call repeatedly.
func (a *Admin) bootstrap(db *sql.DB) error {
	n, err := countUsers(db)
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
	_, err = db.Exec(
		`INSERT INTO users(username, password_hash, role, must_change_pw, active, created_at)
		 VALUES(?, ?, ?, 1, 1, ?)`,
		a.cfg.Admin.Username, hash, RoleAdmin, time.Now().Unix(),
	)
	return err
}

// ensureTenant lazily seeds a tenant's bootstrap admin on first touch (multi
// mode). Guarded so the empty-table check runs at most once per ref per process.
func (a *Admin) ensureTenant(db *sql.DB, ref string) {
	if _, done := a.bootstrapped.Load(ref); done {
		return
	}
	_ = a.bootstrap(db)
	a.bootstrapped.Store(ref, true)
}

func (a *Admin) sweepLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		for _, db := range a.provider.Handles() {
			sweepSessions(db)
			sweepInvites(db)
			sweepResets(db)
		}
	}
}

// ---- request context ----

type ctxKey int

const (
	userKey ctxKey = iota
	sessKey
	tdbKey
	refKey
)

func userFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
}

func sessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessKey).(*Session)
	return s
}

// tenantDB returns the resolved tenant database for the request (set by
// requireAuth for authenticated routes).
func tenantDB(ctx context.Context) *sql.DB {
	db, _ := ctx.Value(tdbKey).(*sql.DB)
	return db
}

// refFrom returns the resolved tenant ref for the request ("" in single mode).
func refFrom(ctx context.Context) string {
	s, _ := ctx.Value(refKey).(string)
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
	// Ref is the tenant ref to thread through pre-session forms/links in
	// multi-tenant mode. Empty in single-tenant mode (templates omit it).
	Ref string
}

// publicBase is for pre-session pages (login, invite redeem) that have no user.
// ref is carried into the page's forms/links so the tenant survives the next
// request (empty in single-tenant mode).
func (a *Admin) publicBase(ref string) base {
	return base{SiteName: a.cfg.Site.Name, Lang: "en", LogoURL: a.cfg.Branding.LogoURL(), Ref: ref}
}

func (a *Admin) newBase(r *http.Request) base {
	b := base{SiteName: a.cfg.Site.Name, Lang: "en", LogoURL: a.cfg.Branding.LogoURL(), Ref: refFrom(r.Context())}
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
