package admin

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
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
	mux.Handle("GET /forgot", http.HandlerFunc(a.forgotForm))
	mux.Handle("POST /forgot", rl(http.HandlerFunc(a.forgot)))
	mux.Handle("GET /reset", http.HandlerFunc(a.resetForm))
	mux.Handle("POST /reset", rl(http.HandlerFunc(a.reset)))
	// platform tenant provisioning (token-signed; multi-tenant only)
	mux.Handle("POST /provision", rl(http.HandlerFunc(a.provision)))
	mux.Handle("GET /sso", rl(http.HandlerFunc(a.sso)))

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
	mux.Handle("POST /settings", a.adminCSRF(a.saveSettings))
	mux.Handle("GET /survey", a.adminOnly(a.surveyEditor))
	mux.Handle("POST /survey", a.adminCSRF(a.surveyPublish))
	mux.Handle("GET /users", a.adminOnly(a.usersPage))
	mux.Handle("POST /users/invite", a.adminCSRF(a.createInvite))
	mux.Handle("POST /users/deactivate", a.adminCSRF(a.deactivate))
	mux.Handle("POST /users/reset", a.adminCSRF(a.resetUser))
	mux.Handle("POST /users/delete", a.adminCSRF(a.deleteUser))
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
// absent and to the password-change page while a forced change is pending. The
// tenant ref is recovered from the session cookie, so authenticated navigation
// never needs ?ref in the URL.
func (a *Admin) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			a.redirectLogin(w, r, "")
			return
		}
		ref, raw := a.decodeSession(c.Value)
		db, err := a.provider.DB(ref)
		if err != nil {
			a.clearSessionCookie(w)
			a.redirectLogin(w, r, ref)
			return
		}
		sess, user, err := lookupSession(db, raw)
		if err != nil {
			a.clearSessionCookie(w)
			a.redirectLogin(w, r, ref)
			return
		}
		if user.MustChangePW && !passwordChangeAllowed(r.URL.Path) {
			http.Redirect(w, r, "/account/password", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = context.WithValue(ctx, sessKey, sess)
		ctx = context.WithValue(ctx, tdbKey, db)
		ctx = context.WithValue(ctx, refKey, ref)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// refFromRequest reads the tenant ref from ?ref= (or a ref form field) in
// multi-tenant mode; single-tenant mode always resolves to "".
func (a *Admin) refFromRequest(r *http.Request) string {
	if !a.provider.Multi() {
		return ""
	}
	return strings.TrimSpace(r.FormValue("ref"))
}

// tenantFor resolves the tenant database for a pre-session page (login, invite,
// forgot, reset). ok is false when the ref is invalid in multi-tenant mode.
func (a *Admin) tenantFor(r *http.Request) (db *sql.DB, ref string, ok bool) {
	ref = a.refFromRequest(r)
	db, err := a.provider.DB(ref)
	if err != nil {
		return nil, ref, false
	}
	return db, ref, true
}

// encodeSession/decodeSession bind the tenant ref to the session cookie in
// multi-tenant mode (single mode stores the raw token unchanged, so existing
// cookies keep working across the upgrade).
func (a *Admin) encodeSession(ref, raw string) string {
	if !a.provider.Multi() {
		return raw
	}
	return base64.RawURLEncoding.EncodeToString([]byte(ref)) + "." + raw
}

func (a *Admin) decodeSession(v string) (ref, raw string) {
	if !a.provider.Multi() {
		return "", v
	}
	i := strings.IndexByte(v, '.')
	if i < 0 {
		return "", v
	}
	b, err := base64.RawURLEncoding.DecodeString(v[:i])
	if err != nil {
		return "", v[i+1:]
	}
	return string(b), v[i+1:]
}

// withRef appends the tenant ref to a path's query (no-op when ref is empty).
func withRef(path, ref string) string {
	if ref == "" {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "ref=" + url.QueryEscape(ref)
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

func (a *Admin) redirectLogin(w http.ResponseWriter, r *http.Request, ref string) {
	http.Redirect(w, r, withRef("/login", ref), http.StatusSeeOther)
}

func (a *Admin) setSessionCookie(w http.ResponseWriter, ref, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    a.encodeSession(ref, raw),
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
