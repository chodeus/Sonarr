-- Ports Migration/100. The C# migration does TWO things: alters
-- SceneMappings.SeasonNumber to nullable, and adds a new
-- SceneSeasonNumber column. The original Go-port sweep ported only the
-- second statement and left an incorrect comment claiming SQLite doesn't
-- enforce the original NOT NULL — it does. Fixed 2026-05-02.
--
-- Making a SQLite column nullable requires the recreate-table dance
-- because SQLite has no ALTER COLUMN. At v100 the table has columns
-- Id, TvdbId, SeasonNumber NOT NULL, SearchTerm, ParseTerm (from v3),
-- plus Title (from v48) and Type (from v52). The new shape replaces
-- the SeasonNumber NOT NULL with a nullable column; everything else
-- copies through unchanged.
--
-- See ledger row 100.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "SceneMappings_new" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TvdbId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER,
    "SearchTerm" TEXT NOT NULL,
    "ParseTerm" TEXT NOT NULL,
    "Title" TEXT,
    "Type" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "SceneMappings_new"
    ("Id","TvdbId","SeasonNumber","SearchTerm","ParseTerm","Title","Type")
    SELECT "Id","TvdbId","SeasonNumber","SearchTerm","ParseTerm","Title","Type"
    FROM "SceneMappings";
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "SceneMappings";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings_new" RENAME TO "SceneMappings";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "SceneMappings" ADD COLUMN "SceneSeasonNumber" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
