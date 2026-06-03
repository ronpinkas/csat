package admin

import (
	"context"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"
)

// Mount registers all admin/auth routes onto mux with appropriate middleware.
func (a *Admin) Mount(mux *http.ServeMux) {
	rl := a.loginLimiter.Middleware(a.cfg.Server.TrustProxy, a.cfg.Server.TrustedProxies)

	// public (pre-session)
	mux.Handle("GET /login", http.HandlerFunc(a.loginForm))
	mux.Handle("POST /login", rl(http.HandlerFunc(a.login)))
	mux.Handle("GET /invite", http.HandlerFunc(a.inviteRedeemForm))
	mux.Handle("POST /invite", rl(http.HandlerFunc(a.inviteRedeem)))

	// session required
	mux.Handle("GET /{$}", a.authed(a.home))
	mux.Handle("POST /logout", a.authedCSRF(a.logout))
	mux.Handle("GET /account/password", a.authed(a.changePasswordForm))
	mux.Handle("POST /account/password", a.authedCSRF(a.changePassword))
	mux.Handle("GET /dashboard", a.authed(a.dashboard))
	mux.Handle("GET /api/analytics", a.authed(a.analytics))
	mux.Handle("GET /api/comments", a.authed(a.comments))
	mux.Handle("GET /export.csv", a.authed(a.exportCSV))

	// admin role required
	mux.Handle("GET /settings", a.adminOnly(a.settings))
	mux.Handle("GET /users", a.adminOnly(a.usersPage))
	mux.Handle("POST /users/invite", a.adminCSRF(a.createInvite))
	mux.Handle("POST /users/deactivate", a.adminCSRF(a.deactivate))
}

// ---- middleware composition helpers ----

func (a *Admin) authed(h http.HandlerFunc) http.Handler {
	return a.requireAuth(h)
}

func (a *Admin) authedCSRF(h http.HandlerFunc) http.Handler {
	return a.requireAuth(a.requireCSRF(h))
}

func (a *Admin) adminOnly(h http.HandlerFunc) http.Handler {
	return a.requireAuth(a.requireRole(RoleAdmin, h))
}

func (a *Admin) adminCSRF(h http.HandlerFunc) http.Handler {
	return a.requireAuth(a.requireRole(RoleAdmin, a.requireCSRF(h)))
}

// requireAuth loads the session/user into context, redirecting to /login if
// absent and to the password-change page while a forced change is pending.
func (a *Admin) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			redirectLogin(w, r)
			return
		}
		sess, user, err := lookupSession(a.db, c.Value)
		if err != nil {
			a.clearSessionCookie(w)
			redirectLogin(w, r)
			return
		}
		if user.MustChangePW && !passwordChangeAllowed(r.URL.Path) {
			http.Redirect(w, r, "/account/password", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = context.WithValue(ctx, sessKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Admin) requireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u == nil || (role == RoleAdmin && u.Role != RoleAdmin) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Admin) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := sessionFrom(r.Context())
		if s == nil || subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(s.CSRF)) != 1 {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func passwordChangeAllowed(path string) bool {
	return path == "/account/password" || path == "/logout"
}

func redirectLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *Admin) setSessionCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(a.sessionTTL.Seconds()),
	})
}

func (a *Admin) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// ---- per-username login throttle ----

type loginThrottle struct {
	mu    sync.Mutex
	fails map[string]*failRec
}

type failRec struct {
	count int
	until time.Time
}

const (
	throttleMax    = 5
	throttleWindow = 5 * time.Minute
)

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{fails: make(map[string]*failRec)}
}

func (t *loginThrottle) blocked(user string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.fails[user]
	return r != nil && r.count >= throttleMax && time.Now().Before(r.until)
}

func (t *loginThrottle) fail(user string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.fails[user]
	if r == nil {
		r = &failRec{}
		t.fails[user] = r
	}
	r.count++
	r.until = time.Now().Add(throttleWindow)
}

func (t *loginThrottle) reset(user string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, user)
}
