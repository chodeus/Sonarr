-- Ports Migration/071. Drop QualityDefinitions.Weight column (was UNIQUE,
-- so SQLite needs the recreate-table dance). Plus a complex Profile JSON
-- update (prepend Quality=0 Unknown to each Profile.Items). Schema-only;
-- Profile JSON update is no-op for fresh DBs. See ledger row 71.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityDefinitions" DROP COLUMN "Weight";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
