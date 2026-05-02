-- Ports Migration/120. See ledger row 120.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX "IX_Episodes_SeriesId_AirDate" ON "Episodes" ("SeriesId" ASC, "AirDate" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX "IX_History_EpisodeId";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_History_EpisodeId" ON "History" ("EpisodeId" ASC, "Date" DESC);
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX "IX_History_DownloadId";
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_History_DownloadId" ON "History" ("DownloadId" ASC, "Date" DESC);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
