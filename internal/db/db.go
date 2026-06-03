// Package db opens the SQLite database (pure-Go driver) and runs migrations.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens (creating if needed) the SQLite database at path with WAL mode and
// sane pragmas, and verifies connectivity.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	// modernc.org/sqlite applies `_pragma=foo(bar)` query params on each new
	// connection. WAL allows concurrent readers with a single writer.
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + path + "?" + q.Encode()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Modest pool: WAL serializes writers, busy_timeout absorbs contention.
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return sqlDB, nil
}
