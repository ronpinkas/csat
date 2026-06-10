package brandstore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ronpinkas/csat/internal/db"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestResolveFallsBackToDefaults(t *testing.T) {
	d := openDB(t)
	b := Resolve(d, "Platform", "#000000")
	if b.SiteName != "Platform" || b.ThemeColor != "#000000" {
		t.Fatalf("empty tenant should yield defaults, got %+v", b)
	}
}

func TestSaveAndResolve(t *testing.T) {
	d := openDB(t)
	if err := Save(d, "Acme", "#ff0000", 100); err != nil {
		t.Fatal(err)
	}
	b := Resolve(d, "Platform", "#000000")
	if b.SiteName != "Acme" || b.ThemeColor != "#ff0000" {
		t.Fatalf("tenant overrides not applied: %+v", b)
	}
	// Empty fields fall back to defaults even when a row exists.
	if err := Save(d, "", "", 200); err != nil {
		t.Fatal(err)
	}
	b = Resolve(d, "Platform", "#000000")
	if b.SiteName != "Platform" || b.ThemeColor != "#000000" {
		t.Fatalf("empty overrides should fall back: %+v", b)
	}
}

func TestLogoStoreAndNonClobber(t *testing.T) {
	d := openDB(t)
	// Name/color first.
	if err := Save(d, "Acme", "#ff0000", 100); err != nil {
		t.Fatal(err)
	}
	// Saving a logo must NOT clobber name/color.
	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := SaveLogo(d, png, "image/png", 150); err != nil {
		t.Fatal(err)
	}
	b := Resolve(d, "Platform", "#000")
	if b.SiteName != "Acme" || b.ThemeColor != "#ff0000" {
		t.Fatalf("SaveLogo clobbered name/color: %+v", b)
	}
	blob, ctype, ok := Logo(d)
	if !ok || ctype != "image/png" || len(blob) != len(png) {
		t.Fatalf("logo not stored: ok=%v ctype=%q len=%d", ok, ctype, len(blob))
	}
	if _, has := LogoVersion(d); !has {
		t.Fatal("LogoVersion should report a logo")
	}
	// LogoURL is ref-scoped + cache-busted.
	u := LogoURL(d, "acme.com", "/fallback")
	if !strings.Contains(u, "/branding/logo?v=") || !strings.Contains(u, "ref=acme.com") {
		t.Fatalf("unexpected logo url: %s", u)
	}

	// Saving name/color again must NOT clobber the logo.
	if err := Save(d, "Acme2", "#00ff00", 200); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Logo(d); !ok {
		t.Fatal("Save clobbered the logo")
	}
}

func TestLogoURLFallback(t *testing.T) {
	d := openDB(t)
	if got := LogoURL(d, "acme", "/deployment-logo"); got != "/deployment-logo" {
		t.Fatalf("no tenant logo should yield fallback, got %s", got)
	}
	if got := LogoURL(nil, "", "/x"); got != "/x" {
		t.Fatalf("nil db should yield fallback, got %s", got)
	}
}
