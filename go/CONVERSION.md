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
  - **JSON wire-shape policy: byte-for-byte parity with the Sonarr v3 API.**
    Idiomatic Go's `omitempty` posture is INCOMPATIBLE with the v3 contract
    existing clients (React UI, mobile apps, *-arr ecosystem) parse against.
    Full rules + field-by-field table + audit checklist live at
    [JSON_CONVENTIONS.md](JSON_CONVENTIONS.md). Every new or modified
    JSON-emitting type MUST follow that policy.
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

## Per-execution metadata — use `ctx.Value`, not interface signature changes

Decided in slice 5.J198 (CommandTrigger plumbing) and reinforced in 5.J199 (CommandResultReporter). When porting C# `AsyncLocal<T>`-style per-execution metadata — anything per-command-dispatch that only a small subset of handlers consume — **use a `context.Value` helper, not a `Handler.Execute` interface signature change.**

Two precedents in tree before J198 followed this shape already (`internal/messaging/messaging.go` recursionKey, `internal/httperr/httperr.go` loggerCtxKey); J198/J199 added two more (`commands.WithTrigger` / `TriggerFromContext`, `commands.ReportResult` over the `resultSlot` mutable cell).

**Pattern:**

1. Add helpers to the owning package's `context.go`:
   - Unexported `xxxKey struct{}` sentinel (zero-size struct).
   - Public `WithXxx(ctx, val) ctx` setter.
   - Public `XxxFromContext(ctx) val` reader returning the zero value on miss.
2. Attach the value at the dispatcher boundary (one line in `commands/dispatcher.go:runHandler` for command-execution metadata).
3. Consume from the handler that needs it; handlers that don't never reference it.
4. If the metadata is **mutable** (output rather than input — e.g. a result a partial-success handler stamps), attach a pointer to a mutex-guarded slot; see the `resultSlot` pattern in J199. Last-write-wins matches C# `IManageCommandQueue.SetResult` semantics.

**Why:**
- Blast radius — 1-of-18 handlers consumes the metadata today; an interface change is 17:1 churn for negative value.
- C# fidelity — C# uses `AsyncLocal<T>` via `ProgressMessageContext`; `ctx.Value` is the direct Go translation.
- Reversibility — `ctx.Value` → interface signature is mechanical (grep the helper). The reverse means re-touching every handler.
- Safe default — the zero value matches the pre-port behaviour for every handler that doesn't consume the metadata.

**Escalate (don't apply this rule) when:**
- The metadata is **required for correctness** (handler can't do its job without it) — make it a parameter.
- **Most handlers** will eventually need it — then the interface change is cheaper than the church-of-ctx-readers it would otherwise become.
- Neither applies today.

**Don't mix the two patterns silently.** New per-execution metadata threads either follow this convention or escalate the divergence in the slice plan.
