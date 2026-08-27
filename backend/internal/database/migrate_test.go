package database

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsFromEmbeddedFS(t *testing.T) {
	loaded, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("no migrations were embedded")
	}

	first := loaded[0]
	if first.Version != 1 {
		t.Errorf("first migration version = %d, want 1", first.Version)
	}
	if first.Name != "initial_schema" {
		t.Errorf("first migration name = %q, want initial_schema", first.Name)
	}
	if first.Filename != "001_initial_schema.sql" {
		t.Errorf("first migration filename = %q", first.Filename)
	}
	if !strings.Contains(first.SQL, "CREATE TABLE users") {
		t.Error("001_initial_schema.sql should create the users table")
	}
	// Each migration runs inside a transaction opened by the migrator, so the
	// file must not manage transactions itself.
	for _, keyword := range []string{"BEGIN;", "COMMIT;", "ROLLBACK;"} {
		if strings.Contains(first.SQL, keyword) {
			t.Errorf("migration must not contain %q: the migrator owns the transaction", keyword)
		}
	}
}

// Every table named in architecture.md section 4 must exist in the initial
// migration. This catches a missing table without needing a live database.
func TestInitialMigrationCreatesEverySpecifiedTable(t *testing.T) {
	loaded, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}

	var all strings.Builder
	for _, m := range loaded {
		all.WriteString(m.SQL)
	}
	sql := all.String()

	tables := []string{
		"users", "whatsapp_accounts", "categories",
		"transactions", "whatsapp_messages", "refresh_tokens",
	}
	for _, table := range tables {
		if !strings.Contains(sql, "CREATE TABLE "+table+" (") {
			t.Errorf("migrations do not create table %q", table)
		}
	}
}

func TestMigrationsAreOrderedByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"003_third.sql":  {Data: []byte("SELECT 3;")},
		"001_first.sql":  {Data: []byte("SELECT 1;")},
		"002_second.sql": {Data: []byte("SELECT 2;")},
	}

	loaded, err := LoadMigrations(fsys)
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	for i, want := range []int{1, 2, 3} {
		if loaded[i].Version != want {
			t.Errorf("migration %d has version %d, want %d", i, loaded[i].Version, want)
		}
	}
}

func TestLoadMigrationsRejectsBadInput(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"unparsable filename": {
			"001_first.sql": {Data: []byte("SELECT 1;")},
			"initial.sql":   {Data: []byte("SELECT 2;")},
		},
		"duplicate version": {
			"001_first.sql":  {Data: []byte("SELECT 1;")},
			"001_second.sql": {Data: []byte("SELECT 2;")},
		},
		"version zero": {
			"000_zeroth.sql": {Data: []byte("SELECT 0;")},
		},
		"uppercase name": {
			"001_InitialSchema.sql": {Data: []byte("SELECT 1;")},
		},
		"no migrations": {
			"README.md": {Data: []byte("not sql")},
		},
	}

	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadMigrations(fsys); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// The checksum is what lets the migrator notice that an applied file was edited
// afterwards, so it must depend on the body and nothing else.
func TestChecksumIsStableAndContentDependent(t *testing.T) {
	a := Migration{Version: 1, Name: "x", SQL: "SELECT 1;"}
	b := Migration{Version: 9, Name: "y", SQL: "SELECT 1;"}
	c := Migration{Version: 1, Name: "x", SQL: "SELECT 2;"}

	if a.Checksum() != a.Checksum() {
		t.Error("Checksum is not stable across calls")
	}
	if a.Checksum() != b.Checksum() {
		t.Error("Checksum should depend only on the SQL body")
	}
	if a.Checksum() == c.Checksum() {
		t.Error("Checksum should change when the SQL changes")
	}
	if len(a.Checksum()) != 64 {
		t.Errorf("Checksum length = %d, want 64 hex characters", len(a.Checksum()))
	}
}

func TestMaxVersion(t *testing.T) {
	if got := maxVersion(nil); got != 0 {
		t.Errorf("maxVersion(nil) = %d, want 0", got)
	}
	got := maxVersion([]Migration{{Version: 2}, {Version: 7}, {Version: 5}})
	if got != 7 {
		t.Errorf("maxVersion = %d, want 7", got)
	}
}
