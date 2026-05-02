package datastore

import (
	"context"
	"database/sql"
	"sort"
	"testing"
)

// expectedTablesAfterMigration1 lists every table the C# Migration/001 creates
// in the *main* DB, in name-sorted order. The Go port's goose migration must
// produce the same table set; subsequent migration PRs grow this list.
//
// The set is unchanged at versions 2-5 (003 and 004 drop-and-recreate
// SceneMappings and History but the table names persist).
var expectedTablesAfterMigration1 = []string{
	"Config",
	"EpisodeFiles",
	"Episodes",
	"History",
	"Indexers",
	"NamingConfig",
	"Notifications",
	"QualityProfiles",
	"QualitySizes",
	"RootFolders",
	"SceneMappings",
	"ScheduledTasks",
	"Seasons",
	"Series",
}

// headVersion is the highest main-DB migration this PR has ported.
// headLogVersion is the highest log-DB migration. Bump both as new migrations
// land. Tests use these to call Migrate() and assert the version a head-
// checkpoint test expects to see.
//
// Note: log-DB versions are sparse — only the C# migrations whose
// LogDbUpgrade() override is non-empty get a Go counterpart. Today: 1, 6.
const (
	headVersion    int64 = 79
	headLogVersion int64 = 64
)

// columnSpec is what we assert about each column. Type strings are the
// dialect-canonical form returned by introspection; differences between
// SQLite and Postgres are wrapped in dialectColumns below.
type columnSpec struct {
	name     string
	wantType string
	notNull  bool
}

// dialectColumns returns the per-dialect expected column shape for a few
// representative tables — Config (the simplest), Series (the most columns),
// and a sample of nullable columns. Hitting all 14 tables × all columns is
// out of scope for this PR; the structural assertion below covers
// existence-of-table for every table.
func dialectColumns(engine Engine) (table string, want []columnSpec) {
	switch engine {
	case EngineSQLite:
		return "Config", []columnSpec{
			// SQLite type names come back uppercase from pragma_table_info.
			{name: "Id", wantType: "INTEGER", notNull: true},
			{name: "Key", wantType: "TEXT", notNull: true},
			{name: "Value", wantType: "TEXT", notNull: true},
		}
	case EnginePostgres:
		return "Config", []columnSpec{
			// Postgres information_schema returns lowercase canonical types.
			{name: "Id", wantType: "integer", notNull: true},
			{name: "Key", wantType: "text", notNull: true},
			{name: "Value", wantType: "text", notNull: true},
		}
	}
	return "", nil
}

func TestMigrate_SQLite_ToVersion1(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)
	if err := MigrateTo(t.Context(), db, 1); err != nil {
		t.Fatalf("MigrateTo(1): %v", err)
	}
	assertVersion(t, db, 1)

	got := listTablesSQLite(t, db.SQL)
	if !equalStringSlices(got, expectedTablesAfterMigration1) {
		t.Errorf("tables mismatch\n got: %v\nwant: %v", got, expectedTablesAfterMigration1)
	}

	table, want := dialectColumns(EngineSQLite)
	gotCols := listColumnsSQLite(t, db.SQL, table)
	if !equalColumnSpecs(gotCols, want) {
		t.Errorf("columns(%s) mismatch\n got: %+v\nwant: %+v", table, gotCols, want)
	}
}

