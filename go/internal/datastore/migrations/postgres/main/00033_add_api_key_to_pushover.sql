-- Ports Migration/033. HIGH RISK — embedded-doc shape mutation on Pushover
-- settings JSON. The C# version reads each Pushover Notifications row,
-- deserializes Settings JSON, sets ApiKey to a hardcoded string, clamps
-- Priority from 2 to 1, and writes back. The hardcoded API key is a
-- baked-in Sonarr-Pushover application key (not a user secret).
--
-- For fresh DBs no action needed (Notifications is empty). Phase 6 cutover
-- reimplements the JSON mutation in Go application code with a real JSON
-- parser. See ledger row 33.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
