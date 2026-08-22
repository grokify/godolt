package godolt

import (
	"fmt"
	"net/url"
	"strings"
)

// DSN helpers for go-sql-driver/mysql-style DSNs pointed at a dolt
// sql-server. Pure string manipulation — godolt still adds no driver
// dependency; the caller opens the *sql.DB.

// LocalDSN returns the conventional DSN for a local dolt sql-server:
// root with no password on 127.0.0.1.
func LocalDSN(port int, database string) string {
	return fmt.Sprintf("root:@tcp(127.0.0.1:%d)/%s", port, database)
}

// EnsureParseTime appends parseTime=true to a MySQL DSN if absent. ORMs
// such as Ent need it to scan DATETIME columns into time.Time.
func EnsureParseTime(dsn string) string {
	if strings.Contains(dsn, "parseTime=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "parseTime=true"
}

// SplitDSN splits a go-sql-driver DSN into the DSN without a database
// selected (still carrying any query parameters) and the database name.
// Use it to connect server-wide before the target database exists.
func SplitDSN(dsn string) (base string, database string, err error) {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return "", "", fmt.Errorf("godolt: DSN %q has no database segment", dsn)
	}
	rest := dsn[slash+1:]
	database = rest
	query := ""
	if q := strings.Index(rest, "?"); q >= 0 {
		database = rest[:q]
		query = rest[q:]
	}
	if database == "" {
		return "", "", fmt.Errorf("godolt: DSN %q has an empty database name", dsn)
	}
	if _, err := url.QueryUnescape(database); err != nil {
		return "", "", fmt.Errorf("godolt: DSN database name: %w", err)
	}
	return dsn[:slash+1] + query, database, nil
}
