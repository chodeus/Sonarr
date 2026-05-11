# JSON Wire-Shape Conventions

**Policy: byte-for-byte parity with the Sonarr v3 API JSON shape.**

Authoritative for every Go type that:
- Round-trips through an `/api/v3` or `/api/v5` endpoint, **or**
- Persists as an embedded-JSON column in the database (read/written by both .NET
  legacy and Go-port code paths), **or**
- Is consumed by an external client that already exists against the .NET API
  (React UI, mobile apps, Radarr/Prowlarr/*-arr ecosystem scripts).

This policy intentionally diverges from idiomatic Go's `omitempty` posture.
Compatibility with the existing Sonarr v3 contract supersedes idiomatic taste.

## Rationale

The .NET `System.Text.Json` serializer with default options emits:
- Non-nullable properties → always emitted (including their zero values).
- Nullable properties (`int?` / `DateTime?` / nullable reference types) → emitted
  as `null` when null.
- `List<T>` initialized to empty → emitted as `[]`.

Go's `encoding/json` with `omitempty` would:
- Omit zero values entirely (`0`, `""`, `false`, nil pointer/slice).
- Treat nil pointer identically to "field absent" — no way to emit explicit `null`.

These two postures produce **different JSON shapes** for the same logical data.
Existing consumers of the Sonarr v3 API parse against the .NET shape; the Go port
MUST match.

## Field-by-field rules

| .NET shape                              | Go shape                       | JSON emission                |
|-----------------------------------------|--------------------------------|------------------------------|
| `int` (non-nullable)                    | `int`                          | always; `0` for default      |
| `int?` (nullable)                       | `*int`, NO `omitempty`         | `null` when nil; `<int>` otherwise |
| `string` (non-nullable)                 | `string`, NO `omitempty`       | always; `""` for default     |
| `string?` (nullable ref, C# 8+)         | `*string`, NO `omitempty`      | `null` when nil; `"<v>"` otherwise |
| `bool` (non-nullable)                   | `bool`                         | always; `false` for default  |
| `bool?` (nullable)                      | `*bool`, NO `omitempty`        | `null` when nil; otherwise   |
| `DateTime` (non-nullable)               | `time.Time`                    | always; zero-time for default|
| `DateTime?` (nullable)                  | `*time.Time`, NO `omitempty`   | `null` when nil; RFC3339     |
| `List<T>` initialised empty             | `[]T` pre-initialised to `[]T{}` | `[]`                       |
| `List<T>` nullable / left null          | `[]T`, NO `omitempty`          | `null` when nil; `[]` when initialised empty |
| `Dictionary<K,V>` initialised empty     | `map[K]V` pre-initialised      | `{}`                         |
| C# enum (int-backed)                    | typed `int` const block         | integer value                |
| C# enum with `[JsonConverter(StringEnum)]` | typed string const block     | string value                 |

## When Go's `encoding/json` doesn't match by default

### Nil slices marshal to `null`, not `[]`

```go
// WRONG — emits "tags":null
type Series struct {
    Tags []int `json:"tags"`
}
s := Series{} // Tags is nil

// RIGHT — emits "tags":[]
type Series struct {
    Tags []int `json:"tags"`
}
s := Series{Tags: []int{}} // explicitly empty
```

If you cannot guarantee initialisation everywhere (DB load paths, partial
construction), implement `MarshalJSON` on the parent struct that coerces nil
slices to `[]` for the relevant fields. Document the coercion in the
struct doc.

### `time.Time` zero value emits the year-0001 string

That's fine for non-nullable `DateTime` — .NET emits the same value. For
`*time.Time` with nil, the stdlib emits `null` correctly out of the box.

### Custom enum values

When the C# enum uses `[JsonConverter(typeof(StringEnumConverter))]`, the Go
const block must implement `MarshalJSON` / `UnmarshalJSON` to produce the
string form. Example: `healthcheck.Result` already does this.

## Schema nullability ≠ .NET property nullability

These often differ. Examples in the codebase:
- `Series.Year` — DB column is nullable (migration 00020), but C# property is
  `public int Year` (non-nullable). .NET reads NULL → 0, emits `"year": 0`.
- `Series.QualityProfileId` — DB column is NOT NULL, C# property is non-nullable
  `public int QualityProfileId`. Always emits a number.

**The .NET property nullability — not the schema's — determines the Go shape**,
because the property is what produces the JSON wire emission. The Scan/Bind
path is responsible for coercing schema-nullable to property-non-nullable
where they differ (mirror what `Mapper<T>.Scan` in `internal/repo` does).

## Audit checklist for new/modified Go types

Before merging a slice that adds or modifies a JSON-emitting type:

1. **Locate the C# original.** Find the matching property on the C# class.
2. **Note nullability.** Is the C# property `T`, `T?`, or a nullable reference?
3. **Apply the table.** Match the Go shape mechanically.
4. **Drop `omitempty`** unless the field is genuinely net-new in the Go port
   with no .NET counterpart (rare; document in a comment when you do).
5. **Reject `*T` for non-nullable .NET properties.** If the C# is `int`, Go
   must be `int`, even if the DB column is NULL-able. The Scan path coerces.
6. **Pre-initialise slices and maps** before any marshal path. Or override
   `MarshalJSON`.
7. **Snapshot test** when feasible: capture a .NET response for the same data
   and assert byte-equality against the Go output.

## Conversion-note guidance

Future contributors familiar with idiomatic Go will flag fields lacking
`omitempty` as "un-idiomatic." They are right that it's un-idiomatic. The
wire-compat requirement supersedes — add a brief comment to the struct's
docstring when the un-idiomatic pattern is load-bearing:

```go
// Series is the persisted series row + v3 API response shape.
//
// JSON tags intentionally omit `omitempty` on nullable fields per
// go/JSON_CONVENTIONS.md — the Sonarr v3 API contract emits explicit
// `null` for null fields; idiomatic Go would omit them.
type Series struct {
    ...
}
```

## Known non-compliant fields (audit findings, pending fix)

- [series.go:81](internal/series/series.go:81) — `Year *int + omitempty`. C# is
  non-nullable `int`. Fix: change to `int`; Scan coerces schema NULL → 0.
- [series.go:88](internal/series/series.go:88) — `QualityProfileID *int + omitempty`.
  C# is non-nullable `int`; schema is NOT NULL. Fix: change to `int`.
- [episodes.go:43](internal/episodes/episodes.go:43) — `EpisodeFileID *int + omitempty`.
  C# is `int?` (nullable). Fix: drop `omitempty` so `null` emits explicitly.
- Sweep needed across other `*int + omitempty` patterns: series, episodes,
  episodefiles, qualityprofiles, customformats, scenemappings, etc.

A dedicated cleanup slice (or sub-arc) should land before any Phase-5 API
surface that exposes these types. After the sweep, this section can be
emptied and the policy enforced by the audit checklist for new code.

## Related

- [CONVERSION.md](CONVERSION.md) — broader C#→Go conversion rules.
- [STATUS.md](STATUS.md) — current slice state and rolling pointer.
