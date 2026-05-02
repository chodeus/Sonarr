-- Ports Migration/148. HIGH RISK MediaInfo JSON shape — rename two keys
-- (audioChannels → audioChannelsContainer; audioChannelPositionsText →
-- audioChannelPositionsTextContainer). Real SQL because the ledger flags
-- this as cumulativity-sensitive (148 → 163 → 225 chain). See ledger row 148.

-- +goose Up
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "MediaInfo" = Replace("MediaInfo", '"audioChannels"', '"audioChannelsContainer"');
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "EpisodeFiles" SET "MediaInfo" = Replace("MediaInfo", '"audioChannelPositionsText"', '"audioChannelPositionsTextContainer"');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
