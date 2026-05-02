-- Ports Migration/149. See ledger row 149.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnSeriesDelete" boolean DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnEpisodeFileDelete" boolean DEFAULT false;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
