-- Ports Migration/037 for Postgres. See ledger row 37.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" DROP COLUMN "Allowed";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE "QualityDefinitions" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Quality" integer NOT NULL UNIQUE,
    "Title" text NOT NULL UNIQUE,
    "Weight" integer NOT NULL UNIQUE,
    "MinSize" integer NOT NULL,
    "MaxSize" integer NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_QualityDefinitions_Quality" ON "QualityDefinitions" ("Quality");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_QualityDefinitions_Title" ON "QualityDefinitions" ("Title");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_QualityDefinitions_Weight" ON "QualityDefinitions" ("Weight");
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE "QualitySizes";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
