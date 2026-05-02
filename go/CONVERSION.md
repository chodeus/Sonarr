# C# → Go Conversion Rules

These rules are the authoritative guide for the Sonarr v5 backend port from C# (.NET) to Go. Every conversion PR must follow them. PRs that violate a rule must call it out explicitly in the description and justify the deviation.

> Source: prompt provided by the project owner during the v5-develop conversion plan.

## Mindset

You are a senior engineer with deep expertise in both C# .NET and Go. Convert the C# code to **idiomatic Go** — do **not** transliterate.

## Conversion Rules

### Language & paradigm

- Do **not** transliterate C# into Go — write Go as a Go engineer would.
- Replace OOP class hierarchies with Go structs + interfaces.
- Replace C# properties (get/set) with exported struct fields. Use getter/setter methods only when logic is required.
- Replace inheritance with composition and interface embedding.
- Replace abstract classes with Go interfaces.
- Replace C# generics with Go generics (1.18+) where appropriate; fall back to interfaces where generics would be overkill.

### Error handling

- Replace **all** exceptions (`try`/`catch`/`throw`) with Go's `(value, error)` return pattern.
- Replace custom exception classes with custom error types implementing the `error` interface.
- Use `fmt.Errorf("context: %w", err)` for error wrapping — never swallow errors silently.

### Concurrency

- Replace `async`/`await` + `Task<T>` with goroutines + channels or `sync.WaitGroup`.
- Replace `CancellationToken` with `context.Context` passed as the first argument.
- Replace C# `lock` with `sync.Mutex` or `sync.RWMutex`.
- Replace thread-local storage with explicit `context.Value` or function parameters.

### Ecosystem / libraries

- Replace `using` / `IDisposable` with `defer` + explicit `Close()` calls.
- Replace LINQ with idiomatic Go loops or the `slices`/`maps` standard packages.
- Replace Entity Framework with `database/sql` + a lightweight driver (`pgx` for Postgres, `modernc.org/sqlite` for SQLite).
- Replace `HttpClient` with `net/http`.
- Replace `Newtonsoft.Json` / `System.Text.Json` with `encoding/json`.
- Replace dependency-injection containers with explicit constructor injection (just pass deps as arguments).
- Replace `appsettings.json` / `IConfiguration` with `os.Getenv` or a typed config struct populated at startup.

### Project structure

- One package per directory; name packages by purpose (e.g. `handler`, `service`, `store`).
- `main()` lives in `cmd/<appname>/main.go`.
- Interfaces live in the package that **uses** them, not the one that implements them (Go convention).
- Unexported types/functions for internal implementation; exported only for public API.

### Style

- All exported names must have a godoc comment.
- Follow `gofmt` formatting conventions.
- Use table-driven tests when generating tests.

## Output expectations for every PR

1. Cite the analysis card (under `go/analysis/`) the PR ports from.
2. Note any C# construct that had no clean Go equivalent and how it was handled.
3. Note any behavior that differs subtly between the two versions.
4. Note any new `go.mod` dependency added.

## Sonarr-specific addenda

- **Frozen contracts** must not change without explicit sign-off:
  - REST API V3 + V5 — exact route, query, body, status, error envelope.
  - DB schema — SQLite and Postgres, identical to C# FluentMigrator output.
  - SignalR event names + payload shapes.
  - On-disk paths (`config.xml`, log dir, backup dir, MediaCover dir).
- **Behavior preservation over cleanup.** Improvements are for Phase 7, not the port.
- **No silent dependency upgrades.** If the C# uses a specific external API version (Skyhook, qBittorrent, etc.), the Go port targets the same version.
