package defstore

import (
	"path/filepath"
	"testing"

	"github.com/ronpinkas/csat/internal/db"
	"github.com/ronpinkas/csat/internal/surveydef"
)

func TestSeedLatestAddByID(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	// Pre-existing response (simulates a single-tenant upgrade) — must be
	// backfilled to the first set by Seed.
	if _, err := database.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at) VALUES('x', 0, 'en', 0)`,
	); err != nil {
		t.Fatal(err)
	}

	def := surveydef.Default()
	id1, err := Seed(database, def, 1000)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if id1 != 1 {
		t.Fatalf("first set id = %d, want 1", id1)
	}

	// The pre-existing response was backfilled to the first set.
	var backfilled int64
	if err := database.QueryRow(`SELECT definition_id FROM responses LIMIT 1`).Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if backfilled != id1 {
		t.Fatalf("response backfill = %d, want %d", backfilled, id1)
	}

	// Seed is idempotent.
	if id, _ := Seed(database, def, 2000); id != id1 {
		t.Fatalf("re-seed should return %d, got %d", id1, id)
	}

	// Latest returns the first set.
	_, gotID, err := Latest(database)
	if err != nil || gotID != id1 {
		t.Fatalf("latest: id=%d err=%v", gotID, err)
	}

	// Add a new set -> becomes the latest; the old one stays addressable.
	edited := surveydef.Default()
	edited.Intro = map[string]string{"en": "Edited intro"}
	id2, err := Add(database, edited, 3000)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if id2 == id1 {
		t.Fatal("add should create a new set id")
	}
	_, latestID, _ := Latest(database)
	if latestID != id2 {
		t.Fatalf("latest should be %d after add, got %d", id2, latestID)
	}
	v1, err := ByID(database, id1)
	if err != nil || v1.IntroFor("en") == "Edited intro" {
		t.Fatalf("set 1 must stay immutable: %v", err)
	}
	v2, err := ByID(database, id2)
	if err != nil || v2.IntroFor("en") != "Edited intro" {
		t.Fatalf("set 2 should carry the edit, got %q err=%v", v2.IntroFor("en"), err)
	}

	// List newest-first; first entry is the latest.
	vs, err := List(database)
	if err != nil || len(vs) != 2 || vs[0].ID != id2 || !vs[0].Latest || vs[1].Latest {
		t.Fatalf("list: %+v err=%v", vs, err)
	}
}

func TestResolveSeedsWhenEmpty(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	d, id, err := Resolve(database, surveydef.Default(), 1)
	if err != nil || d == nil || id == 0 {
		t.Fatalf("resolve should seed: id=%d err=%v", id, err)
	}
	if _, gotID, _ := Latest(database); gotID != id {
		t.Fatal("resolve should have left a latest set")
	}
}