func TestMigrate_SQLite_ToHead(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	assertVersion(t, db, headVersion)

	// IX_Series_TvdbId and IX_Series_TitleSlug remain at HEAD (v19
	// idempotent restore confirmed them). IX_Series_ImdbId stays dropped
	// (v2 dropped it, never restored). IX_Series_TvRageId was dropped
	// at v2 but v45 added a non-unique IX_Series_TvRageId.
	indexes := listIndexesSQLite(t, db.SQL, "Series")
	for _, kept := range []string{"IX_Series_TvdbId", "IX_Series_TitleSlug"} {
		if !containsString(indexes, kept) {
			t.Errorf("Series missing kept index %q: %v", kept, indexes)
		}
	}
	if containsString(indexes, "IX_Series_ImdbId") {
		t.Errorf("Series still has dropped IX_Series_ImdbId: %v", indexes)
	}

	// Version-3 reshaped SceneMappings: dropped CleanTitle, SceneName;
	// added SearchTerm, ParseTerm.
	sm := columnNames(listColumnsSQLite(t, db.SQL, "SceneMappings"))
	for _, want := range []string{"Id", "TvdbId", "SeasonNumber", "SearchTerm", "ParseTerm"} {
		if !containsString(sm, want) {
			t.Errorf("SceneMappings missing column %q (post-v3): %v", want, sm)
		}
	}
	for _, gone := range []string{"CleanTitle", "SceneName"} {
		if containsString(sm, gone) {
			t.Errorf("SceneMappings still has dropped column %q (post-v3): %v", gone, sm)
		}
	}

	// Version-4 reshaped History; version-5 added EventType. The
	// resulting columns are the v4 set plus EventType.
	hist := columnNames(listColumnsSQLite(t, db.SQL, "History"))
	for _, want := range []string{"Id", "EpisodeId", "SeriesId", "SourceTitle", "Date", "Quality", "Data", "EventType"} {
		if !containsString(hist, want) {
			t.Errorf("History missing column %q (post-v5): %v", want, hist)
		}
	}
	for _, gone := range []string{"NzbTitle", "Indexer", "NzbInfoUrl", "ReleaseGroup"} {
		if containsString(hist, gone) {
			t.Errorf("History still has dropped column %q (post-v4): %v", gone, hist)
		}
	}

	// HEAD assertions cover only deltas that survive to HEAD. Intermediate
	// state (e.g. v9 dropping SeasonFolderFormat then v30 re-adding it,
	// v10 adding Monitored to Seasons then v21 dropping the table) is
	// out of scope here — those would be ToVersion-N tests if we needed
	// them.
	naming := columnNames(listColumnsSQLite(t, db.SQL, "NamingConfig"))
	if !containsString(naming, "RenameEpisodes") {
		t.Errorf("NamingConfig missing RenameEpisodes column: %v", naming)
	}
	if containsString(naming, "UseSceneName") {
		t.Errorf("NamingConfig still has UseSceneName: %v", naming)
	}

	series := columnNames(listColumnsSQLite(t, db.SQL, "Series"))
	for _, gone := range []string{"BacklogSetting", "CustomStartDate"} {
		if containsString(series, gone) {
			t.Errorf("Series still has %q: %v", gone, series)
		}
	}
	for _, want := range []string{"Year", "Seasons"} {
		if !containsString(series, want) {
			t.Errorf("Series missing %q (post-v20): %v", want, series)
		}
	}

	// Episodes.Ignored dropped at v11 and stays gone.
	ep := columnNames(listColumnsSQLite(t, db.SQL, "Episodes"))
	if containsString(ep, "Ignored") {
		t.Errorf("Episodes still has Ignored: %v", ep)
	}

	// Episodes.AirDate column-type pivot: TEXT after v15.
	for _, c := range listColumnsSQLite(t, db.SQL, "Episodes") {
		if c.name == "AirDate" && c.wantType != "TEXT" {
			t.Errorf("AirDate type = %q, want TEXT (post-v15)", c.wantType)
		}
	}

	// Tables introduced after v1: Blacklist (v28), Metadata (v39),
	// MetadataFiles (v39), QualityDefinitions (v37). Tables dropped:
	// Seasons (v21), QualitySizes (v37).
	tables := listTablesSQLite(t, db.SQL)
	for _, want := range []string{"Blacklist", "Metadata", "MetadataFiles", "QualityDefinitions"} {
		if !containsString(tables, want) {
			t.Errorf("missing table %q at HEAD: %v", want, tables)
		}
	}
	for _, gone := range []string{"Seasons", "QualitySizes"} {
		if containsString(tables, gone) {
			t.Errorf("dropped table %q still present at HEAD: %v", gone, tables)
		}
	}
}

func TestMigrate_SQLite_Idempotent(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	assertVersion(t, db, headVersion)
}

func TestMigrate_Postgres_ToVersion1(t *testing.T) {
	// Postgres tests share the spike DB and rebuild the public schema
	// for hermeticity, so they cannot run in parallel with each other.
	// SQLite tests still run in parallel.
	db := openPostgres(t)

	if err := MigrateTo(t.Context(), db, 1); err != nil {
		t.Fatalf("MigrateTo(1): %v", err)
	}
	assertVersion(t, db, 1)

	got := listTablesPostgres(t, db.SQL)
	if !equalStringSlices(got, expectedTablesAfterMigration1) {
		t.Errorf("tables mismatch\n got: %v\nwant: %v", got, expectedTablesAfterMigration1)
	}

	table, want := dialectColumns(EnginePostgres)
	gotCols := listColumnsPostgres(t, db.SQL, table)
	if !equalColumnSpecs(gotCols, want) {
		t.Errorf("columns(%s) mismatch\n got: %+v\nwant: %+v", table, gotCols, want)
	}
}

