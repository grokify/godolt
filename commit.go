package godolt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// CreateDatabase creates the named database if it does not exist. The
// caller supplies a *sql.DB connected server-wide (no database selected —
// see SplitDSN); godolt adds no driver dependency.
func CreateDatabase(ctx context.Context, db *sql.DB, name string) error {
	if name == "" {
		return fmt.Errorf("godolt: database name is required")
	}
	// Identifiers can't be parameterized; backticks are stripped to keep
	// the quoting sound. Names come from operator config, not untrusted
	// input.
	safe := strings.ReplaceAll(name, "`", "")
	if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+safe+"`"); err != nil {
		return fmt.Errorf("godolt: create database %s: %w", name, err)
	}
	return nil
}

// HasUncommittedChanges reports whether the working set has staged or
// unstaged changes (any rows in dolt_status).
func (c *Client) HasUncommittedChanges(ctx context.Context) (bool, error) {
	var count int
	if err := c.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM dolt_status").Scan(&count); err != nil {
		return false, fmt.Errorf("godolt: dolt_status: %w", err)
	}
	return count > 0, nil
}

// AddAll stages all working-set changes (CALL DOLT_ADD('.')).
func (c *Client) AddAll(ctx context.Context) error {
	_, err := c.call(ctx, "DOLT_ADD", ".")
	return err
}

// Commit commits staged changes (CALL DOLT_COMMIT('-m', message)) and
// returns the new commit hash. The message is passed as a bind parameter,
// so it may contain any characters. Committing with nothing staged
// returns an error from Dolt; use CommitAll for no-op-when-clean
// semantics.
func (c *Client) Commit(ctx context.Context, message string) (hash string, err error) {
	// DOLT_COMMIT returns a single hash column — a different shape from
	// the (status, message) rows call() handles — and the message must
	// support arbitrary text, so this binds a parameter instead of using
	// call()'s validated inlining.
	rows, err := c.DB.QueryContext(ctx, "CALL DOLT_COMMIT('-m', ?)", message)
	if err != nil {
		return "", fmt.Errorf("godolt: DOLT_COMMIT: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		if err := rows.Scan(&hash); err != nil {
			return "", fmt.Errorf("godolt: DOLT_COMMIT: scan result: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return hash, err
	}
	return hash, nil
}

// CommitAll stages and commits all changes with the given message,
// returning the new commit hash. A clean working set is a no-op returning
// ("", nil) — the pattern applications use to wrap sync runs in Dolt
// commits.
func (c *Client) CommitAll(ctx context.Context, message string) (hash string, err error) {
	dirty, err := c.HasUncommittedChanges(ctx)
	if err != nil {
		return "", err
	}
	if !dirty {
		return "", nil
	}
	if err := c.AddAll(ctx); err != nil {
		return "", err
	}
	return c.Commit(ctx, message)
}
