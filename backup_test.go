package godolt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("dolt binary not available")
	}
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	backupDir := filepath.Join(root, "backup")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := InitDir(ctx, dbDir, "t", "t@local"); err != nil {
		t.Fatal(err)
	}

	// #nosec G204 -- test helper, static args plus a temp dir.
	seed := exec.Command("dolt", "sql", "-q",
		"CREATE TABLE items (id INT PRIMARY KEY); INSERT INTO items VALUES (1),(2),(3); "+
			"CALL DOLT_ADD('.'); CALL DOLT_COMMIT('-m','seed')")
	seed.Dir = dbDir
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v\n%s", err, out)
	}

	if err := BackupAdd(ctx, dbDir, "spike", "file://"+backupDir); err != nil {
		t.Fatalf("backup add: %v", err)
	}
	if err := BackupSync(ctx, dbDir, "spike"); err != nil {
		t.Fatalf("backup sync: %v", err)
	}
	if err := BackupRestore(ctx, root, "file://"+backupDir, "restored", false); err != nil {
		t.Fatalf("backup restore: %v", err)
	}

	// Restoring into an existing name without --force must fail.
	if err := BackupRestore(ctx, root, "file://"+backupDir, "restored", false); err == nil {
		t.Fatal("expected error restoring into an existing directory without --force")
	}
	if err := BackupRestore(ctx, root, "file://"+backupDir, "restored", true); err != nil {
		t.Fatalf("backup restore --force: %v", err)
	}

	restoredDir := filepath.Join(root, "restored")
	// #nosec G204 -- test verification against the restore we just created.
	verify := exec.Command("dolt", "sql", "-q", "SELECT COUNT(*) FROM items")
	verify.Dir = restoredDir
	out, err := verify.Output()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(string(out), "3") {
		t.Fatalf("restored row count wrong, got:\n%s", out)
	}
}
