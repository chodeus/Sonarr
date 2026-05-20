# JSON Wire-Shape Conventions

**Policy: byte-for-byte parity with the Sonarr v3 API JSON shape.**

Authoritative for every Go type that:
- Round-trips through an `/api/v3` or `/api/v5` endpoint, **or**
- Persists as an embedded-JSON column in the database (read/written by both .NET
  legacy and Go-port code paths), **or**
- Is consumed by an external client that already exists against the .NET API
  (React UI, mobile apps, Radarr/Prowlarr/*-arr ecosystem scripts).

## What the C# tree actually emits

The Sonarr API uses **`System.Text.Json` configured with
`DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull`**
([`src/NzbDrone.Common/Serializer/System.Text.Json/STJson.cs:27`](../src/NzbDrone.Common/Serializer/System.Text.Json/STJson.cs), wired into MVC at
[`src/NzbDrone.Host/Startup.cs:107,113`](../src/NzbDrone.Host/Startup.cs)). The legacy Newtonsoft path used in non-API
serialization sets `NullValueHandling = NullValueHandling.Ignore`
([`src/NzbDrone.Common/Serializer/Newtonsoft.Json/Json.cs:26`](../src/NzbDrone.Common/Serializer/Newtonsoft.Json/Json.cs)). Both
omit `null` from the wire output.

Concretely:
- Non-nullable properties → always emitted (including their zero values).
- Nullable properties (`int?` / `DateTime?` / nullable reference types) that
  are null at serialization time → **field omitted from the JSON entirely**,
  NOT emitted as `"field": null`.
- `List<T>` initialised to empty → emitted as `[]`. `List<T>` left null →
  field omitted.

> **Recorded goldens confirm.** Sample C# recordings under
> `test/contract/golden/v3-empty-state/` have zero `: null` emissions; nullable
> fields that are null at serialization time simply do not appear in the body.

## What Go's `encoding/json` does by default

Go's defaults differ in two key ways:
- `*T` nil → `"field": null` (the field IS emitted, just as JSON null).
- Slice/map nil → `"field": null` (same).
- The `omitempty` tag suppresses zero values entirely — including `0`, `""`,
  `false`, nil pointer, AND nil slice/map.

## Current Go-port shape — known divergence

The codebase's nullable-field shape currently follows the **prior policy**
(`*T` without `omitempty`), which emits `"field": null` where C# omits the key.
This is a known wire-shape divergence — the doc previously asserted that C#
emitted null and prescribed "no omitempty" to match; in fact C# omits, so
"no omitempty" produces the opposite of byte-equality.

**Why it has not been swept yet:** the cleanup is a 200+-field migration across
40+ resource types — sequencing under a dedicated audit-B.7-class sweep with a
contract-replay baseline rebuild. Until that sweep lands, expect Go responses
to carry `"field": null` for nullable nil pointers where C# would omit. Most
JSON consumers (React UI, scripted tools, mobile apps) tolerate the divergence
because `undefined` and `null` both flunk truthiness checks the same way, but
strict byte-equal contract tests will catch it.

**Forward guidance for newly added types** (see the audit checklist below):
nullable C# properties → `*T` with `omitempty` in the Go shape so new code does
not enlarge the deferred sweep. Non-nullable C# properties → value type, no
`omitempty` (the zero value is what C# emits).

## Field-by-field rules

The "C# wire emission" column reflects what `System.Text.Json` actually emits
with `WhenWritingNull` in force. The "Forward-guidance Go shape" column is what
new types added to the Go port should adopt to match it byte-for-byte; the
existing codebase frequently does NOT match this and is tracked as the
deferred sweep above.

| .NET shape                                  | Forward-guidance Go shape          | C# wire emission                                  |
|---------------------------------------------|------------------------------------|---------------------------------------------------|
| `int` (non-nullable)                        | `int`                              | always; `0` for default                           |
| `int?` (nullable)                           | `*int`, `omitempty`                | **omitted when nil**; `<int>` otherwise           |
| `string` (non-nullable)                     | `string`                           | always; `""` for default                          |
| `string?` (nullable ref, C# 8+)             | `*string`, `omitempty`             | **omitted when null**; `"<v>"` otherwise          |
| `bool` (non-nullable)                       | `bool`                             | always; `false` for default                       |
| `bool?` (nullable)                          | `*bool`, `omitempty`               | **omitted when nil**; otherwise                   |
| `DateTime` (non-nullable)                   | `time.Time`                        | always; zero-time for default                     |
| `DateTime?` (nullable)                      | `*time.Time`, `omitempty`          | **omitted when nil**; RFC3339 otherwise           |
| `List<T>` initialised empty                 | `[]T` pre-initialised to `[]T{}`   | `[]`                                              |
| `List<T>` nullable / left null              | `[]T`, `omitempty`                 | **omitted when nil**; `[]` when initialised empty |
| `Dictionary<K,V>` initialised empty         | `map[K]V` pre-initialised          | `{}`                                              |
| `Dictionary<K,V>` nullable / left null      | `map[K]V`, `omitempty`             | **omitted when nil**                              |
| C# enum (int-backed)                        | typed `int` const block            | integer value                                     |
| C# enum with `[JsonConverter(StringEnum)]`  | typed string const block           | string value                                      |

## When Go's `encoding/json` doesn't match by default

### Initialised-empty slices vs nil slices

Go's `json.Marshal` emits nil slices as `null` and empty-initialised slices as
`[]`. C# emits initialised-empty `List<T>` as `[]` and omits nullable-null
`List<T>` (under `WhenWritingNull`). To match both shapes from Go, pair the
field design with explicit initialisation at the relevant constructors:

```go
// MATCHES C# initialised-empty List<T>:
type Series struct {
    Tags []int `json:"tags"` // pre-initialise to []int{} at every construction site
}
s := Series{Tags: []int{}} // emits "tags":[]

// MATCHES C# nullable-null List<T>:
type Series struct {
    OptionalTags []int `json:"optionalTags,omitempty"` // nil → omitted, empty → "[]"
}
```

If you cannot guarantee initialisation everywhere (DB load paths, partial
construction), implement `MarshalJSON` on the parent struct that coerces nil
slices to `[]` for fields that should never appear as `null`. Document the
coercion in the struct doc.

### `time.Time` zero value emits the year-0001 string

That's fine for non-nullable `DateTime` — .NET emits the same value. For
nullable `DateTime?`, C# omits when null. Go's `*time.Time` with `omitempty`
matches.

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
4. **Add `omitempty` when the C# property is nullable.** New types contribute
   to the deferred sweep only when they break this rule — keep the door from
   widening.
5. **Reject `*T` for non-nullable .NET properties.** If the C# is `int`, Go
   must be `int`, even if the DB column is NULL-able. The Scan path coerces.
6. **Pre-initialise slices and maps when C# initialises them as empty**
   (before any marshal path) — `[]T{}` for `List<T>` that should render as
   `[]`. Or override `MarshalJSON`.
7. **Snapshot test** when feasible: capture a .NET response for the same data
   and assert byte-equality against the Go output.

## Conversion-note guidance

Future contributors will see the existing codebase using `*T` without
`omitempty` and may assume that's the intended pattern. It is not — that's the
deferred-divergence area. New types should adopt the forward-guidance shape in
the table above. Add a brief comment to the struct's docstring when the
nullable-field pattern is non-obvious:

```go
// MyResource mirrors the C# MyResource. Nullable C# properties use
// `*T,omitempty` to match the WhenWritingNull omission posture; the older
// resources around this package use `*T` without omitempty and are queued
// under the JSON_CONVENTIONS.md "known divergence" sweep.
type MyResource struct {
    Foo *int `json:"foo,omitempty"` // mirrors C# `public int? Foo`
}
```

## Known divergence

**Deferred B.7-class sweep — not blocking releases.** The Go-port emits
`"field": null` for nullable nil pointers where C# omits the key. The
divergence surface is much smaller than the J161 doc rewrite originally
estimated — re-audited at J165 with `grep -rE '\*[a-zA-Z.]+\s+\`json:'
internal/api/`:

- **v3 wire boundary:** the v3 handler resource types (`internal/api/v3/*.go`)
  are mostly omitempty-correct already. After the J164 + J165 fixes on
  `FileSystemModel.Size` and `FileSystemModel.LastModified`, the v3 handler
  surface has **zero** known pointer-without-`omitempty` fields.
- **Internal / persistence types:** ~129 `*T` fields without `omitempty`
  across the rest of `internal/` (series, episodes, episodefiles, indexers,
  scenemappings, broadcasthenet/api, torznab, delayprofiles, etc.).
  Categories:
  - **Embedded-JSON DB column shapes** (e.g., `series.Series` time fields,
    `episodefiles.MediaInfo`): when Go writes back a row whose C# original
    had a null property, Go's JSON emits `"field": null` where C#'s
    Newtonsoft (`NullValueHandling.Ignore`) would omit. Newtonsoft on read
    tolerates both shapes, so this affects on-disk JSON column contents,
    not user-facing wire shape.
  - **External upstream protocols** (e.g., `broadcasthenet/api.go` BTN
    response struct, `scenemappings/proxy.go` SkyHook): these mirror
    upstream wire formats, NOT Sonarr's API. They should NOT be flipped to
    `omitempty` without matching what the upstream wire actually emits.
  - **Internal config / partial-update shapes** (e.g.,
    `indexers/cached_settings_provider.go` `SeedCriteria`): used inside
    the indexer pipeline; not directly v3-emitted.
- **Contract-replay baseline:** 9 known-failing goldens per slice 5.J7/J8.
  Per the original B.1 closure note, these are "mostly environment-dependent
  C# recording artifacts" — re-recording or expanding `redact_paths` is the
  path forward; the null-vs-missing divergence does not appear to be the
  primary cause given how few v3-boundary divergences actually exist.

**Why the bulk sweep stays deferred:** the persistence-layer and external-
upstream surfaces need per-type analysis (some `*T` fields are deliberate,
mirroring upstream `*T` shapes), and the embedded-JSON column changes need a
read-tolerant migration plan. Picking these off case-by-case as types are
touched in feature work is preferable to one big migration.

**What changed from the prior policy.** The prior version of this doc claimed
C# emitted nullable nulls as `null` and prescribed "no omitempty" to match.
That assertion was empirically wrong (confirmed by `STJson.cs:27` + recorded
goldens). The codebase was written against the wrong premise. J161
(2026-05-21) rewrote the rationale + field-by-field table to reflect C#
reality, recorded the existing codebase shape as a known divergence, and
flipped the forward-guidance prescription to `omitempty` on nullable C#
properties. The bulk migration of existing types stays deferred under the
B.7-class sweep.

## Related

- [CONVERSION.md](CONVERSION.md) — broader C#→Go conversion rules.
- [STATUS.md](STATUS.md) — current slice state and rolling pointer.
- [AUDIT_FIX_BACKLOG.md](AUDIT_FIX_BACKLOG.md) — the deferred B.7-class
  divergence sweep is tracked here under "Notable follow-ups".
