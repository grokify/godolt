package godolt

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // test-only: the harness dials the throwaway server
)

func TestQuotableRejectsInjection(t *testing.T) {
	bad := []string{"a'b", "a;b", "a`b", `a\b`, "x'); DROP TABLE t; --"}
	for _, s := range bad {
		if err := quotable(s); err == nil {
			t.Errorf("quotable(%q) accepted", s)
		}
	}
	good := []string{"origin", "main", "file:///tmp/x", "https://doltremoteapi.example/org/db", "tenant-slug_1"}
	for _, s := range good {
		if err := quotable(s); err != nil {
			t.Errorf("quotable(%q) rejected: %v", s, err)
		}
	}
}

// startServer boots a throwaway dolt sql-server over a temp database and
// returns a connected *sql.DB. Skips when dolt is unavailable.
func startServer(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if !Available() {
		t.Skip("dolt binary not available")
	}
	root := t.TempDir()
	dbDir := filepath.Join(root, "syncdb")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InitDir(context.Background(), dbDir, "t", "t@local"); err != nil {
		t.Fatal(err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()

	// #nosec G204 -- test harness; port from net.Listen on loopback.
	srv := exec.Command("dolt", "sql-server", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port))
	srv.Dir = root
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	})

	dsn := fmt.Sprintf("root:@tcp(127.0.0.1:%d)/syncdb", port)
	var db *sql.DB
	deadline := time.Now().Add(15 * time.Second)
	for {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			pingErr := db.PingContext(ctx)
			cancel()
			if pingErr == nil {
				break
			}
			_ = db.Close()
			err = pingErr
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, root
}

func TestClientSyncRoundTrip(t *testing.T) {
	db, root := startServer(t)
	ctx := context.Background()
	c := New(db)

	// Seed a table + Dolt commit so there is something to push.
	for _, q := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(64))",
		"INSERT INTO items VALUES (1,'alpha'),(2,'beta')",
		"CALL DOLT_ADD('.')",
		"CALL DOLT_COMMIT('-m','seed')",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	branch, err := c.ActiveBranch(ctx)
	if err != nil {
		t.Fatal(err)
	}

	remoteDir := filepath.Join(root, "remote")
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteURL := "file://" + remoteDir

	if err := c.RemoteAdd(ctx, "origin", remoteURL); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	remotes, err := c.Remotes(ctx)
	if err != nil || len(remotes) != 1 || remotes[0].Name != "origin" {
		t.Fatalf("remotes = %v (%v)", remotes, err)
	}

	if _, err := c.Push(ctx, "origin", branch); err != nil {
		t.Fatalf("push: %v", err)
	}
	// Incremental push: no error, delta-aware message.
	msg, err := c.Push(ctx, "origin", branch)
	if err != nil {
		t.Fatalf("re-push: %v", err)
	}
	if msg == "" {
		t.Fatal("expected up-to-date message on re-push")
	}

	// Cold path: clone the pushed remote and verify content offline.
	cloneDir := filepath.Join(root, "clone")
	if err := Clone(ctx, remoteURL, cloneDir); err != nil {
		t.Fatalf("clone: %v", err)
	}
	cmd := exec.Command("dolt", "sql", "-q", "SELECT name FROM items ORDER BY id") // #nosec G204 -- test verification against the clone we just created
	cmd.Dir = cloneDir
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("query clone: %v", err)
	}
	if !strings.Contains(string(got), "alpha") {
		t.Fatalf("clone missing seeded row:\n%s", got)
	}

	if err := c.RemoteRemove(ctx, "origin"); err != nil {
		t.Fatalf("remote remove: %v", err)
	}
	remotes, _ = c.Remotes(ctx)
	if len(remotes) != 0 {
		t.Fatalf("remotes after remove = %v", remotes)
	}
}

// TestClientPullMergesDivergedBranch guards against a real bug caught
// while building visionstudio's RMI-VISIONSTUDIO-535 (cloud-to-local
// pull): CALL DOLT_PULL returns three columns (fast_forward, conflicts,
// message), not the two-column (status, message) shape the shared
// call() helper assumes for every other stored procedure — so
// Client.Pull unconditionally errored with "sql: expected 3 destination
// arguments in Scan, not 2" on every call, verified against a real
// dolt sql-server before this fix. This test reproduces true history
// divergence (two independent clones each commit before either pulls
// the other's push) so a fast-forward-only push is actually exercised.
func TestClientPullMergesDivergedBranch(t *testing.T) {
	dbA, root := startServer(t)
	ctx := context.Background()
	a := New(dbA)

	if _, err := dbA.ExecContext(ctx, "CALL DOLT_COMMIT('--allow-empty','-m','seed')"); err != nil {
		t.Fatal(err)
	}
	remoteDir := filepath.Join(root, "remote")
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteURL := "file://" + remoteDir
	if err := a.RemoteAdd(ctx, "origin", remoteURL); err != nil {
		t.Fatalf("A remote add: %v", err)
	}
	if _, err := a.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("A initial push: %v", err)
	}

	// B clones at "seed" — same base as A, then the two diverge.
	cloneDir := filepath.Join(root, "dbB")
	if err := Clone(ctx, remoteURL, cloneDir); err != nil {
		t.Fatalf("clone: %v", err)
	}
	dbB, cleanup := startServerOn(t, cloneDir)
	defer cleanup()
	b := New(dbB)

	// A advances and pushes; B advances locally without knowing about it.
	if _, err := dbA.ExecContext(ctx, "CALL DOLT_COMMIT('--allow-empty','-m','A-only')"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("A second push: %v", err)
	}
	if _, err := dbB.ExecContext(ctx, "CALL DOLT_COMMIT('--allow-empty','-m','B-only')"); err != nil {
		t.Fatal(err)
	}

	// B's push must be rejected: the remote has A's commit B doesn't have.
	if _, err := b.Push(ctx, "origin", "main"); err == nil {
		t.Fatal("expected B's push to be rejected as non-fast-forward")
	} else if !strings.Contains(err.Error(), "non-fast-forward") {
		t.Fatalf("push rejection error = %v, want it to mention non-fast-forward", err)
	}

	// Pull must succeed, merge cleanly (no real data touched), and report
	// zero conflicts — this is the call that was completely broken before
	// this fix, on every invocation, regardless of divergence.
	res, err := b.Pull(ctx, "origin", "main")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.Conflicts != 0 {
		t.Fatalf("conflicts = %d, want 0 (both commits were --allow-empty)", res.Conflicts)
	}

	// Now that B has integrated A's history, its push must succeed.
	if _, err := b.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("push after pull: %v", err)
	}
}

// startServerOn boots a throwaway dolt sql-server over an existing
// (already-initialized) Dolt data directory, e.g. a fresh Clone target.
func startServerOn(t *testing.T, dbDir string) (*sql.DB, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()

	// #nosec G204 -- test harness; port from net.Listen on loopback.
	srv := exec.Command("dolt", "sql-server", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port))
	srv.Dir = dbDir
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

	dbName := filepath.Base(dbDir)
	dsn := fmt.Sprintf("root:@tcp(127.0.0.1:%d)/%s", port, dbName)
	var db *sql.DB
	deadline := time.Now().Add(15 * time.Second)
	for {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			pingErr := db.PingContext(ctx)
			cancel()
			if pingErr == nil {
				break
			}
			_ = db.Close()
			err = pingErr
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return db, func() {
		_ = db.Close()
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}
}
