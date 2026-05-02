-- Ports Migration/151. See ledger row 151.

-- +goose Up
-- +goose StatementBegin
UPDATE "CustomFilters" SET "Type" = 'series' WHERE "Type" = 'seriesIndex';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "CustomFilters" SET "Type" = 'series' WHERE "Type" = 'seriesEditor';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "CustomFilters" SET "Type" = 'series' WHERE "Type" = 'seasonPass';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
