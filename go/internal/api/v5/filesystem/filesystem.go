// Package filesystem is the v5 wire surface for /api/v5/filesystem.
//
// The v5 controller (src/Sonarr.Api.V5/FileSystem/FileSystemController.cs)
// has three GET endpoints, all of which mirror v3 line-for-line:
//
//	v5 endpoint                    Status    Body
//	GET /api/v5/filesystem         200       FileSystemResult
//	GET /api/v5/filesystem/type    200       { type: "file"|"folder" }
//	GET /api/v5/filesystem/mediafiles 200    [ {path, relativePath, name} ]
//
// All three statuses are 200 in both versions (v5 uses TypedResults.Ok
// which produces the same wire response as v3's [ApiController]
// defaults). No bulk surface, no DELETE, no status-code tightening.
//
// Re-export the v3 handlers verbatim via variable aliases. Type
// aliases mirror future-divergence preparation per J251's DiskSpace
// pattern.
//
// Wave 3 v5 endpoint family port — 63 LOC of C# source, second-
// smallest family after DiskSpace. J252.
package filesystem

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// FileSystemModel is an alias of the v3 wire shape (unchanged in v5).
type FileSystemModel = apiv3.FileSystemModel

// FileSystemResult is an alias of the v3 wire shape (unchanged in v5).
type FileSystemResult = apiv3.FileSystemResult

// MediaFileResult is an alias of the v3 wire shape (unchanged in v5).
type MediaFileResult = apiv3.MediaFileResult

// ListHandler returns GET /api/v5/filesystem. Lists directories at
// the queried path; with includeFiles=true also lists files. Re-uses
// the v3 handler — same wire shape, same OrderBy sort, same special-
// folder filter, same Drive-enumeration deferral on Windows.
var ListHandler = apiv3.FileSystemHandler

// TypeHandler returns GET /api/v5/filesystem/type. Returns `{type:"file"}`
// when the path is an existing regular file; otherwise `{type:"folder"}`.
// The folder-default semantic deliberately avoids leaking "does X
// exist" information to the UI (matches C# `:35-46` comment).
var TypeHandler = apiv3.FileSystemTypeHandler

// MediaFilesHandler returns GET /api/v5/filesystem/mediafiles.
// Walks the path recursively for video files; missing folder returns
// `[]` (matches C# `Array.Empty<string>()` short-circuit at `:50-53`).
var MediaFilesHandler = apiv3.FileSystemMediaFilesHandler
