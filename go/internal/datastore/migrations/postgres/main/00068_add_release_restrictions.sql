-- Ports Migration/068. CREATE Restrictions; complex Config-to-Restrictions
-- backfill is no-op (Phase 6 handles). See ledger row 68.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "Restrictions" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Required" text,
    "Preferred" text,
    "Ignored" text,
    "Tags" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM "Config" WHERE "Key" = 'releaserestrictions';
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
