-- Ports Migration/031. Drops the source columns 029 read. See ledger row 31.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "Separator";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "NumberStyle";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "IncludeSeriesTitle";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "IncludeEpisodeTitle";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "IncludeQuality";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "NamingConfig" DROP COLUMN "ReplaceSpaces";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
