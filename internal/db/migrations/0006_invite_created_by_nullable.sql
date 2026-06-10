-- Make invites.created_by nullable so the platform can mint admin invites for a
-- brand-new tenant that has no creator user yet (tenant provisioning). SQLite
-- can't relax a column constraint in place, so rebuild the table.
CREATE TABLE invites_new (
  id               INTEGER PRIMARY KEY,
  token_hash       TEXT    NOT NULL UNIQUE,
  role             TEXT    NOT NULL,
  username         TEXT,
  created_by       INTEGER REFERENCES users(id),
  created_at       INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,
  redeemed_at      INTEGER,
  redeemed_user_id INTEGER REFERENCES users(id),
  CHECK (role IN ('admin','viewer'))
);
INSERT INTO invites_new (id, token_hash, role, username, created_by, created_at, expires_at, redeemed_at, redeemed_user_id)
  SELECT id, token_hash, role, username, created_by, created_at, expires_at, redeemed_at, redeemed_user_id FROM invites;
DROP TABLE invites;
ALTER TABLE invites_new RENAME TO invites;
CREATE INDEX idx_invites_expires ON invites(expires_at);
