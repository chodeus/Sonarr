# Shadow-Traffic JSONL Log Format

The Phase 6.1 shadow-traffic toolkit (`cmd/shadow-traffic-record` / `cmd/shadow-traffic-replay` / `cmd/shadow-traffic-diff`) communicates via a one-line-per-entry JSONL file. This document is the operator-facing reference for that file format — the contract between the recorder (writer), the replayer (reader), and any hand-editor in between.

This file is the authoritative schema reference. If it disagrees with `writer.go` (`RecordedEntry`) or `replay.go` (`LogEntry`), the code wins — file a doc bug.

## File shape

One JSONL file = one recording session.

```
{"ts":"2026-05-26T20:01:14.123Z","method":"GET","path":"/api/v3/series","headers":{...},"status":200}
{"ts":"2026-05-26T20:01:14.871Z","method":"POST","path":"/api/v3/customformat","headers":{...},"body":"{\"name\":\"x265\"}","status":201}
# blank lines and # comments are allowed for hand-edited logs

{"ts":"2026-05-26T20:01:15.412Z","method":"DELETE","path":"/api/v3/customformat/1","status":200}
```

- Each non-blank, non-comment line is exactly one JSON object.
- Lines are independent — no surrounding `[` `]` array brackets, no trailing comma.
- The recorder appends; it never rewrites earlier lines. Restart-resume on the same path adds new entries to the end of the file.
- The reader is streaming, with a 4 MiB max-line cap (large POST bodies like `manualimport` + `customformat-bulk` fit; anything larger gets a parse error referencing the offending line number).

## Per-entry schema

| Field    | JSON type                     | Tag                  | Required | Meaning                                                                                                                                                   |
| -------- | ----------------------------- | -------------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ts`     | RFC 3339 string               | `ts,omitzero` (read) | no       | When the request entered the recording proxy. Drives replay pacing (next.ts − prev.ts = sleep, scaled by `-speed`). Omit → entry replays with no delay.   |
| `method` | string                        | `method`             | YES      | HTTP method. Free-form per HTTP/1.1 §3.1.1 but in practice one of `GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/`OPTIONS`.                                    |
| `path`   | string                        | `path`               | YES      | Request URI including query string (e.g. `/api/v3/series/1?foo=bar`). Replay parses `?` to split path from `RawQuery`.                                    |
| `headers`| object<string, string>        | `headers,omitempty`  | no       | Request headers, single representative value per key. Hop-by-hop entries already stripped on capture; see "Redaction" below for what else gets stripped.  |
| `body`   | string                        | `body,omitempty`     | no       | Request body. If the recording was JSON, the JSON string verbatim; for other content types, the bytes interpreted as UTF-8. See "Body encoding" below.   |
| `status` | int                           | `status,omitempty`   | no       | Recorded upstream response status code. `0` is the sentinel for "upstream unreachable / failed before responding" (see "Status=0" below). Omit ≡ 0.       |

The recorder's `Timestamp` JSON tag is `json:"ts"` (no omitempty); the replayer's read-side uses `json:"ts,omitzero"` so a hand-edited log can omit it entirely.

## Minimal entries

```jsonl
{"method":"GET","path":"/api/v3/system/status"}
```

Replay-legal: no `ts` (no pacing delay), no `headers` (just the headers the replayer injects), no `body`, no `status` (filters that gate on status will skip).

## Body encoding

`body` is a JSON string. Two things to know:

