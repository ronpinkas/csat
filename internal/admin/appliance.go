package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ronpinkas/csat/internal/token"
)

// secContext is the trusted security context the platform stamps as field 0 of
// an appliance token ("SEC|payload"). The appliance trusts ONLY this — never the
// payload — for tenant, identity, and role.
type secContext struct {
	Ref  string `json:"ref"`
	User string `json:"user"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

var errBadAppliance = errors.New("invalid appliance token")

// parseAppliance verifies a platform appliance token and returns its trusted SEC
// context plus the remaining (untrusted) payload. The plaintext is
// "<sec-json>|<payload>"; SEC is JSON (never contains '|'), so it is everything
// before the first '|'.
func parseAppliance(secret, tok string) (secContext, string, error) {
	pt, err := token.DecryptRaw(secret, tok)
	if err != nil {
		return secContext{}, "", errBadAppliance
	}
	secJSON, payload, _ := strings.Cut(pt, "|")
	var sec secContext
	if err := json.Unmarshal([]byte(secJSON), &sec); err != nil || sec.Ref == "" {
		return secContext{}, "", errBadAppliance
	}
	if sec.Exp != 0 && time.Now().Unix() > sec.Exp {
		return secContext{}, "", errBadAppliance
	}
	return sec, payload, nil
}

// MintApplianceToken builds a platform appliance token (SEC|payload) — used by
// the -mint-tenant CLI. The platform's signer mints the identical shape in Node.
func MintApplianceToken(secret, ref, user, role, payload string, ttl time.Duration) (string, error) {
	b, _ := json.Marshal(secContext{Ref: ref, User: user, Role: role, Exp: time.Now().Add(ttl).Unix()})
	return token.SignRaw(secret, string(b)+"|"+payload)
}
