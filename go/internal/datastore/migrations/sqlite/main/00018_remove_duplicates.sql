-- Ports Migration/018_remove_duplicates.cs as a no-op.
--
-- The C# version is a complex C# helper that finds duplicate Series rows by
-- TvdbId or TitleSlug, deletes them along with their cascading Episodes /
-- Seasons / History / EpisodeFiles, and does the same for Episodes by
-- TvDbEpisodeId. It exists only to clean up DBs corrupted by an earlier
-- bug; migration 019 (immediately after) creates UNIQUE INDEXes on the
-- same columns and would fail if duplicates remained.
--
-- For fresh Go databases this is a no-op: the inline UNIQUE constraints
-- on Series.TvdbId, Series.TitleSlug, Episodes.TvDbEpisodeId have been
-- present since migration 001, so duplicates can't exist by the time we
-- reach version 18. Cutover from existing C# DBs (Phase 6) handles legacy
-- corruption separately, before goose runs.
--
-- See go/analysis/migrations/ledger.md row 18.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
