package tenant

import (
	"path/filepath"
	"testing"
)

func TestSafeRef(t *testing.T) {
	cases := map[string]string{
		"acme.com":      "acme.com",
		"ACME":          "ACME",
		"a/b":           "a_b",
		"../etc/passwd": "_etc_passwd",
		"..":            "",
		"":              "",
		".":             "",
		"a b":           "a_b",
	}
	for in, want := range cases {
		if got := SafeRef(in); got != want {
			t.Errorf("SafeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSingleProviderIgnoresRef(t *testing.T) {
	p, err := NewSingle(filepath.Join(t.TempDir(), "one.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Multi() {
		t.Fatal("single provider should report Multi()=false")
	}
	a, _ := p.DB("anything")
	b, _ := p.DB("something-else")
	if a != b {
		t.Fatal("single provider must return the same DB regardless of ref")
	}
	if len(p.Handles()) != 1 {
		t.Fatalf("single provider should have 1 handle, got %d", len(p.Handles()))
	}
}

func TestMultiProviderIsolatesTenants(t *testing.T) {
	p, err := NewMulti(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.Multi() {
		t.Fatal("multi provider should report Multi()=true")
	}

	acme, err := p.DB("acme.com")
	if err != nil {
		t.Fatal(err)
	}
	globex, err := p.DB("globex.com")
	if err != nil {
		t.Fatal(err)
	}
	if acme == globex {
		t.Fatal("distinct refs must map to distinct databases")
	}

	// A row written to one tenant must not be visible in the other.
	if _, err := acme.Exec(`INSERT INTO users(username, password_hash, role, created_at) VALUES('a','h','admin',0)`); err != nil {
		t.Fatalf("insert into acme: %v", err)
	}
	var n int
	if err := globex.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("globex should not see acme's users, got %d", n)
	}

	// Same ref returns the cached handle.
	again, _ := p.DB("acme.com")
	if again != acme {
		t.Fatal("same ref should return the cached handle")
	}

	// An invalid ref is rejected.
	if _, err := p.DB(".."); err == nil {
		t.Fatal("invalid ref should be rejected")
	}
}
