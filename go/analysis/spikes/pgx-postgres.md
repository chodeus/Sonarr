# Spike: Postgres via `jackc/pgx/v5`

**Status:** ✅ green — recommended driver for the Go port.

## Question

Sonarr v5 uses `Npgsql` for Postgres on the C# side. The Go port wants `jackc/pgx/v5` because (a) it's the de-facto standard, (b) it has a native `pgxpool.Pool` for connection pooling, (c) it supports `database/sql` *and* its own faster non-stdlib API, and (d) it has comprehensive JSONB support — which Sonarr leans on for embedded-document settings columns.

## Surface Sonarr exercises

From a static read of `src/NzbDrone.Core/Datastore/**` (cross-checked against the Datastore analysis card):

- Connection pool with bounded size (Postgres has hard `max_connections` defaults around 100; Sonarr stays well below)
- BIGSERIAL primary keys + `RETURNING Id` on INSERT (replaces SQLite's `last_insert_rowid()`)
- `JSONB` columns + `->`, `->>`, `jsonb_set`, `jsonb_array_length`, `jsonb_set` chained for embedded-document operations
- `ILIKE` for case-insensitive matching (replaces SQLite's `LIKE` + `COLLATE NOCASE`)
- Cascading deletes via `ON DELETE CASCADE`
- Multi-statement transactions under contention with read-committed isolation

## Verdict

`jackc/pgx/v5` v5.9.2 covers the surface. The spike at `go/spikes/postgres/pg_spike_test.go` runs against a local PostgreSQL 16 server and exercises each item:

```
$ SONARR_PG_DSN="postgres:///sonarr_spike" go test -count=1 -race ./spikes/postgres/...
ok      sonarr.tv/sonarr/spikes/postgres  1.657s
```

The spike self-skips when `SONARR_PG_DSN` is unset, so it's safe to leave in the default `go test ./...` lane in CI. CI lane gets a Postgres service container later.

## Notes for the porter

- **Two APIs.** `pgxpool` is the high-level connection pool (use this in Sonarr code). `pgx`'s lower-level `Conn` is for one-off connections. There is also a `database/sql` adapter (`stdlib.OpenDBFromPool`) if we want to share repository code between SQLite and Postgres — likely the right call for the Go port so the dialect-bridging happens at one layer, not two.
- **Parameter placeholders** are `$1`, `$2`, … not `?`. Hand-written SQL must use the right form per dialect; the abstraction layer must handle this. (FluentMigrator did it via the dialect bridge in `NzbDroneSQLiteProcessor`; goose handles dialects but each migration file picks one dialect — we'll likely need parallel `.sql` files per migration, one for SQLite and one for Postgres, where they diverge.)
- **`TIMESTAMPTZ` vs `DATETIME`.** The C# `NzbDroneSQLiteTypeMap` translates `DateTimeOffset → DATETIME` so one C# property type works for both engines. In Go, `time.Time` round-trips correctly to both `TIMESTAMPTZ` (Postgres) and `DATETIME` (SQLite, stored as text), but the SQLite text format must be controlled — pgx and modernc.org/sqlite use different defaults. The Datastore analysis card notes this; the porter must verify on first use.
- **JSONB shape parity.** When storing embedded documents, the C# side serializes via Newtonsoft and stores as `text` in SQLite, `jsonb` in Postgres. JSONB *normalizes* (key order, whitespace), so byte-equality at the column level is impossible — the contract is at the JSON-doc semantic level. This must be reflected in schema-diff tooling: dump as `jsonb` → `text` round-trip before comparing.
- **No CGo.** pgx is pure Go. Cross-compilation works.

## Open follow-ups

- **Schema-diff tool:** the Phase 0.5 `schema/postgres.sql` deliverable will let us write a `pg_dump --schema-only` diff between C#-migrated and Go-migrated empty DBs. Add to the migration ledger work in 0.5b.
- **Postgres extensions:** Datastore-Q11 (Q-11) asks whether any Postgres extensions are required (uuid-ossp, pgcrypto, etc.). The spike used none — verify by spinning up an unprivileged role and replaying every migration.
