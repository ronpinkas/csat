-- 0001_init: core schema for the CSAT app.
-- All timestamps are UTC unix seconds.

CREATE TABLE responses (
  id           INTEGER PRIMARY KEY,
  caller_id    TEXT    NOT NULL,             -- E.164 phone, recovered from the encrypted token
  call_time    INTEGER NOT NULL,             -- unix seconds, recovered from the token
  csat         INTEGER NOT NULL,             -- overall satisfaction, 1..5
  resolution   TEXT    NOT NULL,             -- 'yes' | 'partial' | 'no'
  ces          INTEGER NOT NULL,             -- customer effort score, 1..7
  comment      TEXT    NOT NULL DEFAULT '',
  submitted_at INTEGER NOT NULL,             -- unix seconds, server clock
  CHECK (csat BETWEEN 1 AND 5),
  CHECK (ces  BETWEEN 1 AND 7),
  CHECK (resolution IN ('yes','partial','no'))
);

-- One-time enforcement: the unique identity of a survey link. A duplicate INSERT
-- (same caller at the same call time) fails the PRIMARY KEY and is treated as
-- "already submitted".
CREATE TABLE used_tokens (
  caller_id   TEXT    NOT NULL,
  call_time   INTEGER NOT NULL,
  used_at     INTEGER NOT NULL,
  response_id INTEGER REFERENCES responses(id),
  PRIMARY KEY (caller_id, call_time)
);

CREATE TABLE users (
  id             INTEGER PRIMARY KEY,
  username       TEXT    NOT NULL UNIQUE,
  password_hash  TEXT    NOT NULL DEFAULT '',  -- argon2id encoded string; '' until set
  role           TEXT    NOT NULL,             -- 'admin' | 'viewer'
  must_change_pw INTEGER NOT NULL DEFAULT 0,
  active         INTEGER NOT NULL DEFAULT 1,
  created_at     INTEGER NOT NULL,
  last_login_at  INTEGER,
  CHECK (role IN ('admin','viewer'))
);

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,             -- sha256(raw session token), hex
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT    NOT NULL,                -- per-session CSRF secret
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE invites (
  id               INTEGER PRIMARY KEY,
  token_hash       TEXT    NOT NULL UNIQUE,   -- sha256(raw invite token), hex
  role             TEXT    NOT NULL,          -- role to grant
  username         TEXT,                      -- optional pre-fill
  created_by       INTEGER NOT NULL REFERENCES users(id),
  created_at       INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,
  redeemed_at      INTEGER,
  redeemed_user_id INTEGER REFERENCES users(id),
  CHECK (role IN ('admin','viewer'))
);
