// Package mediamanagementsettings is the v5 wire surface for
// /api/v5/settings/mediamanagement (the global MediaManagement
// settings resource — 23 fields covering import/storage/naming/permission
// behaviour).
//
// Source: src/Sonarr.Api.V5/Settings/MediaManagementSettingsController.cs
// (70 LOC) + MediaManagementSettingsResource.cs (72 LOC).
//
// The C# v5 controller inherits from `SettingsController<TResource>`
// and produces the same status codes as the v3 MediaManagementConfig
// Controller:
//
//	GET  → 200 OK         (v3: same)
//	PUT  → 202 Accepted   (v3: same)
//
// Pure path-alias slice — same J280/J281 pattern. Wire shape
// byte-identical between v3 and v5 (24 fields including the inherited
// `id`; both versions emit `id: 1` for the singleton). Re-exports the
// existing v3 handlers under the v5 route prefix.
//
// The v5 C# controller registers a richer SharedValidator chain than
// what the v3 Go-port ports (folderChmodValidator,
// folderWritableValidator, pathExistsValidator, rootFolderValidator,
// rootFolderAncestorValidator, startupFolderValidator,
// systemFolderValidator, seriesPathValidator). v3 Go-port marks these
// as DEFERRED per `config_mediamanagement.go:195-201` pending disk-
// validator adapter shims. v5 inherits the same deferral via the
// path-alias — when those shims land both surfaces widen via the
// shared validator.
//
// One v5-only validator rule: archive/media-file extension intersect
// check on UserRejectedExtensions (rejects extensions that already
// match FileExtensions.ArchiveExtensions or MediaFileExtensions.
// Extensions). v3 Go-port's validateMediaManagementConfig only checks
// for empty-after-trim entries — the C# v5 controller adds the
// intersect rule per MediaManagementSettingsController.cs:43-63. The
// path-alias preserves v3's weaker rule; tightening to match v5
// requires either a v5-specific validator (which forks the slice) or
// porting the v5 rule back to v3 (preferred — same parity argument as
// the deferred path-validators).
package mediamanagementsettings

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5 —
// same 23 fields + the inherited id).
type Resource = apiv3.MediaManagementConfigResource

// Getter is the read-side interface. Aliased from v3.
type Getter = apiv3.MediaManagementConfigGetter

// Writer is the read/write interface for the PUT handler. Aliased
// from v3.
type Writer = apiv3.MediaManagementConfigWriter

// GetHandler returns GET /api/v5/settings/mediamanagement (and the
// /{id} variant). Same wire shape + status code as v3 — re-exported.
var GetHandler = apiv3.MediaManagementConfigHandler

// UpdateHandler returns PUT /api/v5/settings/mediamanagement/{id}.
// Same wire shape + status code (202 Accepted) as v3 — re-exported.
var UpdateHandler = apiv3.MediaManagementConfigUpdateHandler
