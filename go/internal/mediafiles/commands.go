package mediafiles

// RescanSeriesName is the registry key + Commands.Name used by the
// IExecute<RescanSeriesCommand> handler (slice 5). Mirrors the C#
// short form: `RescanSeriesCommand` → `RescanSeries`.
const RescanSeriesName = "RescanSeries"

// RescanSeriesCommand is the JSON-decodable payload of the
// `RescanSeries` command queued through the messaging bus. Mirrors
// `src/NzbDrone.Core/MediaFiles/Commands/RescanSeriesCommand.cs:1-19`.
//
// `SeriesID` is optional: a populated value scopes the rescan to a
// single series; nil (the default-constructed form) scans all series
// — mirroring the C# `int?` field plus the `Execute` branching at
// `DiskScanService.cs:301-317`. The Go field renames the C# `SeriesId`
// to `SeriesID` to match the all-caps initialism convention used by
// the rest of the port (see `episodefiles`, `episodes`, `history`,
// etc.).
//
// JSON shape: `{"seriesId": 42}` or `{}`. The C# tree uses
// PascalCase by default but the messaging bus persists payloads via
// STJson with camelCase, matching the tag here.
type RescanSeriesCommand struct {
	SeriesID *int `json:"seriesId"`
}
