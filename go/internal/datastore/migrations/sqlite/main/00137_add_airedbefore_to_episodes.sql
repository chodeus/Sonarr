-- Ports Migration/137. See ledger row 137.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AiredAfterSeasonNumber" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AiredBeforeSeasonNumber" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AiredBeforeEpisodeNumber" INTEGER;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
