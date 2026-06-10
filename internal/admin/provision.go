package admin

import (
	"net/http"
	"time"

	"github.com/ronpinkas/csat/internal/defstore"
	"github.com/ronpinkas/csat/internal/token"
)

// ProvisionSubject is the reserved token subject that marks a tenant-provisioning
// token (vs. a survey token). subjectTime carries the not-after expiry and ref
// the tenant to create. (Restricting which tenant user this is for is a platform
// policy; csat just returns an unconditional admin invite.)
const ProvisionSubject = "__provision__"

// provision creates (or ensures) a tenant from a platform-signed token and
// returns an admin invite link as JSON. Multi-tenant only; the token must be
// signed with the deployment crypto secret, so only the platform (which shares
// that secret) can call it. The returned invite creates the tenant's first
// admin — no shared password is involved.
func (a *Admin) provision(w http.ResponseWriter, r *http.Request) {
	if !a.provider.Multi() {
		http.Error(w, "provisioning is only available in multi-tenant mode", http.StatusBadRequest)
		return
	}
	tok := r.URL.Query().Get("t")
	if tok == "" {
		_ = r.ParseForm()
		tok = r.PostFormValue("t")
	}
	subject, expiry, _, ref, err := token.Decrypt(a.secret, tok)
	if err != nil || subject != ProvisionSubject || ref == "" {
		http.Error(w, "invalid provisioning token", http.StatusForbidden)
		return
	}
	if expiry != 0 && time.Now().Unix() > expiry {
		http.Error(w, "provisioning token expired", http.StatusForbidden)
		return
	}

	db, err := a.provider.DB(ref)
	if err != nil {
		http.Error(w, "invalid tenant ref", http.StatusBadRequest)
		return
	}
	// Seed the tenant's question set (branding seeds lazily). Deliberately do NOT
	// auto-seed an admin user — the returned invite creates (or reclaims) one.
	if _, err := defstore.Seed(db, a.def, time.Now().Unix()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Mint a fresh, unconditional admin invite — repeatable even when the tenant
	// already has admins. The redeemer enters their email/username + a password:
	// a new username creates the admin; an existing one reclaims that account
	// (acts as a password reset). This is the platform's break-glass for an admin
	// who lost their password with no other admin to issue a reset.
	raw, err := createInviteRow(db, RoleAdmin, "", 0, a.inviteTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	link := requestBaseURL(a, r) + withRef("/invite?t="+raw, ref)
	writeJSON(w, map[string]any{"ref": ref, "invite_url": link})
}
