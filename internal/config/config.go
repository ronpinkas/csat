// Package config loads and validates the CSAT app configuration.
//
// Structural settings come from a TOML file; secret values may be written as
// "env:NAME" to pull from the environment instead. Environment values are
// sourced from a .env file (falling back to .env.example, then built-in
// defaults) so a fresh deploy runs with zero hand-editing.
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Site     Site     `toml:"site"`
	Server   Server   `toml:"server"`
	DB       DB       `toml:"db"`
	Tenancy  Tenancy  `toml:"tenancy"`
	Security Security `toml:"security"`
	Admin    Admin    `toml:"admin"`
	Survey   Survey   `toml:"survey"`
	Branding Branding `toml:"branding"`
}

// Tenancy selects single- vs multi-tenant operation.
//
// "single" (the default) keeps the historical behavior exactly: one database at
// db.path, no ref anywhere. "multi" gives each tenant ("ref") its own SQLite
// database under data_dir; the tenant is resolved per request — from the survey
// token for public survey pages, and from ?ref= (then the session cookie) for
// admin pages. The two modes are mutually exclusive per deployment.
type Tenancy struct {
	Mode    string `toml:"mode"`     // "single" (default) | "multi"
	DataDir string `toml:"data_dir"` // multi mode: dir holding one <ref>.db per tenant
}

// Multi reports whether multi-tenant mode is enabled.
func (t Tenancy) Multi() bool { return t.Mode == "multi" }

type Site struct {
	Name            string `toml:"name"`
	DisplayTimezone string `toml:"display_timezone"`
}

// Branding customizes the customer-facing look.
type Branding struct {
	LogoPath   string `toml:"logo_path"`   // optional explicit path override
	LogoDir    string `toml:"logo_dir"`    // dir to auto-detect logo.<ext> in (defaults to the config dir)
	ThemeColor string `toml:"theme_color"` // brand accent color, e.g. "#2563eb"
}

// logoExts lists accepted logo file extensions, in priority order.
var logoExts = []string{"svg", "png", "webp", "jpg", "jpeg", "gif", "bmp"}

// ResolveLogo returns the path to the logo image to serve, resolved at call time
// so dropping or replacing a file takes effect without a restart. An explicit
// logo_path wins (if it exists); otherwise a file named logo.<ext> in LogoDir is
// auto-detected. Returns "" when no logo is available.
func (b Branding) ResolveLogo() string {
	if b.LogoPath != "" {
		if isFile(b.LogoPath) {
			return b.LogoPath
		}
		return ""
	}
	if b.LogoDir == "" {
		return ""
	}
	for _, ext := range logoExts {
		p := filepath.Join(b.LogoDir, "logo."+ext)
		if isFile(p) {
			return p
		}
	}
	return ""
}

// LogoURL returns the served logo URL (with a content fingerprint so a replaced
// logo busts the browser cache), or "" when no logo is available.
func (b Branding) LogoURL() string {
	p := b.ResolveLogo()
	if p == "" {
		return ""
	}
	v := "1"
	if fi, err := os.Stat(p); err == nil {
		v = fmt.Sprintf("%x-%x", fi.ModTime().UnixNano(), fi.Size())
	}
	return "/branding/logo?v=" + v
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

type Server struct {
	ListenAddr     string   `toml:"listen_addr"`
	TrustProxy     bool     `toml:"trust_proxy"`
	TrustedProxies []string `toml:"trusted_proxies"`
	// SecureCookies forces the Secure flag on cookies even when this process does
	// not terminate TLS itself — set it when running behind a TLS-terminating
	// reverse proxy (e.g. Caddy in the appliance). Autocert mode implies it.
	SecureCookies bool `toml:"secure_cookies"`
	TLS           TLS  `toml:"tls"`
}

type TLS struct {
	Mode     string   `toml:"mode"` // "off" | "autocert"
	Domains  []string `toml:"domains"`
	CacheDir string   `toml:"cache_dir"`
	Email    string   `toml:"email"`
}

type DB struct {
	Path string `toml:"path"`
}

type Security struct {
	CryptoSecret    string `toml:"crypto_secret"`   // may be "env:NAME"; blank => auto-generate
	CryptoKeyPath   string `toml:"crypto_key_path"` // where an auto-generated key is persisted
	SessionTTLHours int    `toml:"session_ttl_hours"`
	InviteTTLHours  int    `toml:"invite_ttl_hours"`
}

type Admin struct {
	Username        string `toml:"username"`
	InitialPassword string `toml:"initial_password"` // may be "env:NAME"
}

type Survey struct {
	// Definition is the path to a survey.json describing the questions (types,
	// per-language labels, options). Empty uses the built-in default CSAT
	// instrument (see internal/surveydef). System strings (buttons, errors,
	// "thank you") come from the en/es catalog in internal/survey/i18n.go.
	Definition string `toml:"definition"`
}

// placeholder values that must never be accepted as real secrets.
var placeholderSecrets = map[string]bool{
	"":                                 true,
	"change-me":                        true,
	"change-me-on-first-login":         true,
	"CHANGE-ME-openssl-rand-base64-48": true,
	"env:CSAT_CRYPTO_SECRET":           true, // unresolved indirection
	"env:CSAT_ADMIN_INITIAL_PW":        true,
}

func defaults() Config {
	return Config{
		Site: Site{Name: "CSAT", DisplayTimezone: "UTC"},
		Server: Server{
			ListenAddr:     ":8080",
			TrustProxy:     false,
			TrustedProxies: []string{"127.0.0.1/32"},
			TLS:            TLS{Mode: "off"},
		},
		DB: DB{Path: "/var/lib/csat/csat.db"},
		Security: Security{
			CryptoKeyPath:   "/var/lib/csat/crypto.key",
			SessionTTLHours: 12,
			InviteTTLHours:  168,
		},
		Admin:    Admin{Username: "admin"},
		Branding: Branding{ThemeColor: "#2563eb"},
	}
}

// Load reads the TOML config at path (over the built-in defaults), seeds the
// process environment from a .env/.env.example file, then resolves any
// "env:NAME" indirections. A missing config file is allowed (defaults are used).
func Load(path string) (*Config, error) {
	loadDotEnvNear(path)

	cfg := defaults()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat config %s: %w", path, err)
		}
	}

	cfg.Security.CryptoSecret = resolveEnv(cfg.Security.CryptoSecret)
	cfg.Admin.InitialPassword = resolveEnv(cfg.Admin.InitialPassword)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resolveEnv expands an "env:NAME" reference to its environment value; any other
