-- Ports Migration/205. HIGH RISK polymorphic-rename in CustomFormats.Specifications
-- JSON: SeasonPackSpecification → ReleaseTypeSpecification. Real REPLACE.
-- See ledger row 205.

-- +goose Up
-- +goose StatementBegin
UPDATE "CustomFormats" SET "Specifications" = REPLACE("Specifications", 'SeasonPackSpecification', 'ReleaseTypeSpecification');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