func TestMigrate_Postgres_ToHead(t *testing.T) {
	// See note on TestMigrate_Postgres_ToVersion1 — no t.Parallel().
	db := openPostgres(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	assertVersion(t, db, headVersion)

	indexes := listIndexesPostgres(t, db.SQL, "Series")
	if containsString(indexes, "IX_Series_ImdbId") {
		t.Errorf("Series still has dropped IX_Series_ImdbId: %v", indexes)
	}

	sm := columnNames(listColumnsPostgres(t, db.SQL, "SceneMappings"))
	for _, want := range []string{"Id", "TvdbId", "SeasonNumber", "SearchTerm", "ParseTerm"} {
		if !containsString(sm, want) {
			t.Errorf("SceneMappings missing column %q (post-v3): %v", want, sm)
		}
	}
	for _, gone := range []string{"CleanTitle", "SceneName"} {
		if containsString(sm, gone) {
			t.Errorf("SceneMappings still has dropped column %q (post-v3): %v", gone, sm)
		}
	}

	hist := columnNames(listColumnsPostgres(t, db.SQL, "History"))
	for _, want := range []string{"Id", "EpisodeId", "SeriesId", "SourceTitle", "Date", "Quality", "Data", "EventType"} {
		if !containsString(hist, want) {
			t.Errorf("History missing column %q (post-v5): %v", want, hist)
		}
	}

	// HEAD assertions only — see comment on the SQLite counterpart for
	// why intermediate states aren't asserted.
	naming := columnNames(listColumnsPostgres(t, db.SQL, "NamingConfig"))
	if !containsString(naming, "RenameEpisodes") {
		t.Errorf("NamingConfig missing RenameEpisodes: %v", naming)
	}
	if containsString(naming, "UseSceneName") {
		t.Errorf("NamingConfig still has UseSceneName: %v", naming)
	}
	series := columnNames(listColumnsPostgres(t, db.SQL, "Series"))
	for _, gone := range []string{"BacklogSetting", "CustomStartDate"} {
		if containsString(series, gone) {
			t.Errorf("Series still has %q: %v", gone, series)
		}
	}
	for _, want := range []string{"Year", "Seasons"} {
		if !containsString(series, want) {
			t.Errorf("Series missing %q (post-v20): %v", want, series)
		}
	}
	ep := columnNames(listColumnsPostgres(t, db.SQL, "Episodes"))
	if containsString(ep, "Ignored") {
		t.Errorf("Episodes still has Ignored: %v", ep)
	}
	for _, want := range []string{"AirDate", "AirDateUtc"} {
		if !containsString(ep, want) {
			t.Errorf("Episodes missing %q (post-v15)", want)
		}
	}
	tables := listTablesPostgres(t, db.SQL)
	for _, want := range []string{"Blacklist", "Metadata", "MetadataFiles", "QualityDefinitions"} {
		if !containsString(tables, want) {
			t.Errorf("missing table %q at HEAD: %v", want, tables)
		}
	}
	for _, gone := range []string{"Seasons", "QualitySizes"} {
		if containsString(tables, gone) {
			t.Errorf("dropped table %q still at HEAD: %v", gone, tables)
		}
	}
}

// openPostgres + resetPostgresSchema is the shared setup for any Postgres
// migration test — opens against the spike DB and drops public so the test
// is hermetic. Skips when SONARR_PG_DSN is unset.
func openPostgres(t *testing.T) *DB {
	t.Helper()
	dsn := pgDSN(t)
	c, err := configFromSpikeDSN(dsn)
	if err != nil {
		t.Fatalf("configFromSpikeDSN: %v", err)
	}
	db, err := Open(t.Context(), c)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	resetPostgresSchema(t, db.SQL)
	return db
}

func assertVersion(t *testing.T, db *DB, want int64) {
	t.Helper()
	got, err := CurrentVersion(t.Context(), db)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if got != want {
		t.Errorf("CurrentVersion = %d, want %d", got, want)
	}
}

// --- introspection helpers ---

func listTablesSQLite(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version' ORDER BY name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	return scanStrings(t, rows)
}

func listTablesPostgres(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name <> 'goose_db_version' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	return scanStrings(t, rows)
}

func listColumnsSQLite(t *testing.T, db *sql.DB, table string) []columnSpec {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name, type, "notnull" FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []columnSpec
	for rows.Next() {
		var c columnSpec
		var nn int
		if err := rows.Scan(&c.name, &c.wantType, &nn); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		c.notNull = nn != 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func listColumnsPostgres(t *testing.T, db *sql.DB, table string) []columnSpec {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `
		SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		ORDER BY column_name`, table)
	if err != nil {
		t.Fatalf("information_schema.columns(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []columnSpec
	for rows.Next() {
		var c columnSpec
		var nullable string
		if err := rows.Scan(&c.name, &c.wantType, &nullable); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		c.notNull = nullable == "NO"
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func resetPostgresSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	// The shared sonarr_spike DB may host residue from earlier spike runs
	// (e.g. the pgx spike's "counter" table). Wipe public entirely so the
	// migration runner starts on a clean slate. The spike re-creates its
	// own state on every run, so this doesn't disrupt it.
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS public CASCADE`,
		`CREATE SCHEMA public`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("reset %q: %v", q, err)
		}
	}
}

func scanStrings(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalColumnSpecs(a, b []columnSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func columnNames(cols []columnSpec) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.name
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func listIndexesSQLite(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_autoindex_%' ORDER BY name`,
		table)
	if err != nil {
		t.Fatalf("query indexes(%s): %v", table, err)
	}
	return scanStrings(t, rows)
}

func listIndexesPostgres(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT indexname FROM pg_indexes WHERE schemaname='public' AND tablename=$1 ORDER BY indexname`,
		table)
	if err != nil {
		t.Fatalf("query indexes(%s): %v", table, err)
	}
	return scanStrings(t, rows)
}

// --- log-DB tests ---
//
// Postgres log-DB testing requires a separate physical database (the C# side
// uses sonarr-log alongside sonarr-main). Provisioning that for tests is a
// later PR. For now log-DB coverage is SQLite-only; the migration files for
// Postgres exist and were vetted via go vet.

func openSQLiteLog(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(t.Context(), Config{
		Engine: EngineSQLite,
		Role:   RoleLog,
		Path:   dir + "/logs.db",
	})
	if err != nil {
		t.Fatalf("Open SQLite log: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrate_SQLite_Log_ToVersion1(t *testing.T) {
	t.Parallel()
	db := openSQLiteLog(t)
	if err := MigrateTo(t.Context(), db, 1); err != nil {
		t.Fatalf("MigrateTo(1): %v", err)
	}
	assertVersion(t, db, 1)

	// Logs is the only table at v1; no IX_Logs_Time index yet (added in v6).
	tables := listTablesSQLite(t, db.SQL)
	if !equalStringSlices(tables, []string{"Logs"}) {
		t.Errorf("log-DB tables at v1 = %v, want [Logs]", tables)
	}
	idx := listIndexesSQLite(t, db.SQL, "Logs")
	if containsString(idx, "IX_Logs_Time") {
		t.Errorf("Logs already has IX_Logs_Time at v1 (should land in v6): %v", idx)
	}
}

func TestMigrate_SQLite_Log_ToHead(t *testing.T) {
	t.Parallel()
	db := openSQLiteLog(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	assertVersion(t, db, headLogVersion)

	// v6 dropped + recreated Logs and added IX_Logs_Time.
	idx := listIndexesSQLite(t, db.SQL, "Logs")
	if !containsString(idx, "IX_Logs_Time") {
		t.Errorf("Logs missing IX_Logs_Time index (post-v6): %v", idx)
	}

	// Column shape unchanged from v1.
	cols := columnNames(listColumnsSQLite(t, db.SQL, "Logs"))
	for _, want := range []string{"Id", "Message", "Time", "Logger", "Level"} {
		if !containsString(cols, want) {
			t.Errorf("Logs missing column %q (post-v6): %v", want, cols)
		}
	}
}

// --- backfill correctness ---
//
// Migration 010 is the first port-time migration with a non-trivial UPDATE-
// based backfill: it adds a Monitored column and derives its value from the
// legacy Ignored column. Schema-only assertions don't catch a busted backfill
// (the column exists either way), so this test seeds rows at the pre-10 state
// and checks the post-10 values directly. The same pattern applies to every
// future backfill migration — copy this test and adjust the seed/assertion.

// TestMigrate_SQLite_Backfill_020_SeriesSeasonsJSON locks in the
// embedded-document JSON shape for Series.Seasons. This shape — a JSON array
// of {"seasonNumber": N, "monitored": <bool>} objects — is the contract that
// every later migration that reads or extends Series.Seasons must preserve
// byte-for-byte. A regression here corrupts existing user databases on
// upgrade.
func TestMigrate_SQLite_Backfill_020_SeriesSeasonsJSON(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)

	// Bring schema up to v19, then seed a Series row with two Seasons.
	// Season 0 (specials) → forced monitored=false even if Monitored=1.
	// Season 1 → monitored=true. Season 2 → monitored=false.
	if err := MigrateTo(t.Context(), db, 19); err != nil {
		t.Fatalf("MigrateTo(19): %v", err)
	}
	for _, q := range []string{
		// Series at v19 has TvdbId, TvRageId, ImdbId, Title, TitleSlug, CleanTitle,
		// Status, Images, Path, Monitored, QualityProfileId, SeasonFolder,
		// Runtime, SeriesType, UseSceneNumbering as NOT NULL columns.
		`INSERT INTO "Series" ("TvdbId", "TvRageId", "ImdbId", "Title", "TitleSlug", "CleanTitle", "Status", "Images", "Path", "Monitored", "QualityProfileId", "SeasonFolder", "Runtime", "SeriesType", "UseSceneNumbering") VALUES (1, 1, 'tt1', 'Test Show', 'test-show', 'testshow', 0, '[]', '/data', 1, 1, 1, 30, 0, 0)`,
		`INSERT INTO "Seasons" ("SeriesId", "SeasonNumber", "Monitored") VALUES (1, 0, 1)`,
		`INSERT INTO "Seasons" ("SeriesId", "SeasonNumber", "Monitored") VALUES (1, 1, 1)`,
		`INSERT INTO "Seasons" ("SeriesId", "SeasonNumber", "Monitored") VALUES (1, 2, 0)`,
	} {
		if _, err := db.SQL.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}

	// Apply v20: Year + Seasons columns added; Seasons JSON backfilled.
	if err := MigrateTo(t.Context(), db, 20); err != nil {
		t.Fatalf("MigrateTo(20): %v", err)
	}

	var got string
	if err := db.SQL.QueryRowContext(t.Context(),
		`SELECT "Seasons" FROM "Series" WHERE "TvdbId" = 1`).Scan(&got); err != nil {
		t.Fatalf("read Series.Seasons: %v", err)
	}

	// The JSON shape must be exactly this string. Order is deterministic
	// because SQLite's json_group_array follows correlated subquery row
	// order (which is rowid order for the unindexed Seasons table).
	want := `[{"seasonNumber":0,"monitored":false},` +
		`{"seasonNumber":1,"monitored":true},` +
		`{"seasonNumber":2,"monitored":false}]`
	if got != want {
		t.Errorf("Series.Seasons JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestMigrate_SQLite_Backfill_010_MonitoredFromIgnored(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)

	// Bring schema up to v9 — Episodes and Seasons exist with Ignored
	// but not yet Monitored.
	if err := MigrateTo(t.Context(), db, 9); err != nil {
		t.Fatalf("MigrateTo(9): %v", err)
	}

	// Seed two episode rows and two season rows with mixed Ignored values.
	for _, q := range []string{
		`INSERT INTO "Episodes" ("TvDbEpisodeId", "SeriesId", "SeasonNumber", "EpisodeNumber", "Ignored") VALUES (1, 1, 1, 1, 0)`,
		`INSERT INTO "Episodes" ("TvDbEpisodeId", "SeriesId", "SeasonNumber", "EpisodeNumber", "Ignored") VALUES (2, 1, 1, 2, 1)`,
		`INSERT INTO "Seasons" ("SeriesId", "SeasonNumber", "Ignored") VALUES (1, 1, 0)`,
		`INSERT INTO "Seasons" ("SeriesId", "SeasonNumber", "Ignored") VALUES (1, 2, 1)`,
	} {
		if _, err := db.SQL.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}

	// Now migrate to v10; the backfill UPDATEs should set Monitored as the
	// inverse of Ignored.
	if err := MigrateTo(t.Context(), db, 10); err != nil {
		t.Fatalf("MigrateTo(10): %v", err)
	}

	cases := []struct {
		table       string
		key, keyVal string
		want        int
	}{
		{"Episodes", "TvDbEpisodeId", "1", 1}, // Ignored=0 → Monitored=1
		{"Episodes", "TvDbEpisodeId", "2", 0}, // Ignored=1 → Monitored=0
		{"Seasons", "SeasonNumber", "1", 1},
		{"Seasons", "SeasonNumber", "2", 0},
	}
	for _, c := range cases {
		var got int
		row := db.SQL.QueryRowContext(t.Context(),
			`SELECT "Monitored" FROM "`+c.table+`" WHERE "`+c.key+`" = `+c.keyVal)
		if err := row.Scan(&got); err != nil {
			t.Fatalf("%s.%s=%s: %v", c.table, c.key, c.keyVal, err)
		}
		if got != c.want {
			t.Errorf("%s.%s=%s: Monitored = %d, want %d", c.table, c.key, c.keyVal, got, c.want)
		}
	}
}
