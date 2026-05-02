-- Ports Migration/084. ALTER COLUMN MinSize/MaxSize INT → DOUBLE. SQLite
-- has dynamic typing so no-op there; Postgres needs the type change.
-- See ledger row 84.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" ALTER COLUMN "MinSize" TYPE double precision USING "MinSize"::double precision;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" ALTER COLUMN "MaxSize" TYPE double precision USING "MaxSize"::double precision;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" ALTER COLUMN "MinSize" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" ALTER COLUMN "MaxSize" DROP NOT NULL;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "QualityDefinitions" SET "MaxSize" = NULL WHERE "Quality" = 10 OR "MaxSize" = 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
