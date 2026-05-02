-- Ports Migration/036. HIGH RISK — first major QualityProfiles JSON
-- shape change. The C# reads QualityProfiles.Allowed (JSON list of
-- quality enum values), and writes QualityProfiles.Items (JSON list
-- of {Quality, Allowed} objects covering every default quality).
-- It also rewrites EpisodeFiles.Quality JSON shape.
-- Schema portion only; complex C# JSON conversion is no-op for fresh
-- DBs. See ledger row 36.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "QualityProfiles" ADD COLUMN "Items" text;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
