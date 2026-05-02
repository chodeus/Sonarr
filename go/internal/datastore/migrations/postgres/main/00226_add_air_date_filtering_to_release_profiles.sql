-- Ports Migration/226. See ledger row 226.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "AirDateRestriction" boolean DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "ReleaseProfiles" ADD COLUMN "AirDateGracePeriod" integer DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
