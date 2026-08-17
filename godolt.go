// Package godolt wraps Dolt's operational surface for Go programs,
// following the gogit precedent (a standalone service-integration module).
//
// Design: SQL-first. The primary workload is many concurrent sessions
// against `dolt sql-server` (the embedded driver sustains only one stable
// connection and is not a target), so version-control verbs run as Dolt
// stored procedures over the same MySQL wire as queries — the caller
// supplies the *sql.DB and godolt adds zero driver dependencies. The few
// operations that cannot run over the wire (clone/init bootstrap, server
// lifecycle) shell out to the dolt CLI (exec.go).
package godolt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Client executes Dolt operations over an existing SQL connection to a
// dolt sql-server. The caller owns the *sql.DB (driver, pooling, DSN).
type Client struct {
	DB *sql.DB
}

// New returns a Client over db.
func New(db *sql.DB) *Client {
	return &Client{DB: db}
}

// Remote is a configured Dolt remote.
type Remote struct {
	Name string
	URL  string
}

// quotable rejects arguments that could escape a single-quoted SQL
// literal. Dolt stored procedures do not support placeholders in all
// versions, so arguments are inlined after validation.
func quotable(arg string) error {
	if strings.ContainsAny(arg, "'`\\;") {
		return fmt.Errorf("godolt: argument %q contains disallowed characters", arg)
	}
	return nil
}

func (c *Client) call(ctx context.Context, proc string, args ...string) (message string, err error) {
	for _, a := range args {
		if err := quotable(a); err != nil {
			return "", err
		}
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + a + "'"
	}
	// #nosec G201 -- proc is a package-internal constant name and args are
	// validated by quotable above.
	q := fmt.Sprintf("CALL %s(%s)", proc, strings.Join(quoted, ","))
	rows, err := c.DB.QueryContext(ctx, q)
	if err != nil {
		return "", fmt.Errorf("godolt: %s: %w", proc, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	var status int
	if rows.Next() {
		switch len(cols) {
		case 1:
			if err := rows.Scan(&status); err != nil {
				return "", err
			}
		default:
			if err := rows.Scan(&status, &message); err != nil {
				return "", err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return message, err
	}
	if status != 0 {
		return message, fmt.Errorf("godolt: %s returned status %d: %s", proc, status, message)
	}
	return message, nil
}

// RemoteAdd registers a remote (CALL DOLT_REMOTE('add', name, url)).
func (c *Client) RemoteAdd(ctx context.Context, name, url string) error {
	_, err := c.call(ctx, "DOLT_REMOTE", "add", name, url)
	return err
}

// RemoteRemove removes a remote.
func (c *Client) RemoteRemove(ctx context.Context, name string) error {
	_, err := c.call(ctx, "DOLT_REMOTE", "remove", name)
	return err
}

// Remotes lists configured remotes from the dolt_remotes system table.
func (c *Client) Remotes(ctx context.Context) ([]Remote, error) {
	rows, err := c.DB.QueryContext(ctx, "SELECT name, url FROM dolt_remotes")
	if err != nil {
		return nil, fmt.Errorf("godolt: list remotes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Remote
	for rows.Next() {
		var r Remote
		if err := rows.Scan(&r.Name, &r.URL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Push pushes branch to remote (CALL DOLT_PUSH). Returns the server
// message (e.g. "Everything up-to-date").
func (c *Client) Push(ctx context.Context, remote, branch string) (string, error) {
	return c.call(ctx, "DOLT_PUSH", remote, branch)
}

// PullResult reports the outcome of a CALL DOLT_PULL.
type PullResult struct {
	FastForward bool
	Conflicts   int
	Message     string
}

// Pull pulls branch from remote (CALL DOLT_PULL) and merges into the
// active branch. DOLT_PULL returns three columns (fast_forward,
// conflicts, message) — a different shape from every other stored
// procedure call() handles, so Pull scans it directly rather than going
// through call(). A non-zero Conflicts count means DOLT_PULL completed
// the merge but left conflict rows in dolt_conflicts_<table> for the
// caller to resolve; it is not returned as an error, since that is
// expected, resolvable local state, not a failure of the pull itself.
func (c *Client) Pull(ctx context.Context, remote, branch string) (*PullResult, error) {
	for _, a := range []string{remote, branch} {
		if err := quotable(a); err != nil {
			return nil, err
		}
	}
	// #nosec G201 -- remote and branch are validated by quotable above.
	q := fmt.Sprintf("CALL DOLT_PULL('%s','%s')", remote, branch)
	rows, err := c.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("godolt: DOLT_PULL: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var res PullResult
	if rows.Next() {
		var fastForward, conflicts int
		if err := rows.Scan(&fastForward, &conflicts, &res.Message); err != nil {
			return nil, fmt.Errorf("godolt: DOLT_PULL: scan result: %w", err)
		}
		res.FastForward = fastForward != 0
		res.Conflicts = conflicts
	}
	if err := rows.Err(); err != nil {
		return &res, err
	}
	return &res, nil
}

// Fetch fetches from remote (CALL DOLT_FETCH).
func (c *Client) Fetch(ctx context.Context, remote string) error {
	_, err := c.call(ctx, "DOLT_FETCH", remote)
	return err
}

// ActiveBranch returns the connection's active branch.
func (c *Client) ActiveBranch(ctx context.Context) (string, error) {
	var b string
	if err := c.DB.QueryRowContext(ctx, "SELECT active_branch()").Scan(&b); err != nil {
		return "", fmt.Errorf("godolt: active branch: %w", err)
	}
	return b, nil
}