1. **JSON-encoded ≠ binary-safe.** The on-disk format goes through `json.Marshal`, which silently replaces invalid UTF-8 bytes with the Unicode replacement character (U+FFFD). If you need to round-trip arbitrary bytes (rare for an HTTP API — Sonarr is JSON-only), the JSONL format is the wrong fit. For the Sonarr API this is a non-issue: every body is JSON.
2. **gzip request bodies are decompressed in-place** (J244). When the captured request had `Content-Encoding: gzip`, the recorder decompresses the body into the log and STRIPS `Content-Encoding` from the captured `headers` map. The on-wire upstream-bound bytes were NOT altered (the proxy is semantically transparent — upstream got the original gzipped payload). Operator inspection of the log sees plain JSON.

   Malformed gzip degrades to verbatim logging: the gzipped bytes go into `body` (subject to the UTF-8 caveat above) and `Content-Encoding` STAYS in `headers` (still truthful — the body in the log IS what the client sent, gzip-tagged).

   See the recorder's `maybeDecompressForLog` for the implementation contract (gzip-only; bomb-guarded to `-max-body-len` or 8 MiB; malformed-safe).

## Headers map

Single-value-per-key (the `map[string]string` shape, NOT `map[string][]string`). When the original request had multiple values for one header, only the first survives. This is a deliberate simplification — multi-value headers in the wild on the Sonarr API are essentially non-existent.

Stripped from the captured headers map (operator can see these were filtered):

- **Hop-by-hop headers** (RFC 7230 §6.1) — `Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `Te`, `Trailers`, `Transfer-Encoding`, `Upgrade`. Forwarding these across a proxy is a protocol violation; the recorder filters them on capture so replay doesn't inject them either.
- **API keys** (when `-drop-api-keys=true`, the default) — `X-Api-Key`, `Authorization`. Captured logs are routinely emailed / Slack'd / committed to repos for diff investigation, so default-deny on credential leakage. Pass `-drop-api-keys=false` if you trust the downstream pipeline AND need the original auth context for replay.
- **`Content-Encoding`** — only when the recorder decompressed the body in place. Otherwise the header stays.

Upstream-bound dispatch sees the ORIGINAL headers (only hop-by-hop are stripped on the request side too, because RFC 7230 mandates that). API keys reach the upstream verbatim even when stripped from the log — the proxy is transparent to the API contract.

## Status=0 sentinel

When the upstream backend is unreachable or fails before producing a status (DNS error, connection refused, TLS handshake failure, timeout, EOF mid-headers), the recorder writes the entry with `Status: 0`. The omitempty tag drops the field entirely, so:

```jsonl
{"ts":"...","method":"GET","path":"/api/v3/series","headers":{...}}
```

is indistinguishable from:

```jsonl
{"ts":"...","method":"GET","path":"/api/v3/series","headers":{...},"status":0}
```

Both mean "the recorder accepted the request, the upstream did not respond cleanly." The client side received a 502 from the recorder. The recorder's own request counter still ticks.

Status filtering during replay (`-status` flag) treats `0` as its own bucket — `-status 0` matches only upstream-failure entries; `-status 200,4xx` matches 2xx-200 + any 4xx but NOT failures.

## Truncation marker

If `-max-body-len` (default 256 KiB) is exceeded, the recorder writes the first `max-body-len` bytes of the body followed by the literal ASCII sentinel `...<truncated>`:

```jsonl
{"method":"POST","path":"/api/v3/manualimport","body":"{\"items\":[{\"path\":\"...long...\"}, ...<truncated>"}
```

The marker is unambiguous (no real JSON body ends with `...<truncated>` outside of this convention). Replay sends the marker verbatim to the target as if it were body text — which means a truncated entry will NOT replay correctly against a real server (the JSON is malformed). Truncation is a "preserve some context for inspection" knob, not a "replay correctly" knob. If you need byte-perfect replay, set `-max-body-len=0` (unlimited) at capture time.

## Comments and blank lines

The reader skips:

- Lines that are entirely whitespace.
- Lines whose first non-whitespace character is `#`.

So hand-edited logs can have:

```jsonl
# 2026-05-26 capture session — Sonarr v5.0.0.1658
# Focus: customformat regression after upstream PR #9876

{"ts":"...","method":"GET","path":"/api/v3/customformat","status":200}

# This entry is a manually-crafted probe — no recording timestamp
{"method":"GET","path":"/api/v3/customformat/schema"}
```

