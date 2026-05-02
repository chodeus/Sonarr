-- Ports Migration/137. See ledger row 137.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AiredAfterSeasonNumber" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AiredBeforeSeasonNumber" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Episodes" ADD COLUMN "AiredBeforeEpisodeNumber" integer;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
