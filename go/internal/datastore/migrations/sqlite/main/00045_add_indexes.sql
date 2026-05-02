-- Ports Migration/045. Add several non-unique indexes. See ledger row 45.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX "IX_Blacklist_SeriesId" ON "Blacklist" ("SeriesId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_EpisodeFiles_SeriesId" ON "EpisodeFiles" ("SeriesId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Episodes_EpisodeFileId" ON "Episodes" ("EpisodeFileId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Episodes_SeriesId" ON "Episodes" ("SeriesId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_History_EpisodeId" ON "History" ("EpisodeId");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_History_Date" ON "History" ("Date");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Series_Path" ON "Series" ("Path");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Series_CleanTitle" ON "Series" ("CleanTitle");
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX "IX_Series_TvRageId" ON "Series" ("TvRageId");
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
