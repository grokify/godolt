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
