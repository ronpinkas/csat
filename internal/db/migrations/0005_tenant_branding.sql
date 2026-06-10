-- Per-tenant branding, edited in the admin Settings tab. A single row holds the
-- tenant's overrides; empty/missing values fall back to the deployment config
-- ([site].name, [branding].theme_color, the bundled logo file). One row per
-- tenant database. The logo is stored as a small image blob so it is served
-- same-origin and needs no filesystem.
CREATE TABLE IF NOT EXISTS tenant_settings (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  site_name   TEXT,
  theme_color TEXT,
  logo        BLOB,
  logo_type   TEXT,
  updated_at  INTEGER NOT NULL
);
