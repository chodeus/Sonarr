-- Ports Migration/153. See ledger row 153.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Notifications" ADD COLUMN "OnEpisodeFileDeleteForUpgrade" boolean DEFAULT true;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
