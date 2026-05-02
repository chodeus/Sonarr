-- Ports Migration/020_add_year_and_seasons_to_series.cs.
-- HIGH RISK: first write of the Series.Seasons embedded-document JSON.
-- The shape locked in here ([{"seasonNumber": N, "monitored": <bool>}])
-- is the wire contract every later migration (and the Go port's runtime
-- code) must preserve byte-for-byte.
--
-- C# semantics: read SeasonNumber + Monitored from Seasons rows, group by
-- SeriesId, force monitored=false for SeasonNumber=0 (specials), serialize
-- via Newtonsoft anonymous-object ToJson() — which yields camelCase keys
-- and lowercase boolean literals.
--
-- The SQL form uses SQLite JSON1's json_group_array + json_object. Note the
-- json(...) wrapper around the boolean expression: json_object emits
-- integer values as JSON numbers, but Sonarr's contract is JSON true/false
-- — so we wrap a literal 'true'/'false' string in json() to embed it
-- raw. COALESCE handles series with no Seasons rows ('[]' empty array).
--
-- See go/analysis/migrations/ledger.md row 20 (HIGH RISK).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Year" INTEGER;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "Series" ADD COLUMN "Seasons" TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE "Series" SET "Seasons" = COALESCE(
    (SELECT json_group_array(
        json_object(
            'seasonNumber', "SeasonNumber",
            'monitored', json(CASE
                WHEN "SeasonNumber" = 0 THEN 'false'
                WHEN "Monitored" = 1 THEN 'true'
                ELSE 'false'
            END)
        )
     ) FROM "Seasons" WHERE "SeriesId" = "Series"."Id"),
    '[]'
);
-- +goose StatementEnd

-- +goose Down
SELECT 'rollback not supported; restore from backup' WHERE 0;
