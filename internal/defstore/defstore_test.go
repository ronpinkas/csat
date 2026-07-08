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

// TestSetDefaultPinsAndStays: pinning makes an explicit default; it overrides
// "newest" and STAYS put when a newer survey is later published (the whole point
// of an explicit default). With nothing pinned, default == newest (back-compat).
func TestSetDefaultPinsAndStays(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	id1, _ := Add(database, surveydef.Default(), 1000)
	id2, _ := Add(database, surveydef.Default(), 2000)

	// No pin: default is the newest (Curaçao's single-survey behavior generalizes).
	if _, id, _ := Default(database); id != id2 {
		t.Fatalf("default with no pin = %d, want newest %d", id, id2)
	}

	// Pin the older one; Default + List both reflect it.
	if err := SetDefault(database, id1); err != nil {
		t.Fatal(err)
	}
	if _, id, _ := Default(database); id != id1 {
		t.Fatalf("default after pin = %d, want %d", id, id1)
	}
	vs, _ := List(database)
	for _, v := range vs {
		if v.IsDefault != (v.ID == id1) {
			t.Fatalf("IsDefault wrong for set %d: got %v", v.ID, v.IsDefault)
		}
	}

	// Publishing a newer survey does NOT steal the pin.
	id3, _ := Add(database, surveydef.Default(), 3000)
	if _, id, _ := Default(database); id != id1 {
		t.Fatalf("default after publishing newer = %d, want pinned %d (not %d)", id, id1, id3)
	}
}

// TestByNameFollowsLatest: ByName resolves a survey by its human name
// (case-insensitive) to the NEWEST set with that name, so a name reference
// follows re-publishes. An unknown name errors (callers fall back to default).
func TestByNameFollowsLatest(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	d1 := surveydef.Default()
	d1.Name = "Air Fryer Survey"
	Add(database, d1, 1000)
	d2 := surveydef.Default()
	d2.Name = "Air Fryer Survey" // re-published, same name -> supersedes
	id2, _ := Add(database, d2, 2000)
	other := surveydef.Default()
	other.Name = "NPS"
	Add(database, other, 3000)

	// Case-insensitive match resolves to the newest set with that name.
	if _, id, err := ByName(database, "air fryer survey"); err != nil || id != id2 {
		t.Fatalf("ByName -> id=%d err=%v, want newest %d", id, err, id2)
	}
	// Unknown name errors -> caller (resolveFormDef) falls back to the default.
	if _, _, err := ByName(database, "no-such-survey"); err == nil {
		t.Fatal("unknown name should error so the form falls back to default")
	}
}

// TestDeleteRemovesSetAndResponses: Delete removes the set plus every response
// answered under it (and their answers + used-token markers), leaving other
// sets untouched.
func TestDeleteRemovesSetAndResponses(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	id1, _ := Seed(database, surveydef.Default(), 1000)
	id2, _ := Add(database, surveydef.Default(), 2000)

	// A response answered under set 1, with an answer row and a used-token marker.
	res, err := database.Exec(
		`INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id) VALUES('sub', 42, 'en', 1000, ?)`, id1)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := res.LastInsertId()
	if _, err := database.Exec(`INSERT INTO answers(response_id, question_key, num) VALUES(?, 'csat', 5)`, rid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO used_tokens(subject, subject_time, used_at, response_id) VALUES('sub', 42, 1000, ?)`, rid); err != nil {
		t.Fatal(err)
	}

	removed, err := Delete(database, id1)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	// Set 1 is gone; set 2 remains.
	if _, err := ByID(database, id1); err == nil {
		t.Fatal("set 1 should be deleted")
	}
	if _, err := ByID(database, id2); err != nil {
		t.Fatalf("set 2 should remain: %v", err)
	}

	// Its responses, answers, and used-token markers are gone.
	count := func(q string, arg any) int {
		var n int
		if err := database.QueryRow(q, arg).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM responses WHERE definition_id = ?`, id1); n != 0 {
		t.Fatalf("responses remain: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM answers WHERE response_id = ?`, rid); n != 0 {
		t.Fatalf("answers remain: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM used_tokens WHERE response_id = ?`, rid); n != 0 {
		t.Fatalf("used_tokens remain: %d", n)
	}
	if n, _ := Count(database); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}
