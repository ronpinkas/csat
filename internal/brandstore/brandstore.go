// Package brandstore persists per-tenant branding (site name, theme color) in a
// tenant database. Empty fields fall back to the deployment configuration, so a
// tenant that has set nothing looks exactly like the single-tenant default.
package brandstore

import (
	"database/sql"
	"errors"
	"net/url"
	"strconv"
)

// Branding is a tenant's resolved name + accent color.
type Branding struct {
	SiteName   string
	ThemeColor string
}

// Get returns the tenant's stored overrides, or nil if none are set.
func Get(db *sql.DB) (*Branding, error) {
	var name, color sql.NullString
	err := db.QueryRow(`SELECT site_name, theme_color FROM tenant_settings WHERE id = 1`).Scan(&name, &color)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Branding{SiteName: name.String, ThemeColor: color.String}, nil
}

// Save upserts the tenant's branding overrides.
func Save(db *sql.DB, name, color string, now int64) error {
	_, err := db.Exec(
		`INSERT INTO tenant_settings(id, site_name, theme_color, updated_at) VALUES(1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET site_name = excluded.site_name,
		   theme_color = excluded.theme_color, updated_at = excluded.updated_at`,
		name, color, now)
	return err
}

// SaveLogo upserts the tenant's logo blob (preserving name/color).
func SaveLogo(db *sql.DB, blob []byte, contentType string, now int64) error {
	_, err := db.Exec(
		`INSERT INTO tenant_settings(id, logo, logo_type, updated_at) VALUES(1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET logo = excluded.logo,
		   logo_type = excluded.logo_type, updated_at = excluded.updated_at`,
		blob, contentType, now)
	return err
}

// Logo returns the tenant's stored logo blob + content type, or ok=false if none.
func Logo(db *sql.DB) (blob []byte, contentType string, ok bool) {
	var (
		b []byte
		t sql.NullString
	)
	err := db.QueryRow(`SELECT logo, logo_type FROM tenant_settings WHERE id = 1 AND logo IS NOT NULL`).Scan(&b, &t)
	if err != nil || len(b) == 0 {
		return nil, "", false
	}
	return b, t.String, true
}

// LogoURL returns the logo URL for a tenant: a same-origin, ref-scoped,
// cache-busted /branding/logo when the tenant has uploaded one, otherwise the
// supplied fallback (the deployment logo URL, possibly empty).
func LogoURL(db *sql.DB, ref, fallback string) string {
	if db != nil {
		if v, has := LogoVersion(db); has {
			u := "/branding/logo?v=" + strconv.FormatInt(v, 10)
			if ref != "" {
				u += "&ref=" + url.QueryEscape(ref)
			}
			return u
		}
	}
	return fallback
}

// LogoVersion returns a cache-busting version (updated_at) and whether a logo is
// set, for building the logo URL.
func LogoVersion(db *sql.DB) (version int64, has bool) {
	var (
		v int64
		n sql.NullInt64
	)
	if err := db.QueryRow(
		`SELECT updated_at, length(logo) FROM tenant_settings WHERE id = 1`).Scan(&v, &n); err != nil {
		return 0, false
	}
	return v, n.Valid && n.Int64 > 0
}

// Resolve returns the effective branding: the tenant's overrides where set,
// otherwise the supplied deployment defaults. A nil db yields the defaults.
func Resolve(db *sql.DB, defName, defColor string) Branding {
	b := Branding{SiteName: defName, ThemeColor: defColor}
	if db == nil {
		return b
	}
	if got, err := Get(db); err == nil && got != nil {
		if got.SiteName != "" {
			b.SiteName = got.SiteName
		}
		if got.ThemeColor != "" {
			b.ThemeColor = got.ThemeColor
		}
	}
	return b
}
