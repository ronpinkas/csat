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
// a phone number, order id, ticket id, …), subject time (UTC unix seconds), and
// language tag (e.g. "en", "es"). The subject and language must not contain the
// '|' separator; an empty language defaults to "en".
func Encrypt(secret, subject string, subjectTime int64, lang string) (string, error) {
	if strings.Contains(subject, "|") {
		return "", errors.New("subject must not contain '|'")
	}
	if strings.Contains(lang, "|") {
		return "", errors.New("language must not contain '|'")
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
	sealed := gcm.Seal(nonce, nonce, []byte(pt), nil) // result = nonce || ciphertext||tag
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt validates a token and returns the subject, subject time, and language.
// It returns ErrInvalidToken for anything that does not decrypt to the expected
// three-field payload.
func Decrypt(secret, tok string) (subject string, subjectTime int64, lang string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) < nonceSize {
		return "", 0, "", ErrInvalidToken
	}
	gcm, err := newGCM(secret)
	if err != nil {
		return "", 0, "", err
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", 0, "", ErrInvalidToken
	}
	parts := strings.Split(string(pt), "|")
	if len(parts) != 3 || parts[0] == "" {
		return "", 0, "", ErrInvalidToken
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, "", ErrInvalidToken
	}
	return parts[0], ts, parts[2], nil
}
