# godolt

[![Go CI][go-ci-svg]][go-ci-url]
[![Go Lint][go-lint-svg]][go-lint-url]
[![Go SAST][go-sast-svg]][go-sast-url]
[![Docs][docs-godoc-svg]][docs-godoc-url]
[![Docs][docs-mkdoc-svg]][docs-mkdoc-url]
[![Visualization][viz-svg]][viz-url]
[![License][license-svg]][license-url]

 [go-ci-svg]: https://github.com/grokify/godolt/actions/workflows/go-ci.yaml/badge.svg?branch=main
 [go-ci-url]: https://github.com/grokify/godolt/actions/workflows/go-ci.yaml
 [go-lint-svg]: https://github.com/grokify/godolt/actions/workflows/go-lint.yaml/badge.svg?branch=main
 [go-lint-url]: https://github.com/grokify/godolt/actions/workflows/go-lint.yaml
 [go-sast-svg]: https://github.com/grokify/godolt/actions/workflows/go-sast-codeql.yaml/badge.svg?branch=main
 [go-sast-url]: https://github.com/grokify/godolt/actions/workflows/go-sast-codeql.yaml
 [docs-godoc-svg]: https://pkg.go.dev/badge/github.com/grokify/godolt
 [docs-godoc-url]: https://pkg.go.dev/github.com/grokify/godolt
 [docs-mkdoc-svg]: https://img.shields.io/badge/Go-dev%20guide-blue.svg
 [docs-mkdoc-url]: https://grokify.github.io/godolt
 [viz-svg]: https://img.shields.io/badge/visualizaton-Go-blue.svg
 [viz-url]: https://mango-dune-07a8b7110.1.azurestaticapps.net/?repo=grokify%2Fgodolt
 [loc-svg]: https://tokei.rs/b1/github/grokify/godolt
 [repo-url]: https://github.com/grokify/godolt
 [license-svg]: https://img.shields.io/badge/license-MIT-blue.svg
 [license-url]: https://github.com/grokify/godolt/blob/main/LICENSE

