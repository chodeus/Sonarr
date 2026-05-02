# Phase 0.5 — Deep-dive analysis deliverables

Everything in this tree is **read-only documentation produced from the C# source**, not Go code. The port references these documents the way a translator references a glossary: every conversion PR cites the analysis card it implements.

The phase is not "done" until every checklist below is green and `questions.md` is empty.

## Layout

```
analysis/
  README.md                   # this file
  questions.md                # open questions discovered while reading
  subsystems/                 # one card per src/NzbDrone.Core/<Dir> + a few non-Core projects
    common.md
    datastore.md
    tv.md
    parser.md
    indexers.md
    download.md
    ...
  schema/
    sqlite.sql                # captured from a freshly migrated empty DB
    postgres.sql              # ditto
    sqlite-seeded.sql         # captured after integration-test fixtures applied
    postgres-seeded.sql       # ditto
  migrations/
    ledger.md                 # one row per FluentMigrator file with semantics + reversibility
  rest/
    inventory.md              # static cross-reference of all routes
    corpus/                   # input → ../test/contract/corpus/ once recorded
    golden/                   # output → ../test/contract/golden/ once recorded
  signalr/
    inventory.md              # all hub methods + server events
  parser/
    regex-corpus.md           # every regex with its source file + test inputs
  providers/
    indexers.md
    download-clients.md
    notifications.md
    import-lists.md
    metadata-sources.md
    metadata-writers.md
  embedded-docs/
    catalogue.md              # every IEmbeddedDocument-stored type, field-by-field
  jobs/
    catalogue.md              # every IExecute<TCommand> + scheduled job
  native/
    inventory.md              # NzbDrone.Mono + NzbDrone.Windows + RuntimePatches
  spikes/
    sqlite-modernc.md         # spike write-up
    pgx-postgres.md
    signalr-philippseith.md
    imaging-imagesharp.md
    ffmpeg-exec.md
    anacrolix-torrent.md
```

## Card template — `subsystems/*.md`

Every subsystem card uses this template. The card is checked into the repo and is the source of truth a Phase 1+ porter cites in PR descriptions.

```markdown
# <Subsystem>

**Source:** `src/NzbDrone.Core/<Dir>/**` (or other path)
**Target Go package:** `go/internal/<dir>/`
**Status:** drafted | reviewed | signed-off

## Purpose
One paragraph: what does this subsystem do, why does it exist, who consumes it.

## Public types
| Type | File | Kind | Notes |
|------|------|------|-------|
| `Series` | `Tv/Series.cs` | model | … |

## Public services
| Type | Methods | Side effects | Notes |
|------|---------|--------------|-------|

## DB tables touched
- `Series` — read/write
- `Seasons` — read/write
- …

## Events raised (IEventAggregator)
| Event | Payload | Producers | Consumers |

## Events consumed
| Event | Handler | Behavior |

## Config keys
- `Sonarr.X.Y` (read at …)

## Jobs / commands (`IExecute<TCommand>`)
| Command | Trigger | Frequency | Idempotent? |

## External services
- TheTVDB (Skyhook) — `https://…`
- …

## Notable quirks / TODOs / HACKs found in source
- `Tv/SeriesService.cs:142` — comment says “workaround for X” …
- …

## Test files (the port must keep passing equivalents)
- `src/NzbDrone.Core.Test/Tv/SeriesServiceFixture.cs`
- …

## Open questions
- Q: …  → resolved (see questions.md#Q-NN) | OR | unresolved (blocks Phase N)
```

## Acceptance gates

Before Phase 1 may begin:

- [ ] One filled-in card exists for every directory under `src/NzbDrone.Core/`.
- [ ] Cards exist for `NzbDrone.Common`, `Sonarr.Http`, `Sonarr.Api.V3`, `Sonarr.Api.V5`, `NzbDrone.SignalR`, `NzbDrone.Host`, `NzbDrone.Mono`, `NzbDrone.Windows`, `NzbDrone.Update`.
- [ ] `schema/{sqlite,postgres}.sql` regenerate cleanly from the C# binary.
- [ ] `migrations/ledger.md` covers every file under `src/NzbDrone.Core/Datastore/Migration/`.
- [ ] `rest/inventory.md` lists every `[HttpGet/Post/Put/Delete]` in V3 and V5.
- [ ] `signalr/inventory.md` lists every hub method and server-pushed event.
- [ ] `parser/regex-corpus.md` enumerates every regex in `src/NzbDrone.Core/Parser/`.
- [ ] `providers/*.md` enumerate every concrete indexer, download client, notification, import list, metadata source, and metadata writer.
- [ ] `embedded-docs/catalogue.md` lists every `IEmbeddedDocument` type with field-by-field JSON shape.
- [ ] `jobs/catalogue.md` lists every `IExecute<TCommand>` and every scheduled job.
- [ ] `native/inventory.md` lists every Mono / Windows / RuntimePatches surface with the OS call it makes.
- [ ] `spikes/*.md` exist with green/red verdicts for each high-risk dependency swap.
- [ ] `questions.md` is empty (every question resolved with citation).
