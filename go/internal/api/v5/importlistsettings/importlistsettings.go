// Package importlistsettings is the v5 wire surface for
// /api/v5/settings/importlist (the global ImportList settings resource:
// `ListSyncLevel` enum + `ListSyncTag` int).
//
// The C# v5 controller at
// `src/Sonarr.Api.V5/Settings/ImportListSettingsController.cs:9-26`
// inherits from `SettingsController<TResource>` and produces the same
// status codes as the v3 ImportListConfigController:
//
//	GET  → 200 OK         (v3: same)
//	PUT  → 202 Accepted   (v3: same — SettingsController.SaveSettings
//	                       returns `TypedAccepted<TResource>`)
//
// Because the wire shape (`{id, listSyncLevel, listSyncTag}`) and the
// status codes both match v3 verbatim, this slice is a pure path-alias:
// the v5 routes mount the existing v3 handlers under the new path
// prefix.
//
// The only on-paper divergence is the v5 SettingsController's reflection-
// based `SaveConfigDictionary` (vs v3's explicit per-field setters), but
// this is an implementation detail of the C# host — both code paths write
// the same two config rows and emit the same response. The Go-port
// already implements the persistence via the typed `SetListSyncLevel` /
// `SetListSyncTag` setters which produce byte-identical results.
//
// Audit row C.1.d (upstream v5 catch-up — closed 5.J224).
package importlistsettings

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.ImportListConfigResource

// Getter is the read-side interface (returns ListSyncLevel +
// ListSyncTag from the config provider). Aliased from v3.
type Getter = apiv3.ImportListConfigGetter

// Writer is the read/write interface for the PUT handler. Aliased
// from v3.
type Writer = apiv3.ImportListConfigWriter

// GetHandler returns GET /api/v5/settings/importlist (and the
// /{id} variant). Same wire shape + status code as v3 — re-exported.
var GetHandler = apiv3.ImportListConfigHandler

// UpdateHandler returns PUT /api/v5/settings/importlist/{id}. Same
// wire shape + status code (202 Accepted) as v3 — re-exported.
var UpdateHandler = apiv3.ImportListConfigUpdateHandler
