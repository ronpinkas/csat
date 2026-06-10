// Package token implements the opaque, encrypted survey-link token.
//
// A token is AES-256-GCM(key, "callerID|callTime") where key = SHA-256(secret).
// The ciphertext (with a fresh random 12-byte nonce prepended) is base64url
// encoded. This hides the caller id and call time inside the URL and is
// self-authenticating: any tampering makes decryption fail, so no separate HMAC
// is needed.
//
// The platform's SMS link-builder MUST construct tokens with the identical
// scheme and the per-customer shared secret. See Encrypt for the canonical
// recipe (and token_test.go for golden vectors).
package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// nonceSize is the standard GCM nonce length.
const nonceSize = 12

// ErrInvalidToken is returned for any malformed, tampered, or undecryptable
// token. Callers should surface a single generic message and never reveal which
// stage failed.
var ErrInvalidToken = errors.New("invalid token")

// deriveKey turns an arbitrary-length secret into a 32-byte AES-256 key.
func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func newGCM(secret string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt builds a token for the given subject (the entity the survey is about —
// a phone number, order id, ticket id, …), subject time (UTC unix seconds),
// language tag (e.g. "en", "es"), and tenant ref. The subject, language, and ref
// must not contain the '|' separator; an empty language defaults to "en".
//
// The ref binds the token to a tenant in multi-tenant mode. An empty ref omits
// the field entirely, producing the historical three-field token — so single-
// tenant links (and any links already in the wild) are byte-for-byte unchanged.
func Encrypt(secret, subject string, subjectTime int64, lang, ref string) (string, error) {
	if strings.Contains(subject, "|") {
		return "", errors.New("subject must not contain '|'")
	}
	if strings.Contains(lang, "|") {
		return "", errors.New("language must not contain '|'")
	}
	if strings.Contains(ref, "|") {
		return "", errors.New("ref must not contain '|'")
	}
	if lang == "" {
		lang = "en"
	}
	gcm, err := newGCM(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	pt := subject + "|" + strconv.FormatInt(subjectTime, 10) + "|" + lang
	if ref != "" {
		pt += "|" + ref
	}
	sealed := gcm.Seal(nonce, nonce, []byte(pt), nil) // result = nonce || ciphertext||tag
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt validates a token and returns the subject, subject time, language, and
// tenant ref. A three-field (legacy/single-tenant) token decrypts with ref="".
// It returns ErrInvalidToken for anything that does not decrypt to a valid
// three- or four-field payload.
func Decrypt(secret, tok string) (subject string, subjectTime int64, lang, ref string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) < nonceSize {
		return "", 0, "", "", ErrInvalidToken
	}
	gcm, err := newGCM(secret)
	if err != nil {
		return "", 0, "", "", err
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", 0, "", "", ErrInvalidToken
	}
	parts := strings.Split(string(pt), "|")
	if len(parts) < 3 || len(parts) > 4 || parts[0] == "" {
		return "", 0, "", "", ErrInvalidToken
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, "", "", ErrInvalidToken
	}
	if len(parts) == 4 {
		ref = parts[3]
	}
	return parts[0], ts, parts[2], ref, nil
}
