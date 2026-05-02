-- Ports Migration/032. See ledger row 32.

-- +goose Up
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "ReleaseGroup" = 'DRONE' WHERE "ReleaseGroup" IS NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
