package godolt

import (
	"context"
	"fmt"
	"os/exec"
)

// The cold path: operations that cannot run over the SQL wire because no
// server exists yet on the target (bootstrap) or the operation manages
// the server itself. These shell out to the dolt CLI.

// Clone clones a remote URL into dir (dir must not already contain a
// Dolt repository).
func Clone(ctx context.Context, remoteURL, dir string) error {
	// #nosec G204 -- dolt invoked by name; args are caller-supplied paths/URLs
	// for a local developer tool, not untrusted network input.
	cmd := exec.CommandContext(ctx, "dolt", "clone", remoteURL, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("godolt: clone %s: %w\n%s", remoteURL, err, out)
	}
	return nil
}

// InitDir initializes a new Dolt database directory with the given
// committer identity.
func InitDir(ctx context.Context, dir, name, email string) error {
	// #nosec G204 -- see Clone.
	cmd := exec.CommandContext(ctx, "dolt", "init", "--name", name, "--email", email)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("godolt: init %s: %w\n%s", dir, err, out)
	}
	return nil
}

// Available reports whether the dolt CLI is on PATH (the cold path's
// prerequisite; the SQL path needs only a *sql.DB).
func Available() bool {
	_, err := exec.LookPath("dolt")
	return err == nil
}

// Backup operations. No SQL-callable equivalent exists (verified: no
// DOLT_BACKUP stored procedure) — CLI-exec only. Verified safe to run
// against a directory a live dolt sql-server is actively serving; no
// need to stop the server first.

// BackupAdd registers a named backup target for the Dolt database in dir.
func BackupAdd(ctx context.Context, dir, name, url string) error {
	// #nosec G204 -- see Clone.
	cmd := exec.CommandContext(ctx, "dolt", "backup", "add", name, url)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("godolt: backup add %s: %w\n%s", name, err, out)
	}
	return nil
}

// BackupSync snapshots the Dolt database in dir (branches, tags, working
// sets) and uploads it to the named backup target.
func BackupSync(ctx context.Context, dir, name string) error {
	// #nosec G204 -- see Clone.
	cmd := exec.CommandContext(ctx, "dolt", "backup", "sync", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("godolt: backup sync %s: %w\n%s", name, err, out)
	}
	return nil
}

// BackupRestore restores a database from a backup URL into a new
// subdirectory named dbName under workDir (workDir must not already
// contain a directory of that name unless force is set).
func BackupRestore(ctx context.Context, workDir, url, dbName string, force bool) error {
	args := []string{"backup", "restore"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, url, dbName)
	// #nosec G204 -- see Clone.
	cmd := exec.CommandContext(ctx, "dolt", args...)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("godolt: backup restore %s: %w\n%s", url, err, out)
	}
	return nil
}
