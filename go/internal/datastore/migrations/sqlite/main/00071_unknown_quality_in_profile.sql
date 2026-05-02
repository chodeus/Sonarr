-- Ports Migration/071. Drop QualityDefinitions.Weight column (was UNIQUE,
-- so SQLite needs the recreate-table dance). Plus a complex Profile JSON
-- update (prepend Quality=0 Unknown to each Profile.Items). Schema-only;
-- Profile JSON update is no-op for fresh DBs. See ledger row 71.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "QualityDefinitions_new" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Quality" INTEGER NOT NULL UNIQUE,
    "Title" TEXT NOT NULL UNIQUE,
    "MinSize" INTEGER NOT NULL,
    "MaxSize" INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "QualityDefinitions_new" SELECT "Id","Quality","Title","MinSize","MaxSize" FROM "QualityDefinitions";
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "QualityDefinitions";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions_new" RENAME TO "QualityDefinitions";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