// value is returned unchanged.
func resolveEnv(v string) string {
	if name, ok := strings.CutPrefix(v, "env:"); ok {
		return os.Getenv(name)
	}
	return v
}

// loadDotEnvNear loads a .env file located next to the config file (falling back
// to .env.example in the same dir, then to the current working directory). Keys
// already present in the environment win, so systemd's EnvironmentFile or an
// explicit export always takes precedence.
func loadDotEnvNear(configPath string) {
	var dirs []string
	if configPath != "" {
		dirs = append(dirs, filepath.Dir(configPath))
	}
	dirs = append(dirs, ".")
	for _, dir := range dirs {
		for _, name := range []string{".env", ".env.example"} {
			p := filepath.Join(dir, name)
			if applyDotEnv(p) {
				return // first file found wins
			}
		}
	}
}

func applyDotEnv(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// strip an inline comment that is not inside quotes
		if !strings.HasPrefix(val, "\"") && !strings.HasPrefix(val, "'") {
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return true
}

// Validate checks structural sanity. It does NOT enforce secret strength; that
// is handled after secret resolution (see ResolveCryptoKey / the admin bootstrap)
// so that a blank crypto_secret can legitimately trigger auto-generation.
func (c *Config) Validate() error {
	if c.Site.DisplayTimezone == "" {
		c.Site.DisplayTimezone = "UTC"
	}
	if _, err := time.LoadLocation(c.Site.DisplayTimezone); err != nil {
		return fmt.Errorf("invalid display_timezone %q: %w", c.Site.DisplayTimezone, err)
	}
	if c.Server.ListenAddr == "" {
		return errors.New("server.listen_addr is required")
	}
	switch c.Server.TLS.Mode {
	case "", "off":
		c.Server.TLS.Mode = "off"
	case "autocert":
		if len(c.Server.TLS.Domains) == 0 {
			return errors.New("server.tls.domains required when mode=autocert")
		}
	default:
		return fmt.Errorf("invalid server.tls.mode %q (want off|autocert)", c.Server.TLS.Mode)
	}
	switch c.Tenancy.Mode {
	case "", "single":
		c.Tenancy.Mode = "single"
		if c.DB.Path == "" {
			return errors.New("db.path is required")
		}
	case "multi":
		if c.Tenancy.DataDir == "" {
			return errors.New("tenancy.data_dir is required when mode=multi")
		}
	default:
		return fmt.Errorf("invalid tenancy.mode %q (want single|multi)", c.Tenancy.Mode)
	}
	if c.Admin.Username == "" {
		return errors.New("admin.username is required")
	}
	if c.Security.SessionTTLHours <= 0 {
		c.Security.SessionTTLHours = 12
	}
	if c.Security.InviteTTLHours <= 0 {
		c.Security.InviteTTLHours = 168
	}
	if c.Security.CryptoKeyPath == "" {
		c.Security.CryptoKeyPath = "/var/lib/csat/crypto.key"
	}
	if c.Branding.ThemeColor == "" {
		c.Branding.ThemeColor = "#2563eb"
	}
	return nil
}

// Location returns the configured display timezone (already validated).
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Site.DisplayTimezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// ResolveCryptoKey returns the token secret as raw bytes. Resolution order:
//  1. an explicit, non-placeholder crypto_secret from config/env;
//  2. an existing key persisted at crypto_key_path;
//  3. a freshly generated 32-byte key, written to crypto_key_path (chmod 600).
//
// The returned bool reports whether a new key was generated this call (so the
// caller can surface it once at first boot).
func (c *Config) ResolveCryptoKey() (secret string, generated bool, err error) {
	if s := c.Security.CryptoSecret; !placeholderSecrets[s] {
		if len(s) < 32 {
			return "", false, fmt.Errorf("crypto_secret too short (%d bytes, need >= 32)", len(s))
		}
		return s, false, nil
	}
	// try the persisted keyfile
	if b, err := os.ReadFile(c.Security.CryptoKeyPath); err == nil {
		s := strings.TrimSpace(string(b))
		if len(s) >= 32 {
			return s, false, nil
		}
	}
	// generate a fresh key and persist it
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, fmt.Errorf("generate crypto key: %w", err)
	}
	s := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(c.Security.CryptoKeyPath), 0o700); err != nil {
		return "", false, fmt.Errorf("create key dir: %w", err)
	}
	if err := os.WriteFile(c.Security.CryptoKeyPath, []byte(s+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write crypto key: %w", err)
	}
	return s, true, nil
}
