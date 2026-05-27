// Package indexersettings is the v5 wire surface for
// /api/v5/settings/indexer (the global Indexer settings resource:
// MinimumAge / Retention / MaximumSize / RssSyncInterval).
//
// Source: src/Sonarr.Api.V5/Settings/IndexerSettingsController.cs
// (30 LOC) + IndexerSettingsResource.cs (27 LOC).
//
// The C# v5 controller inherits from `SettingsController<TResource>`
// at SettingsController.cs:11-54 and produces the same status codes
// as the v3 IndexerConfigController:
//
//	GET  → 200 OK         (v3: same)
//	PUT  → 202 Accepted   (v3: same — SettingsController.SaveSettings
//	                       returns TypedAccepted<TResource>)
//
// Because the wire shape (`{id, minimumAge, retention, maximumSize,
// rssSyncInterval}`) and the status codes both match v3 verbatim,
// this slice is a pure path-alias: the v5 routes mount the existing
// v3 handlers under the new path prefix. Same J224 importlistsettings
// pattern.
//
// The C# v5 SettingsController uses reflection-based
// `SaveConfigDictionary` (vs v3's explicit per-field setters), but
// this is an implementation detail of the C# host — both code paths
// write the same four config rows and emit the same response. The
// Go-port already implements the persistence via the typed
// SetMinimumAge / SetRetention / SetMaximumSize / SetRssSyncInterval
// setters which produce byte-identical results.
package indexersettings

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 wire shape (unchanged in v5).
type Resource = apiv3.IndexerConfigResource

// Getter is the read-side interface. Aliased from v3.
type Getter = apiv3.IndexerConfigGetter

// Writer is the read/write interface for the PUT handler. Aliased
// from v3.
type Writer = apiv3.IndexerConfigWriter

// GetHandler returns GET /api/v5/settings/indexer (and the /{id}
// variant). Same wire shape + status code as v3 — re-exported.
var GetHandler = apiv3.IndexerConfigHandler

// UpdateHandler returns PUT /api/v5/settings/indexer/{id}. Same
// wire shape + status code (202 Accepted) as v3 — re-exported.
var UpdateHandler = apiv3.IndexerConfigUpdateHandler
