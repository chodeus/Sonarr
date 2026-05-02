-- Ports Migration/055. See ledger row 55.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" DROP COLUMN "QualityProfileId";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
