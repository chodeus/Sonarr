# Contract test harness

The contract harness is the load-bearing safety net for the C#→Go port. It records the C# v5 backend's exact REST and SignalR behavior as golden files, then replays the same request corpus against the Go implementation and asserts byte-equality on the responses.

## Why bytes, not "shape-equivalent"

Third-party clients (Overseerr, Tautulli, Prowlarr, etc.) and the React frontend depend on field ordering, casing, null-vs-omitted, status codes, and error envelope wording. A "looks the same" check is not enough; the bar is byte-equality.

Where byte-equality is impossible (timestamps, generated IDs, random tokens) we use *normalized* equality: a documented set of redactions applied identically to both responses before diff.

## Layout

```
test/contract/
  README.md                        # this file
  corpus/                          # input corpus — request specs grouped by feature
    series/list.json
    series/lookup.json
    ...
  golden/                          # recorded C# responses — committed to git
    series/list.golden.json
    ...
  recorder/                        # CLI that drives a C# binary and writes golden/
  replayer/                        # Go test helper that hits a running Go binary
  diff/                            # CLI for ad-hoc drift diagnostics (slice 4.FN–FQ)
  internal/                        # request/response types, normalizer, diff
```

## Usage (developer-facing)

### Record golden files against the C# backend

```sh
# In one shell, boot the C# Sonarr against a fixture DB and an empty appdata dir.
dotnet run --project ../../src/NzbDrone -- -appdata /tmp/sonarr-fixtures -nobrowser

# In another, drive the corpus and write golden files.
go run ./test/contract/recorder \
    -base http://127.0.0.1:8989 \
    -api-key $SONARR_API_KEY \
    -corpus ./test/contract/corpus \
    -out ./test/contract/golden
```

### Replay against the Go backend

```sh
# Boot the Go binary the same way, then:
go test ./test/contract/replayer/... \
    -contract.base=http://127.0.0.1:8989 \
    -contract.api-key=$SONARR_API_KEY
```

CI runs the replay step in the `vet-lint-test` job once a Go API surface exists.

### Diagnose drift with the diff CLI

When the replayer fails it surfaces structured diffs in the test log (slice 4.FO). For wider-scope drift inspection — e.g. "show me every endpoint that diverges right now" — use the standalone `diff` CLI:

```sh
# Mode A: live Go vs recorded C# goldens. Useful without a staging host.
go run ./test/contract/diff \
    -primary   http://127.0.0.1:8989 \
    -primary-key $SONARR_API_KEY \
    -corpus    ./test/contract/corpus/v3-empty-state.json \
    -golden    ./test/contract/golden/v3-empty-state \
    -v

# Mode B: live Go vs live C# (Phase 6 shadow-traffic / staging).
go run ./test/contract/diff \
    -primary   http://127.0.0.1:8990 \
    -secondary http://127.0.0.1:8989 \
    -primary-key   $GO_API_KEY \
    -secondary-key $CSHARP_API_KEY \
    -corpus    ./test/contract/corpus/v3-empty-state.json
```

The CLI exits 0 on no drift, 1 on drift or per-entry errors, 2 on flag misuse. For machine consumption (nightly CI, dashboards, trend analysis) add `-format json`:

```sh
go run ./test/contract/diff \
    -primary http://127.0.0.1:8989 -primary-key $SONARR_API_KEY \
    -corpus  ./test/contract/corpus/v3-empty-state.json \
    -golden  ./test/contract/golden/v3-empty-state \
    -format json > drift-report.json
```

Output shape:

```json
{
  "summary": {"total": 51, "matched": 41, "mismatched": 10, "errored": 0},
  "results": [
    {
      "id": "v3.diskspace",
      "statusA": 200, "statusB": 200,
      "statusMatch": true,
      "bodyMismatch": true,
      "diffs": [{"path": "$", "kind": "value", "a": 5, "b": 3}]
    }
  ]
}
```

Empty `diffs` arrays are omitted; per-entry `err` strings appear as `"err": "..."` when a backend / golden read fails. Results are sorted alphabetically by `id` in both output modes so the report is greppable + diff-able across runs.

The underlying primitive (`normalize.JSONDiff`) is also what the replayer's failure renderer uses (slice 4.FO), so the replayer's test-log diffs and the CLI's report speak the same language: same path notation, same Kind enum, same value formatting.

## Authoring corpus entries

Each corpus file is a JSON document with one or more requests. A request entry:

```json
{
  "id": "series/list",
  "request": {
    "method": "GET",
    "path": "/api/v3/series",
    "query": {"includeSeasonImages": ["true"]},
    "headers": {},
    "body": null
  },
  "normalize": {
    "redact_paths": [
      "$[*].added",
      "$[*].lastInfoSync"
    ],
    "redact_headers": ["Date", "Server", "X-Application-Version"]
  }
}
```

A golden file stores the canonicalized response — status, headers (after redaction), and body (pretty-printed JSON when `Content-Type` is JSON, raw bytes otherwise).

The `normalize` block is the **only** place divergence is allowed. Every redaction must be justified in a comment in the corpus file. Reviewers should be skeptical of new redactions.

## SignalR

SignalR contracts are recorded by establishing a hub connection, performing the corpus action over REST, and capturing every server-pushed message until quiescence. The replayer asserts the same set of message names and (normalized) payload shapes arrive in the same order.

## Phase 0 status

The package skeleton in this directory compiles and is ready to receive corpus entries. Recording requires a running C# binary; that step lands as part of Phase 0.5 deliverable #4 (REST endpoint inventory) and #5 (SignalR event inventory).
