-- Ports Migration/068. CREATE Restrictions; complex Config-to-Restrictions
-- backfill is no-op (Phase 6 handles). See ledger row 68.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Restrictions" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Required" TEXT,
    "Preferred" TEXT,
    "Ignored" TEXT,
    "Tags" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Config" WHERE "Key" = 'releaserestrictions';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE 0;
