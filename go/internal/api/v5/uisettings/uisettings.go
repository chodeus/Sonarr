// Package uisettings is the v5 wire surface for /api/v5/settings/ui
// (the global UI settings resource: calendar / date-format / theme /
// locale fields, 10 fields total).
//
// Source: src/Sonarr.Api.V5/Settings/UiSettingsController.cs (31 LOC)
// + UiSettingsResource.cs (44 LOC).
//
// The C# v5 controller inherits from `SettingsController<TResource>`
// and produces the same status codes as the v3 UiConfigController:
//
//	GET  → 200 OK         (v3: same)
//	PUT  → 202 Accepted   (v3: same)
//
// Pure path-alias slice — same J224/J280 pattern. Wire shape
// byte-identical between v3 and v5 (11 fields including the inherited
// `id`; both versions emit `id: 1` for the singleton). Re-exports the
// existing v3 handlers under the v5 route prefix.
//
// SharedValidator chain on the C# side (UiSettingsController.cs:14-24)
// — UiLanguage must be ≥ 1 AND must exist in Language.All — is
// preserved verbatim by the path-alias because v3's
// `validateUIConfig` at `internal/api/v3/config_ui.go:185-195`
// implements both rules. The C# v5 SettingsController.SaveSettings
// reflection-based SaveConfigDictionary writes to both
// IConfigFileProvider (theme XML row) and IConfigService (the other 9
// DB-backed fields); the Go-port composes both via the
// UIConfigWriter interface which the existing host_wiring.go
// uiConfigGetter adapter satisfies.
package uisettings

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.UIConfigResource

// Getter is the read-side interface. Aliased from v3.
type Getter = apiv3.UIConfigGetter

// Writer is the read/write interface for the PUT handler. Aliased
// from v3.
type Writer = apiv3.UIConfigWriter

// GetHandler returns GET /api/v5/settings/ui (and the /{id} variant).
// Same wire shape + status code as v3 — re-exported.
var GetHandler = apiv3.UIConfigHandler

// UpdateHandler returns PUT /api/v5/settings/ui/{id}. Same wire shape
// + status code (202 Accepted) as v3 — re-exported.
var UpdateHandler = apiv3.UIConfigUpdateHandler
