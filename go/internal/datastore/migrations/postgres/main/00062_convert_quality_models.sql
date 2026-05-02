-- Ports Migration/062. HIGH RISK — Quality JSON shape conversion in
-- EpisodeFiles, Blacklist, History. The C# converts {"Quality":N,"Proper":bool}
-- to {"Quality":N,"Revision":{"Version":1|2}}. No-op for fresh DBs;
-- Phase 6 cutover handles legacy data. See ledger row 62.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
