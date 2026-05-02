-- Ports Migration/070. CREATE DelayProfiles + insert default row. See ledger row 70.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE "DelayProfiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "EnableUsenet" boolean NOT NULL,
    "EnableTorrent" boolean NOT NULL,
    "PreferredProtocol" integer NOT NULL,
    "UsenetDelay" integer NOT NULL,
    "TorrentDelay" integer NOT NULL,
    "Order" integer NOT NULL,
    "Tags" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO "DelayProfiles" ("EnableUsenet","EnableTorrent","PreferredProtocol","UsenetDelay","TorrentDelay","Order","Tags") VALUES (true, true, 1, 0, 0, 2147483647, '[]');
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported' WHERE false;
