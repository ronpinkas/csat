-- 0003_password_reset: self-service "forgot password" requests + admin-issued
-- one-time reset links. Unlike invites (which INSERT a new user), redeeming a
-- reset UPDATEs an existing user's password in place.

-- A pending "I forgot my password" request the user files from the login page.
-- NULL = no outstanding request. Set when the user asks; cleared when an admin
-- issues a reset link or the reset completes.
ALTER TABLE users ADD COLUMN reset_requested_at INTEGER;

-- One-time, admin-issued password reset tokens, scoped to an existing user.
CREATE TABLE password_resets (
  id          INTEGER PRIMARY KEY,
  token_hash  TEXT    NOT NULL UNIQUE,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL, -- admin who issued
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  used_at     INTEGER
);

CREATE INDEX idx_password_resets_expires ON password_resets(expires_at);
CREATE INDEX idx_password_resets_user    ON password_resets(user_id);
