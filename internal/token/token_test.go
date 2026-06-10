package token

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testSecret = "test-secret-at-least-32-bytes-long-xxxxx"

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		cid  string
		ts   int64
		lang string
		ref  string
	}{
		{"+15551234567", 1717286400, "en", ""},         // legacy 3-field
		{"+5999123456", 0, "es", ""},                   // legacy 3-field
		{"anonymous", 9999999999, "en", ""},            // legacy 3-field
		{"+15551234567", 1717286400, "en", "acme.com"}, // 4-field, tenant-scoped
		{"order-42", 1717286400, "es", "globex.io"},    // 4-field, tenant-scoped
	}
	for _, c := range cases {
		tok, err := Encrypt(testSecret, c.cid, c.ts, c.lang, c.ref)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", c.cid, err)
		}
		gotCID, gotTS, gotLang, gotRef, err := Decrypt(testSecret, tok)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", c.cid, err)
		}
		if gotCID != c.cid || gotTS != c.ts || gotLang != c.lang || gotRef != c.ref {
			t.Fatalf("round trip mismatch: got (%q,%d,%q,%q) want (%q,%d,%q,%q)",
				gotCID, gotTS, gotLang, gotRef, c.cid, c.ts, c.lang, c.ref)
		}
	}
}

// TestRefIsOmittedWhenEmpty proves an empty ref produces the historical
// three-field token (so existing single-tenant links stay byte-compatible).
func TestRefIsOmittedWhenEmpty(t *testing.T) {
	tok, _ := Encrypt(testSecret, "+15551234567", 1717286400, "en", "")
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	gcm, _ := newGCM(testSecret)
	pt, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(pt), "|"); got != 2 {
		t.Fatalf("empty ref should yield a 3-field payload (2 separators), got %d: %q", got, pt)
	}
}

func TestNonceIsRandom(t *testing.T) {
	a, _ := Encrypt(testSecret, "+15551234567", 1717286400, "en", "")
	b, _ := Encrypt(testSecret, "+15551234567", 1717286400, "en", "")
	if a == b {
		t.Fatal("two tokens for the same input were identical; nonce not random")
	}
}

func TestTamperFails(t *testing.T) {
	tok, _ := Encrypt(testSecret, "+15551234567", 1717286400, "en", "")
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	raw[nonceSize+1] ^= 0x01
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	if _, _, _, _, err := Decrypt(testSecret, tampered); err != ErrInvalidToken {
		t.Fatalf("tampered token: want ErrInvalidToken, got %v", err)
	}
}

func TestWrongSecretFails(t *testing.T) {
	tok, _ := Encrypt(testSecret, "+15551234567", 1717286400, "es", "acme.com")
	if _, _, _, _, err := Decrypt("a-completely-different-secret-key-32bytes", tok); err != ErrInvalidToken {
		t.Fatalf("wrong secret: want ErrInvalidToken, got %v", err)
	}
}

func TestMalformedInputs(t *testing.T) {
	for _, bad := range []string{"", "!!!not base64!!!", "short", base64.RawURLEncoding.EncodeToString([]byte("tooshort"))} {
		if _, _, _, _, err := Decrypt(testSecret, bad); err != ErrInvalidToken {
			t.Fatalf("Decrypt(%q): want ErrInvalidToken, got %v", bad, err)
		}
	}
}

func TestSeparatorInFieldsRejected(t *testing.T) {
	if _, err := Encrypt(testSecret, "+1555|injected", 1, "en", ""); err == nil {
		t.Fatal("expected error encrypting caller id containing '|'")
	}
	if _, err := Encrypt(testSecret, "+15551234567", 1, "e|n", ""); err == nil {
		t.Fatal("expected error encrypting language containing '|'")
	}
	if _, err := Encrypt(testSecret, "+15551234567", 1, "en", "ac|me"); err == nil {
		t.Fatal("expected error encrypting ref containing '|'")
	}
}

func TestEmptyCallerIDInPayloadRejected(t *testing.T) {
	gcm, _ := newGCM(testSecret)
	nonce := make([]byte, nonceSize)
	sealed := gcm.Seal(nonce, nonce, []byte("|123|en"), nil)
	tok := base64.RawURLEncoding.EncodeToString(sealed)
	if _, _, _, _, err := Decrypt(testSecret, tok); err != ErrInvalidToken {
		t.Fatalf("empty caller id: want ErrInvalidToken, got %v", err)
	}
}

func TestEmptyLangDefaultsToEN(t *testing.T) {
	tok, _ := Encrypt(testSecret, "+15551234567", 1717286400, "", "")
	_, _, lang, _, err := Decrypt(testSecret, tok)
	if err != nil || lang != "en" {
		t.Fatalf("empty lang should default to en: got %q err=%v", lang, err)
	}
}

func TestTwoFieldTokenRejected(t *testing.T) {
	// A malformed 2-field payload (no language) is rejected.
	gcm, _ := newGCM(testSecret)
	nonce := make([]byte, nonceSize)
	sealed := gcm.Seal(nonce, nonce, []byte("+15551234567|123"), nil)
	tok := base64.RawURLEncoding.EncodeToString(sealed)
	if _, _, _, _, err := Decrypt(testSecret, tok); err != ErrInvalidToken {
		t.Fatalf("2-field token: want ErrInvalidToken, got %v", err)
	}
}

func TestFiveFieldTokenRejected(t *testing.T) {
	// More than four fields is rejected.
	gcm, _ := newGCM(testSecret)
	nonce := make([]byte, nonceSize)
	sealed := gcm.Seal(nonce, nonce, []byte("+15551234567|123|en|acme|extra"), nil)
	tok := base64.RawURLEncoding.EncodeToString(sealed)
	if _, _, _, _, err := Decrypt(testSecret, tok); err != ErrInvalidToken {
		t.Fatalf("5-field token: want ErrInvalidToken, got %v", err)
	}
}

func TestKeyDerivationStable(t *testing.T) {
	if strings.Contains(testSecret, "|") {
		t.Skip("secret contains separator")
	}
	if len(deriveKey(testSecret)) != 32 {
		t.Fatal("derived key is not 32 bytes")
	}
}
