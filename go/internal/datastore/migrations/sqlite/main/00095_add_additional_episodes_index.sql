-- Ports Migration/095. See ledger row 95.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX "IX_Episodes_SeriesId_SeasonNumber_EpisodeNumber" ON "Episodes" ("SeriesId" ASC, "SeasonNumber" ASC, "EpisodeNumber" ASC);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
