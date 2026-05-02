-- Ports Migration/037. Drops Allowed column; creates QualityDefinitions
-- table; drops QualitySizes. The C# QualitySizes→QualityDefinitions data
-- migration is a no-op for fresh DBs (QualitySizes is empty and the
-- runtime seeds QualityDefinitions defaults on startup). See ledger row 37.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" DROP COLUMN "Allowed";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "QualityDefinitions" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Quality" INTEGER NOT NULL UNIQUE,
    "Title" TEXT NOT NULL UNIQUE,
    "Weight" INTEGER NOT NULL UNIQUE,
    "MinSize" INTEGER NOT NULL,
    "MaxSize" INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_QualityDefinitions_Quality" ON "QualityDefinitions" ("Quality");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_QualityDefinitions_Title" ON "QualityDefinitions" ("Title");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_QualityDefinitions_Weight" ON "QualityDefinitions" ("Weight");
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "QualitySizes";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
