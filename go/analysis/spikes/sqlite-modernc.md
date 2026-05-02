# Spike: SQLite via `modernc.org/sqlite`

**Status:** ✅ green — recommended driver for the Go port.

## Question

Sonarr v5 uses `SourceGear.sqlite3` (System.Data.SQLite under the hood) on the C# side, which is a CGo-based wrapper around the C SQLite library. The Go port wants to drop CGo entirely so cross-compilation to all our supported targets (linux-amd64/arm64/arm/musl, darwin-amd64/arm64, windows-amd64, freebsd-amd64) becomes a simple `GOOS=… GOARCH=… go build`. `modernc.org/sqlite` is a pure-Go transpilation of SQLite — promising on paper, but does it actually support the surface Sonarr uses?

## Surface Sonarr exercises

From a static read of `src/NzbDrone.Core/Datastore/**` and the migrations:

- `journal_mode = WAL` plus `synchronous = NORMAL`
- `foreign_keys = ON` (cascading deletes on Episodes when a Series is removed)
- `busy_timeout` (writer-locking is real because of background scan jobs)
- `COLLATE NOCASE` on title columns for case-insensitive lookup
- JSON1 functions (`json_extract`, `json_set`, `json_array_length`) heavily used to read/write embedded-document settings columns (per-indexer, per-downloader, per-notification config)
- Recursive CTEs (used in some history/queue traversal queries)
- Mixed many-readers + single-writer load with goroutines (the C# side uses thread pools)

## Verdict

`modernc.org/sqlite` v1.50.0 covers all of the above. The spike at `go/spikes/sqlite/sqlite_spike_test.go` is a runnable battery of tests for each item. It runs green under `go test -race`:

```
$ go test -count=1 -race ./spikes/sqlite/...
ok      sonarr.tv/sonarr/spikes/sqlite  1.704s
```

## Notes for the porter

- **Driver name** is `"sqlite"` (not `"sqlite3"` like with mattn/go-sqlite3). Adjust the `sql.Open` calls accordingly.
- **Pragmas via DSN**: pass them as `?_pragma=name(value)&_pragma=...`. The driver applies them on every connection in the pool, which is what we want — `journal_mode` is per-database and persists in the file header, but the others are per-connection.
- **No CGo**, so cross-compilation Just Works. No need for the `osxcross`/`musl-cross` toolchains the C# pipeline uses.
- **Performance** is reportedly ~1.5–2× slower than the native CGo wrapper on heavy workloads. Sonarr's DB load is small (single-user PVR), so this is not a concern. If we ever hit a hot path, we can opt in to `mattn/go-sqlite3` behind a build tag.
- **Foreign keys** must be re-enabled per connection via `_pragma=foreign_keys(1)` — this is also true of every CGo SQLite wrapper, not specific to modernc.

## Open follow-ups

- **Real DB compatibility check** is still pending: the spike uses a synthesized schema, not a captured production-shape Sonarr SQLite file. Phase 0.5 schema-dump deliverable will produce one — once it exists, add a follow-up test in `go/spikes/sqlite/` that opens the real file and runs `sqlite_master` queries.
- **Migration replay**: the goose ports in Phase 1 must produce a `.sql` schema dump byte-identical to the C# FluentMigrator output. The schema-dump deliverable is the diff target.
