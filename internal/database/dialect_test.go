package database

import (
	"strings"
	"testing"
)

func TestParseDriver(t *testing.T) {
	cases := []struct {
		in   string
		want Dialect
	}{
		{"", SQLite},
		{"sqlite", SQLite},
		{"SQLite3", SQLite},
		{"postgres", PostgreSQL},
		{"PostgreSQL", PostgreSQL},
		{"pg", PostgreSQL},
	}
	for _, tc := range cases {
		got, err := ParseDriver(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseDriver(%q)=%q err=%v, want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseDriver("mysql"); err == nil {
		t.Fatal("mysql driver was accepted")
	}
}

func TestPostgresRebind(t *testing.T) {
	query := PostgreSQL.Rebind(`INSERT INTO users(username, password_hash, role) VALUES(?,?,?) RETURNING id`)
	want := `INSERT INTO users(username, password_hash, role) VALUES($1,$2,$3) RETURNING id`
	if query != want {
		t.Fatalf("rebind = %q", query)
	}
	if SQLite.Rebind(`SELECT id FROM users WHERE id=?`) != `SELECT id FROM users WHERE id=?` {
		t.Fatal("sqlite rebind changed placeholders")
	}
}

func TestCurrentSchemaUsesDialectTypes(t *testing.T) {
	pg := strings.Join(currentSchema(PostgreSQL), "\n")
	if !strings.Contains(pg, "BIGSERIAL PRIMARY KEY") || !strings.Contains(pg, "BYTEA") {
		t.Fatal("postgres schema is missing BIGSERIAL/BYTEA")
	}
	sqlite := strings.Join(currentSchema(SQLite), "\n")
	if !strings.Contains(sqlite, "INTEGER PRIMARY KEY AUTOINCREMENT") || !strings.Contains(sqlite, "BLOB") {
		t.Fatal("sqlite schema is missing AUTOINCREMENT/BLOB")
	}
}
