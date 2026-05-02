-- Ports Migration/020_add_year_and_seasons_to_series.cs for Postgres.
-- HIGH RISK: see SQLite counterpart for full rationale and the
-- [{"seasonNumber": N, "monitored": <bool>}] shape lock.
-- Postgres has native booleans so json_build_object emits true/false
-- correctly without wrapping. COALESCE handles series with no Seasons.
-- See go/analysis/migrations/ledger.md row 20 (HIGH RISK).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Year" integer;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Seasons" text;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Seasons" = COALESCE(
    (SELECT json_agg(
        json_build_object(
            'seasonNumber', "SeasonNumber",
            'monitored', CASE
                WHEN "SeasonNumber" = 0 THEN false
                WHEN "Monitored" IS TRUE THEN true
                ELSE false
            END
        )
     )::text FROM "Seasons" WHERE "SeriesId" = "Series"."Id"),
    '[]'
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE false;
