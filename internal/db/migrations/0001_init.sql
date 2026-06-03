-- 0001_init: core schema for the (generic) survey app.
-- All timestamps are UTC unix seconds.

-- One submitted survey. `subject` is the generic entity the survey is about
-- (a phone number, order id, ticket id, …), recovered from the link token.
CREATE TABLE responses (
  id           INTEGER PRIMARY KEY,
  subject      TEXT    NOT NULL,
  subject_time INTEGER NOT NULL,
  lang         TEXT    NOT NULL DEFAULT 'en',
  submitted_at INTEGER NOT NULL
);

-- One answer per (response, question). Numeric question types store `num`;
-- choice/text store `text`. A multichoice question stores several rows.
CREATE TABLE answers (
  id           INTEGER PRIMARY KEY,
  response_id  INTEGER NOT NULL REFERENCES responses(id) ON DELETE CASCADE,
  question_key TEXT    NOT NULL,
  num          INTEGER,
  text         TEXT
);

-- One-time enforcement: the unique identity of a survey link. A duplicate
-- INSERT (same subject at the same subject_time) fails the PRIMARY KEY and is
-- treated as "already submitted".
CREATE TABLE used_tokens (
  subject      TEXT    NOT NULL,
  subject_time INTEGER NOT NULL,
  used_at      INTEGER NOT NULL,
  response_id  INTEGER REFERENCES responses(id),
  PRIMARY KEY (subject, subject_time)
);

CREATE TABLE users (
  id             INTEGER PRIMARY KEY,
  username       TEXT    NOT NULL UNIQUE,
  password_hash  TEXT    NOT NULL DEFAULT '',
  role           TEXT    NOT NULL,
  must_change_pw INTEGER NOT NULL DEFAULT 0,
  active         INTEGER NOT NULL DEFAULT 1,
  created_at     INTEGER NOT NULL,
  last_login_at  INTEGER,
  CHECK (role IN ('admin','viewer'))
);

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE invites (
  id               INTEGER PRIMARY KEY,
  token_hash       TEXT    NOT NULL UNIQUE,
  role             TEXT    NOT NULL,
  username         TEXT,
  created_by       INTEGER NOT NULL REFERENCES users(id),
  created_at       INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,
  redeemed_at      INTEGER,
  redeemed_user_id INTEGER REFERENCES users(id),
  CHECK (role IN ('admin','viewer'))
);
