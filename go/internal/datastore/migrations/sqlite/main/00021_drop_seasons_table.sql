-- Ports Migration/021_drop_seasons_table.cs.
-- Migration 020 wrote the seasons data into Series.Seasons JSON; this
-- migration finishes the move by dropping the Seasons table.
-- See go/analysis/migrations/ledger.md row 21.

-- +goose Up
-- +goose StatementBegin
DROP TABLE "Seasons";
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
