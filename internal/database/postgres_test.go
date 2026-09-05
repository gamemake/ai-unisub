package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenSQLiteAppliesCurrentSchema(t *testing.T) {
	db, err := OpenSQLite(context.Background(), t.TempDir()+"/fresh.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"accounts", "api_keys", "users", "admin", "schema_migrations"} {
		exists, err := db.TableExists(context.Background(), table)
		if err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}
	id, err := db.InsertID(context.Background(), `INSERT INTO users(username, password_hash, role) VALUES(?,?,?)`, "alice", "hash", "admin")
	if err != nil || id == 0 {
		t.Fatalf("insert id=%d err=%v", id, err)
	}
}

func TestPostgresFreshSchema(t *testing.T) {
	db := openIsolatedPostgres(t)
	ctx := context.Background()
	for _, table := range []string{"accounts", "api_keys", "users", "admin", "schema_migrations"} {
		exists, err := db.TableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}
	id, err := db.InsertID(ctx, `INSERT INTO users(username, password_hash, role) VALUES(?,?,?)`, "alice", "hash", "admin")
	if err != nil || id == 0 {
		t.Fatalf("insert id=%d err=%v", id, err)
	}
	var username string
	if err := db.QueryRowContext(ctx, `SELECT username FROM users WHERE id=?`, id).Scan(&username); err != nil || username != "alice" {
		t.Fatalf("username=%q err=%v", username, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(username, password_hash, role) VALUES(?,?,?)`, "alice", "hash", "admin"); !IsUniqueViolation(err) {
		t.Fatalf("duplicate username error = %v", err)
	}
	var versions int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil || versions != 8 {
		t.Fatalf("schema versions=%d err=%v", versions, err)
	}
}

func openIsolatedPostgres(t *testing.T) *DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("UNISUB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set UNISUB_TEST_POSTGRES_DSN to run postgres tests")
	}
	schema := fmt.Sprintf("unisub_test_%d", time.Now().UnixNano())
	bootstrap, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Exec(`CREATE SCHEMA ` + schema); err != nil {
		bootstrap.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bootstrap.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = bootstrap.Close()
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := Open(context.Background(), string(PostgreSQL), dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
