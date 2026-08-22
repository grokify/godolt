package godolt

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port
}

func TestStartServerAndEnsureServer(t *testing.T) {
	if !Available() {
		t.Skip("dolt binary not available")
	}
	dataDir := t.TempDir()
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	if ServerReachable(addr) {
		t.Fatalf("unexpected listener on %s before start", addr)
	}

	proc, err := StartServer(dataDir, port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = proc.Process.Kill()
		_, _ = proc.Process.Wait()
	})

	if !ServerReachable(addr) {
		t.Fatal("server not reachable after StartServer")
	}

	// EnsureServer against a running server is a no-op.
	if err := EnsureServer(dataDir, port); err != nil {
		t.Fatalf("EnsureServer on running server: %v", err)
	}
}

func TestCreateDatabaseAndCommitAll(t *testing.T) {
	db, _ := startServer(t)
	ctx := context.Background()

	base := connectedBase(t, db)
	if err := CreateDatabase(ctx, base, "newdb"); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := CreateDatabase(ctx, base, "newdb"); err != nil {
		t.Fatal(err)
	}
	if err := CreateDatabase(ctx, base, ""); err == nil {
		t.Fatal("expected error for empty database name")
	}

	c := New(db)

	// Clean working set: CommitAll is a no-op.
	hash, err := c.CommitAll(ctx, "noop")
	if err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Fatalf("expected no-op commit on clean working set, got hash %q", hash)
	}

	if _, err := db.ExecContext(ctx, "CREATE TABLE things (id INT PRIMARY KEY, note TEXT)"); err != nil {
		t.Fatal(err)
	}
	dirty, err := c.HasUncommittedChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("expected dirty working set after CREATE TABLE")
	}

	// Message with characters call()'s quotable would reject.
	hash, err = c.CommitAll(ctx, "add things; it's a `table`")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("expected commit hash")
	}

	dirty, err = c.HasUncommittedChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("expected clean working set after CommitAll")
	}
}

// connectedBase opens a server-wide connection (no database selected) to
// the same throwaway server backing db.
func connectedBase(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	var addr string
	// The harness DSN targets the "syncdb" database; derive the base
	// address from a round trip through the server.
	row := db.QueryRow("SELECT @@hostname")
	_ = row.Scan(new(string)) // reachability probe only; value unused
	// startServer always dials 127.0.0.1 with root and no password, so
	// rebuild the base DSN from the connection's port.
	var port int
	if err := db.QueryRow("SELECT @@port").Scan(&port); err != nil {
		t.Fatal(err)
	}
	addr = fmt.Sprintf("root:@tcp(127.0.0.1:%d)/", port)
	base, err := sql.Open("mysql", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := base.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return base
}