Both the leading metadata block and the inline annotation are stripped by the reader; only the two JSON lines are dispatched.

This is for human convenience only — the recorder itself never writes comments or blank lines.

## Examples

### Plain GET

```jsonl
{"ts":"2026-05-26T20:01:14.123Z","method":"GET","path":"/api/v3/series","headers":{"Accept":"application/json","User-Agent":"curl/8.5.0"},"status":200}
```

### POST with JSON body

```jsonl
{"ts":"2026-05-26T20:01:14.871Z","method":"POST","path":"/api/v3/customformat","headers":{"Content-Type":"application/json"},"body":"{\"name\":\"x265\",\"specifications\":[]}","status":201}
```

### POST with originally-gzipped body, decompressed by recorder

```jsonl
{"ts":"2026-05-26T20:01:14.871Z","method":"POST","path":"/api/v3/customformat/bulk","headers":{"Content-Type":"application/json"},"body":"[{\"id\":1},{\"id\":2}]","status":200}
```

Note `Content-Encoding` absent from `headers` even though the original request had it — the recorder stripped it because the body in the log is now plain JSON.

### POST with API key stripped (default `-drop-api-keys=true`)

```jsonl
{"ts":"2026-05-26T20:01:14.871Z","method":"POST","path":"/api/v3/qualityprofile","headers":{"Content-Type":"application/json"},"body":"{\"name\":\"HD-1080p\"}","status":201}
```

Upstream got `X-Api-Key: <original>` verbatim; the log shows it stripped.

### Upstream unreachable (Status=0)

```jsonl
{"ts":"2026-05-26T20:01:14.871Z","method":"GET","path":"/api/v3/series","headers":{"Accept":"application/json"}}
```

Client got 502 from the recorder; the entry preserves the request shape for forensics.

### Truncated body

```jsonl
{"ts":"2026-05-26T20:01:14.871Z","method":"POST","path":"/api/v3/manualimport","headers":{"Content-Type":"application/json"},"body":"{\"items\":[{\"id\":1, ...<truncated>","status":200}
```

The `...<truncated>` sentinel signals to readers that the body has been cut at `-max-body-len`. Replay against a real server will reject the malformed JSON.

### Hand-edited probe (no timestamp)

```jsonl
# Quick smoke probe — no pacing wanted
{"method":"GET","path":"/api/v3/system/status"}
{"method":"GET","path":"/api/v3/health"}
```

Replays back-to-back at maximum rate (the replayer's pacing falls back to no-delay when `ts` is zero on either entry).

## Inter-tool contract

The three Phase 6.1 binaries share this format:

| Binary                       | Role                  | Reads format | Writes format |
| ---------------------------- | --------------------- | :----------: | :-----------: |
| `cmd/shadow-traffic-record`  | Capture (tee + log)   | -            | YES           |
| `cmd/shadow-traffic-replay`  | Dispatch from log     | YES          | -             |
| `cmd/shadow-traffic-diff`    | Dual-dispatch + diff  | -            | -             |

`shadow-traffic-diff` doesn't touch the JSONL format directly — it's a live reverse-proxy that dispatches each inbound request to two upstreams in parallel and diffs the responses. To exercise diff with previously-recorded traffic, chain replay → diff: `replay -target=<diff-listen-addr>`.

## Stability

The schema has been stable since J239 (introduced) through J244 (gzip handling added without schema change). Field additions are backward-compatible (older readers ignore unknown fields); field removals or type changes would be a breaking change and should bump a schema-version tag (none today — the format is small enough that operator inspection catches drift).

If you find yourself wanting a new field, prefer:

1. Encoding it inside `headers` as an `X-Shadow-*` synthetic header (no schema change).
2. Adding it as an optional `omitempty` field (backward-compatible).

Only in last resort: a new mandatory field (would break every existing capture).
