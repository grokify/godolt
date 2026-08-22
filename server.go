package godolt

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Server lifecycle: part of the cold path (exec.go) — a dolt sql-server
// cannot be started over its own SQL wire.

// serverReadyTimeout bounds how long StartServer waits for the launched
// server to accept connections.
const serverReadyTimeout = 5 * time.Second

// ServerReachable reports whether a TCP listener answers on addr
// (host:port) within 500ms.
func ServerReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// StartServer launches `dolt sql-server` over dataDir on 127.0.0.1:port,
// creating dataDir if needed, and waits until the server accepts
// connections. The caller owns the returned process: Wait on it, kill it,
// or detach. Requires the dolt binary on PATH.
func StartServer(dataDir string, port int) (*exec.Cmd, error) {
	doltBin, err := exec.LookPath("dolt")
	if err != nil {
		return nil, fmt.Errorf("godolt: `dolt` binary not found on PATH (install via https://docs.dolthub.com/introduction/installation): %w", err)
	}

	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("godolt: resolving data dir: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("godolt: creating data dir: %w", err)
	}

	// #nosec G204 -- binary from LookPath, args are typed.
	proc := exec.Command(doltBin, "sql-server", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port))
	proc.Dir = absDir
	if err := proc.Start(); err != nil {
		return nil, fmt.Errorf("godolt: starting dolt sql-server: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(serverReadyTimeout)
	for time.Now().Before(deadline) {
		if ServerReachable(addr) {
			return proc, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Process.Kill()
	_, _ = proc.Process.Wait()
	return nil, fmt.Errorf("godolt: dolt sql-server did not become reachable on %s", addr)
}

// EnsureServer checks that a dolt sql-server is reachable on
// 127.0.0.1:port, launching one as a detached subprocess over dataDir if
// not. The launched process keeps serving after the caller exits —
// suitable for local developer tools that share one long-lived server
// (the visionstudio/omniroadmap pattern).
func EnsureServer(dataDir string, port int) error {
	if ServerReachable(fmt.Sprintf("127.0.0.1:%d", port)) {
		return nil
	}
	proc, err := StartServer(dataDir, port)
	if err != nil {
		return err
	}
	// Detach: don't reap; the server outlives this process.
	go func() { _ = proc.Wait() }()
	return nil
}
