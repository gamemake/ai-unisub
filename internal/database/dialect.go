package database

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Dialect string

const (
	SQLite     Dialect = "sqlite"
	PostgreSQL Dialect = "postgres"
)

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ParseDriver(driver string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return SQLite, nil
	case "postgres", "postgresql", "pg":
		return PostgreSQL, nil
	default:
		return "", fmt.Errorf("unsupported database driver %q (want sqlite or postgres)", driver)
	}
}

func (d Dialect) Rebind(query string) string {
	if d != PostgreSQL {
		return query
	}
	n := 0
	var b strings.Builder
	b.Grow(len(query) + 16)
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

func (d Dialect) LikeOperator() string {
	if d == PostgreSQL {
		return "ILIKE"
	}
	return "LIKE"
}

func (d Dialect) SerialPrimaryKey() string {
	if d == PostgreSQL {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

func (d Dialect) BlobType() string {
	if d == PostgreSQL {
		return "BYTEA"
	}
	return "BLOB"
}

func (d Dialect) BigIntType() string {
	if d == PostgreSQL {
		return "BIGINT"
	}
	return "INTEGER"
}

func validIdent(name string) bool {
	return identPattern.MatchString(name)
}
