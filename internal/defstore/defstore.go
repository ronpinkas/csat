// Package defstore persists versioned survey definitions ("question sets") in a
// tenant database.
//
// Every set is permanent and independently usable: a survey link may name a set
// id, and blank links default to the latest set. Editing the survey adds a new
// set; older ones stay mintable and viewable. Each response records the set it
// was answered under (responses.definition_id), so analytics over any set are
// computed against exactly the questions its respondents saw.
package defstore

import (
	"database/sql"
	"errors"

	"github.com/ronpinkas/csat/internal/surveydef"
)

// ErrNoDefinition means the tenant has no stored set yet (pre-seed).
var ErrNoDefinition = errors.New("no survey definition")

// Version is metadata about one stored set.
type Version struct {
	ID        int64
	CreatedAt int64
	Name      string
	Latest    bool // newest by id
	IsDefault bool // the effective default (pinned set, else newest)
}

// Latest returns the newest set and its id (the default for blank survey links).
func Latest(db *sql.DB) (*surveydef.Definition, int64, error) {
	var (
		id int64
		js string
	)
	err := db.QueryRow(
		`SELECT id, json FROM survey_definitions ORDER BY id DESC LIMIT 1`,
	).Scan(&id, &js)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNoDefinition
	}
	if err != nil {
		return nil, 0, err
	}
	d, err := surveydef.Parse([]byte(js))
	return d, id, err
}

// Default returns the effective default set: the explicitly pinned one
// (is_default=1) if any, otherwise the newest. This is what blank links and the
// form/dashboard resolve to.
func Default(db *sql.DB) (*surveydef.Definition, int64, error) {
	var (
		id int64
		js string
	)
	err := db.QueryRow(
		`SELECT id, json FROM survey_definitions WHERE is_default = 1 ORDER BY id DESC LIMIT 1`,
	).Scan(&id, &js)
	if errors.Is(err, sql.ErrNoRows) {
		return Latest(db) // nothing pinned -> newest
	}
	if err != nil {
		return nil, 0, err
	}
	d, err := surveydef.Parse([]byte(js))
	return d, id, err
}

// SetDefault pins one set as the default, clearing any previous pin.
func SetDefault(db *sql.DB, id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE survey_definitions SET is_default = 0 WHERE is_default = 1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE survey_definitions SET is_default = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ByID returns the set for a specific id.
func ByID(db *sql.DB, id int64) (*surveydef.Definition, error) {
	var js string
	err := db.QueryRow(`SELECT json FROM survey_definitions WHERE id = ?`, id).Scan(&js)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoDefinition
	}
	if err != nil {
		return nil, err
	}
	return surveydef.Parse([]byte(js))
}

// ByName returns the NEWEST set whose name matches (case-insensitive). A link
// can target a survey by its human name and automatically follow re-publishes
// (a new version with the same name supersedes). ErrNoDefinition if no match.
func ByName(db *sql.DB, name string) (*surveydef.Definition, int64, error) {
	var (
		id int64
		js string
	)
	err := db.QueryRow(
		`SELECT id, json FROM survey_definitions WHERE name = ? COLLATE NOCASE ORDER BY id DESC LIMIT 1`,
		name,
	).Scan(&id, &js)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNoDefinition
	}
	if err != nil {
		return nil, 0, err
	}
	d, err := surveydef.Parse([]byte(js))
	return d, id, err
}

// List returns set metadata, newest first; the first entry is the latest.
func List(db *sql.DB) ([]Version, error) {
	rows, err := db.Query(`SELECT id, created_at, name, is_default FROM survey_definitions ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	var pinnedID int64
	for rows.Next() {
		var v Version
		var pinned int
		if err := rows.Scan(&v.ID, &v.CreatedAt, &v.Name, &pinned); err != nil {
			return nil, err
		}
		if pinned != 0 {
			pinnedID = v.ID
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		out[0].Latest = true
		// Effective default: the pinned set, else the newest.
		defaultID := pinnedID
		if defaultID == 0 {
			defaultID = out[0].ID
		}
		for i := range out {
			out[i].IsDefault = out[i].ID == defaultID
		}
	}
	return out, nil
}

// Add validates def and stores it as a new set, returning its id. The new set
// becomes the latest (the default for blank links); existing sets are untouched.
func Add(db *sql.DB, def *surveydef.Definition, now int64) (int64, error) {
	js, err := def.JSON()
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(
		`INSERT INTO survey_definitions(json, created_at, name) VALUES(?, ?, ?)`, string(js), now, def.Name)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// Seed inserts def as the first set when the tenant has none yet, and backfills
// existing responses to it. Idempotent: a no-op once a set exists. Returns the
// latest set id either way.
func Seed(db *sql.DB, def *surveydef.Definition, now int64) (int64, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM survey_definitions`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		var id int64
		err := db.QueryRow(`SELECT id FROM survey_definitions ORDER BY id DESC LIMIT 1`).Scan(&id)
		return id, err
	}
	js, err := def.JSON()
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT INTO survey_definitions(json, created_at, name) VALUES(?, ?, ?)`, string(js), now, def.Name)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	// Backfill pre-existing responses (single-tenant upgrades) to the first set.
	if _, err := tx.Exec(`UPDATE responses SET definition_id = ? WHERE definition_id IS NULL`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Count returns the number of stored sets.
func Count(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM survey_definitions`).Scan(&n)
	return n, err
}

// Delete removes a set and everything answered under it — its responses, their
// answers, and the used-token markers — in one transaction. Callers MUST enforce
// policy first (never delete the effective default or the last remaining set).
// Deletes are explicit (not relying on PRAGMA foreign_keys) and idempotent.
// Returns the number of responses removed.
func Delete(db *sql.DB, id int64) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	sub := `(SELECT id FROM responses WHERE definition_id = ?)`
	if _, err := tx.Exec(`DELETE FROM answers WHERE response_id IN `+sub, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM used_tokens WHERE response_id IN `+sub, id); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM responses WHERE definition_id = ?`, id)
	if err != nil {
		return 0, err
	}
	removed, _ := res.RowsAffected()
	if _, err := tx.Exec(`DELETE FROM survey_definitions WHERE id = ?`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}

// Resolve returns the latest set, seeding fallback as the first set if the
// tenant has none yet (so any access path — survey or admin — converges to a
// stored, backfilled set).
func Resolve(db *sql.DB, fallback *surveydef.Definition, now int64) (*surveydef.Definition, int64, error) {
	d, id, err := Default(db)
	if errors.Is(err, ErrNoDefinition) {
		id, serr := Seed(db, fallback, now)
		if serr != nil {
			return nil, 0, serr
		}
		return fallback, id, nil
	}
	return d, id, err
}
