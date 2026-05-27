// Package namingsettings is the v5 wire surface for
// /api/v5/settings/naming (the global naming-config family — 12
// fields covering filename + folder format strings for standard,
// daily, anime, series, season, specials).
//
// Source: src/Sonarr.Api.V5/Settings/NamingSettingsController.cs
// (143 LOC) + NamingSettingsResource.cs (18 LOC) + NamingExampleResource.cs.
//
// 4 endpoints. The C# v5 controller inherits from RestController<T>
// (not SettingsController<T>), so it manages persistence directly via
// _namingConfigService.GetConfig / Save. Status codes match v3:
//
//	GET    /naming         → 200 OK
//	GET    /naming/{id}    → 200 OK
//	PUT    /naming/{id}    → 202 Accepted
//	GET    /naming/examples → 200 OK
//
// Pure path-alias slice — same J224/J280/J281/J282 template. Wire
// shape byte-identical between v3 NamingConfigResource and v5
// NamingSettingsResource (12 fields: id + renameEpisodes +
// replaceIllegalCharacters + colonReplacementFormat +
// customColonReplacementFormat + multiEpisodeStyle +
// standardEpisodeFormat + dailyEpisodeFormat + animeEpisodeFormat +
// seriesFolderFormat + seasonFolderFormat + specialsFolderFormat).
//
// NamingExampleResource shape also identical (8 nullable string
// fields). v3 emits empty examples per the deferred state at
// `internal/api/v3/config_naming.go:177-194` pending the
// IFilenameSampleService Go port. v5 inherits the same deferral via
// the path-alias.
//
// Validation chain on the C# v5 side is richer than v3 Go-port —
// the per-format token validators (ValidEpisodeFormat /
// ValidDailyEpisodeFormat / ValidAnimeEpisodeFormat /
// ValidSeriesFolderFormat / ValidSeasonFolderFormat /
// ValidSpecialsFolderFormat / ValidCustomColonReplacement) need
// IFilenameSampleService + IFilenameValidationService adapter shims.
// Same DEFERRED state as v3 Go-port per
// `internal/api/v3/config_naming.go:122-130`. v5 inherits.
package namingsettings

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5 — same
// 12 fields + the inherited id).
type Resource = apiv3.NamingConfigResource

// ExampleResource is the JSON shape produced by GET /examples — also
// shared verbatim with v3.
type ExampleResource = apiv3.NamingExampleResource

// Getter is the read-side interface. Aliased from v3.
type Getter = apiv3.NamingConfigGetter

// Writer is the read/write interface for the PUT handler. Aliased
// from v3.
type Writer = apiv3.NamingConfigWriter

// GetHandler returns GET /api/v5/settings/naming (and the /{id}
// variant). Same wire shape + status code as v3 — re-exported.
var GetHandler = apiv3.NamingConfigHandler

// UpdateHandler returns PUT /api/v5/settings/naming/{id}. Same wire
// shape + status code (202 Accepted) as v3 — re-exported.
var UpdateHandler = apiv3.NamingConfigUpdateHandler

// ExamplesHandler returns GET /api/v5/settings/naming/examples. v3
// emits an empty NamingExampleResource pending the
// IFilenameSampleService port. v5 inherits the same deferral.
var ExamplesHandler = apiv3.NamingConfigExamplesHandler