godolt wraps [Dolt](https://www.dolthub.com/)'s operational surface for Go programs, following the [gogit](https://github.com/grokify/gogit) precedent — a standalone, dependency-light service-integration module.

**Design: SQL-first.** The primary workload is many concurrent sessions against `dolt sql-server` (the embedded driver sustains only one stable connection and is not a target), so version-control verbs — remotes, push, pull, fetch, active branch — run as Dolt stored procedures over the same MySQL wire as queries. The caller supplies the `*sql.DB` and godolt adds zero driver dependencies. The few operations that cannot run over the wire — clone/init bootstrap, server lifecycle, backups — shell out to the `dolt` CLI.

```go
db, _ := sql.Open("mysql", "root:@tcp(127.0.0.1:3306)/mydb")
client := godolt.New(db)

if err := client.RemoteAdd(ctx, "origin", "file:///path/to/remote"); err != nil {
    log.Fatal(err)
}
msg, err := client.Push(ctx, "origin", "main")
```

## Library Features

| Feature | Entry Point | Description |
|---------|-------------|--------------|
| Client | `New(db *sql.DB)` | Wraps an existing `*sql.DB` connection to a `dolt sql-server`; the caller owns the driver, pooling, and DSN |
| Remotes | `Client.RemoteAdd`, `Client.RemoteRemove`, `Client.Remotes` | Register, remove, and list configured remotes (`CALL DOLT_REMOTE(...)`) |
| Sync | `Client.Push`, `Client.Fetch` | Push/fetch against a remote (`CALL DOLT_PUSH`/`DOLT_FETCH`), returning the server's status message |
| Sync | `Client.Pull` | Pull and merge a remote's branch (`CALL DOLT_PULL`); returns `*PullResult` (`FastForward`, `Conflicts`, `Message`) — a non-zero `Conflicts` means the merge completed but left conflict rows in `dolt_conflicts_<table>` for the caller to resolve |
| Branch | `Client.ActiveBranch` | The connection's active branch (`SELECT active_branch()`) |
| Commits | `Client.HasUncommittedChanges`, `Client.AddAll`, `Client.Commit`, `Client.CommitAll` | Dirty-status check (`dolt_status`), stage all (`CALL DOLT_ADD('.')`), and commit (`CALL DOLT_COMMIT`) returning the new hash; `CommitAll` is a no-op on a clean working set — the pattern applications use to wrap sync runs in Dolt commits |
| Databases | `CreateDatabase(ctx, db, name)` | `CREATE DATABASE IF NOT EXISTS` over a server-wide connection (no database selected — see `SplitDSN`) |
| DSN helpers | `LocalDSN`, `EnsureParseTime`, `SplitDSN` | Build the conventional local-server DSN, append `parseTime=true` for ORMs like Ent, and split a DSN into its server-wide base and database name |
| Server lifecycle | `ServerReachable`, `StartServer`, `EnsureServer` | Probe an address, launch `dolt sql-server` over a data directory and wait for readiness (caller owns the process), or ensure one is serving — launching detached if not (the shared local-server pattern) |
| Bootstrap | `Clone(ctx, remoteURL, dir)`, `InitDir(ctx, dir, name, email)` | Cold-path operations that shell out to the `dolt` CLI, since no server exists yet to talk to |
| Availability | `Available()` | Reports whether the `dolt` CLI is on `PATH` — the cold path's prerequisite |
| Backups | `BackupAdd`, `BackupSync`, `BackupRestore` | Named backup targets, snapshot/sync, and restore — no `DOLT_BACKUP` stored procedure exists, so these are CLI-exec only. Verified safe to run against a directory a live `dolt sql-server` is actively serving |

## Installation

```bash
go get github.com/grokify/godolt
```

## Usage

### SQL-wire operations (the primary workload)

```go
import (
	"context"
	"database/sql"

	"github.com/grokify/godolt"
	_ "github.com/go-sql-driver/mysql"
)

db, err := sql.Open("mysql", "root:@tcp(127.0.0.1:3306)/mydb")
if err != nil {
	log.Fatal(err)
}
client := godolt.New(db)

ctx := context.Background()
if err := client.RemoteAdd(ctx, "origin", "file:///path/to/remote"); err != nil {
	log.Fatal(err)
}

remotes, err := client.Remotes(ctx)

msg, err := client.Push(ctx, "origin", "main")

// Push is fast-forward-only: a remote with commits the local branch
// doesn't have rejects the push (error contains "non-fast-forward").
// Pull to fetch and merge before pushing again.
result, err := client.Pull(ctx, "origin", "main")
if result.Conflicts > 0 {
	// Merge completed; resolve dolt_conflicts_<table> rows before pushing.
}

branch, err := client.ActiveBranch(ctx)
```

### Application store pattern (server lifecycle + commits)

The pattern shared by visionstudio, omniroadmap, and uiforge: ensure a
long-lived local `dolt sql-server` over a data directory, create the app's
database, connect with Ent (or any MySQL client), and wrap write runs in Dolt
commits.

```go
// Ensure a shared local server is running (launches detached if not).
if err := godolt.EnsureServer("/path/to/data", 13307); err != nil {
	log.Fatal(err)
}

// Create the database if needed, connecting server-wide first.
dsn := godolt.EnsureParseTime(godolt.LocalDSN(13307, "myapp"))
base, dbName, err := godolt.SplitDSN(dsn)
if err != nil {
	log.Fatal(err)
}
serverDB, err := sql.Open("mysql", base)
if err != nil {
	log.Fatal(err)
}
if err := godolt.CreateDatabase(ctx, serverDB, dbName); err != nil {
	log.Fatal(err)
}
_ = serverDB.Close()

// Connect to the database and wrap work in a Dolt commit.
db, err := sql.Open("mysql", dsn)
if err != nil {
	log.Fatal(err)
}
client := godolt.New(db)
// ... write rows ...
hash, err := client.CommitAll(ctx, "sync run 2026-08-19")
if err != nil {
	log.Fatal(err)
}
if hash == "" {
	// Clean working set — nothing to commit.
}
```

### Cold path: bootstrap and backups

```go
if !godolt.Available() {
	log.Fatal("dolt CLI not found on PATH")
}

if err := godolt.InitDir(ctx, "/path/to/db", "my-service", "my-service@local"); err != nil {
	log.Fatal(err)
}

if err := godolt.Clone(ctx, "file:///path/to/remote", "/path/to/clone"); err != nil {
	log.Fatal(err)
}

if err := godolt.BackupAdd(ctx, "/path/to/db", "spike", "file:///path/to/backup"); err != nil {
	log.Fatal(err)
}
if err := godolt.BackupSync(ctx, "/path/to/db", "spike"); err != nil {
	log.Fatal(err)
}
```

## Documentation

Full docs, including release notes, are published at [grokify.github.io/godolt](https://grokify.github.io/godolt).

## License

[MIT](LICENSE)
