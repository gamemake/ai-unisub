package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type DB struct {
	raw     *sql.DB
	Dialect Dialect
}

type Tx struct {
	raw     *sql.Tx
	Dialect Dialect
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (db *DB) SQL() *sql.DB { return db.raw }

func (db *DB) Close() error { return db.raw.Close() }

func (db *DB) PingContext(ctx context.Context) error { return db.raw.PingContext(ctx) }

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.raw.ExecContext(ctx, db.Dialect.Rebind(query), args...)
}

func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.raw.Exec(db.Dialect.Rebind(query), args...)
}

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.raw.QueryContext(ctx, db.Dialect.Rebind(query), args...)
}

func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.raw.Query(db.Dialect.Rebind(query), args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.raw.QueryRowContext(ctx, db.Dialect.Rebind(query), args...)
}

func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.raw.QueryRow(db.Dialect.Rebind(query), args...)
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.raw.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{raw: tx, Dialect: db.Dialect}, nil
}

func (db *DB) InsertID(ctx context.Context, query string, args ...any) (int64, error) {
	return insertID(ctx, db, query, args...)
}

func (db *DB) TableExists(ctx context.Context, name string) (bool, error) {
	return tableExists(ctx, db, db.Dialect, name)
}

func (db *DB) ListTablesLike(ctx context.Context, pattern string) ([]string, error) {
	return listTablesLike(ctx, db, db.Dialect, pattern)
}

func (db *DB) ColumnNames(ctx context.Context, table string) (map[string]bool, error) {
	return columnNames(ctx, db, db.Dialect, table)
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.raw.ExecContext(ctx, tx.Dialect.Rebind(query), args...)
}

func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.raw.Exec(tx.Dialect.Rebind(query), args...)
}

func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.raw.QueryContext(ctx, tx.Dialect.Rebind(query), args...)
}

func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.raw.QueryRowContext(ctx, tx.Dialect.Rebind(query), args...)
}

func (tx *Tx) InsertID(ctx context.Context, query string, args ...any) (int64, error) {
	return insertID(ctx, tx, query, args...)
}

func (tx *Tx) ColumnNames(ctx context.Context, table string) (map[string]bool, error) {
	return columnNames(ctx, tx, tx.Dialect, table)
}

func (tx *Tx) Commit() error { return tx.raw.Commit() }

func (tx *Tx) Rollback() error { return tx.raw.Rollback() }

func insertID(ctx context.Context, q rowQuerier, query string, args ...any) (int64, error) {
	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	if !strings.Contains(strings.ToUpper(query), " RETURNING ") {
		query += " RETURNING id"
	}
	var id int64
	if err := q.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}
