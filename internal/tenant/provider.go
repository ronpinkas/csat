// Package tenant resolves a tenant "ref" to its database handle.
//
// In single-tenant mode the ref is ignored and the one configured database is
// always returned — byte-for-byte the historical behavior. In multi-tenant mode
// each ref maps to its own SQLite file under a data directory, opened and
// migrated lazily on first use and then cached for the process lifetime. Because
// every table (responses, users, sessions, invites, …) lives inside the tenant's
// own file, tenants are isolated by the file boundary with no schema changes.
package tenant

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/ronpinkas/csat/internal/db"
)

// ErrInvalidRef is returned when a ref cannot be turned into a safe DB filename.
var ErrInvalidRef = errors.New("invalid tenant ref")

// Provider resolves a ref to a *sql.DB.
type Provider interface {
	// DB returns the database for ref. In single mode the ref is ignored.
	DB(ref string) (*sql.DB, error)
	// Handles returns the databases currently open (for periodic sweeps). In
	// single mode that is the one database; in multi mode the tenant DBs opened
	// so far this process.
	Handles() []*sql.DB
	// Multi reports whether refs are meaningful (multi-tenant mode).
	Multi() bool
	// Close closes all open handles.
	Close() error
}

var refSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// SafeRef converts a ref into a safe filename component, or "" if it is empty or
// invalid. It replaces every character outside [A-Za-z0-9._-] with "_" and
// strips leading/trailing dots so a ref can never escape the data directory or
// name a hidden/"" file.
func SafeRef(ref string) string {
	s := refSanitizer.ReplaceAllString(ref, "_")
	s = strings.Trim(s, ".")
	if s == "" || len(s) > 200 {
		return ""
	}
	return s
}

// ---- single-tenant ----

type single struct{ db *sql.DB }

// NewSingle opens and migrates the one database at path.
func NewSingle(path string) (Provider, error) {
	d, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(d); err != nil {
		_ = d.Close()
		return nil, err
	}
	return &single{db: d}, nil
}

// WrapSingle adapts an already-open database into a single-tenant Provider
// (no open or migrate). Handy for tests that manage their own handle.
func WrapSingle(db *sql.DB) Provider { return &single{db: db} }

func (s *single) DB(string) (*sql.DB, error) { return s.db, nil }
func (s *single) Handles() []*sql.DB         { return []*sql.DB{s.db} }
func (s *single) Multi() bool                { return false }
func (s *single) Close() error               { return s.db.Close() }

// ---- multi-tenant ----

type multi struct {
	dir string
	mu  sync.Mutex
	dbs map[string]*sql.DB // sanitized ref -> handle
}

// NewMulti prepares a per-ref pool over dir. Tenant databases are opened (and
// migrated) lazily on first DB(ref).
func NewMulti(dir string) (Provider, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &multi{dir: dir, dbs: make(map[string]*sql.DB)}, nil
}

func (m *multi) DB(ref string) (*sql.DB, error) {
	key := SafeRef(ref)
	if key == "" {
		return nil, ErrInvalidRef
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.dbs[key]; ok {
		return d, nil
	}
	d, err := db.Open(filepath.Join(m.dir, key+".db"))
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(d); err != nil {
		_ = d.Close()
		return nil, err
	}
	m.dbs[key] = d
	return d, nil
}

func (m *multi) Handles() []*sql.DB {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*sql.DB, 0, len(m.dbs))
	for _, d := range m.dbs {
		out = append(out, d)
	}
	return out
}

func (m *multi) Multi() bool { return true }

func (m *multi) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.dbs {
		_ = d.Close()
	}
	m.dbs = map[string]*sql.DB{}
	return nil
}
