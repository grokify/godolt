# godolt

godolt wraps [Dolt](https://www.dolthub.com/)'s operational surface for Go programs, following the [gogit](https://github.com/grokify/gogit) precedent — a standalone, dependency-light service-integration module.

**Design: SQL-first.** The primary workload is many concurrent sessions against `dolt sql-server` (the embedded driver sustains only one stable connection and is not a target), so version-control verbs run as Dolt stored procedures over the same MySQL wire as queries. The caller supplies the `*sql.DB` and godolt adds zero driver dependencies. The few operations that cannot run over the wire — clone/init bootstrap, server lifecycle, backups — shell out to the `dolt` CLI.

## Library

- **Client** — `New(db *sql.DB)` wraps an existing `*sql.DB` connection to a `dolt sql-server`; the caller owns the driver, pooling, and DSN.
- **Remotes** — `Client.RemoteAdd`, `Client.RemoteRemove`, and `Client.Remotes` register, remove, and list configured remotes (`CALL DOLT_REMOTE(...)`).
- **Sync** — `Client.Push` and `Client.Fetch` push/fetch against a remote (`CALL DOLT_PUSH`/`DOLT_FETCH`), returning the server's status message. Push is fast-forward-only; a diverged remote rejects it. `Client.Pull` fetches and merges a remote's branch (`CALL DOLT_PULL`), returning `*PullResult` (`FastForward`, `Conflicts`, `Message`) — a non-zero `Conflicts` means the merge completed but left conflict rows in `dolt_conflicts_<table>` for the caller to resolve locally.
- **Branch** — `Client.ActiveBranch` returns the connection's active branch (`SELECT active_branch()`).
- **Commits** — `Client.HasUncommittedChanges`, `Client.AddAll`, `Client.Commit`, and `Client.CommitAll` check the working set (`dolt_status`), stage everything (`CALL DOLT_ADD('.')`), and commit (`CALL DOLT_COMMIT`, returning the new hash). `CommitAll` is a no-op on a clean working set — the pattern applications use to wrap sync runs in Dolt commits.
- **Databases** — `CreateDatabase(ctx, db, name)` runs `CREATE DATABASE IF NOT EXISTS` over a server-wide connection (no database selected — see `SplitDSN`).
- **DSN helpers** — `LocalDSN`, `EnsureParseTime`, and `SplitDSN` build the conventional local-server DSN, append `parseTime=true` for ORMs like Ent, and split a DSN into its server-wide base and database name. Pure string helpers — no driver dependency added.
- **Server lifecycle** — `ServerReachable`, `StartServer`, and `EnsureServer` probe an address, launch `dolt sql-server` over a data directory and wait for readiness (the caller owns the process), or ensure one is serving — launching it detached if not, the shared local-server pattern from visionstudio and omniroadmap.
- **Bootstrap** — `Clone(ctx, remoteURL, dir)` and `InitDir(ctx, dir, name, email)` are cold-path operations that shell out to the `dolt` CLI, since no server exists yet to talk to.
- **Availability** — `Available()` reports whether the `dolt` CLI is on `PATH`, the cold path's prerequisite.
- **Backups** — `BackupAdd`, `BackupSync`, and `BackupRestore` manage named backup targets, snapshot/sync, and restore. No `DOLT_BACKUP` stored procedure exists, so these are CLI-exec only — verified safe to run against a directory a live `dolt sql-server` is actively serving.

```go
db, _ := sql.Open("mysql", "root:@tcp(127.0.0.1:3306)/mydb")
client := godolt.New(db)

if err := client.RemoteAdd(ctx, "origin", "file:///path/to/remote"); err != nil {
    log.Fatal(err)
}
msg, err := client.Push(ctx, "origin", "main")
```

Install:

```bash
go get github.com/grokify/godolt
```

See the [README](https://github.com/grokify/godolt#readme) for full usage examples, and [Releases](releases/v0.3.0.md) for version history.
