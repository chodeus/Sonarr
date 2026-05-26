// Package log is the v5 wire surface for /api/v5/log and
// /api/v5/log/file (+ /update sibling).
//
// The v5 family (src/Sonarr.Api.V5/Logs/) is 286 LOC of C# across 6
// files — LogController (80), LogResource (32), LogFileController (41),
// LogFileControllerBase (73), LogFileResource (11),
// UpdateLogFileController (49).
//
// Five endpoints, all GET, all 200:
//
//	v5 endpoint                            v5 status   vs v3 Go-port
//	GET /api/v5/log                            200       same paged envelope
//	GET /api/v5/log/file                       200       same []LogFileResource
//	GET /api/v5/log/file/{filename}            200/404   same file or 404
//	GET /api/v5/log/file/update                200       same
//	GET /api/v5/log/file/update/{filename}     200/404   same
//
// Pure path-alias slice (J251/J259/J264/J268/J271 pattern).
//
// **Resource shapes**:
//
//   - LogResource — v3 carries 8 fields (id, time, exception?,
//     exceptionType?, level, logger, message, **method?**); v5 omits
//     the `method` field (per `src/Sonarr.Api.V5/Logs/LogResource.cs` —
//     6 fields). Invisible at the empty-corpus level because v3
//     Go-port returns `records: []` (the log DB plumbing isn't ported;
//     same DEFERRED state as the v3 surface per
//     `internal/api/v3/log.go:8-12`). When the log-DB port lands, v5
//     Go-port will need a v5-specific Resource that omits `method`
//     from the wire; until then both surfaces emit an empty page.
//   - LogFileResource — IDENTICAL 5 fields (id, filename, lastWriteTime,
//     contentsUrl, downloadUrl) between v3 and v5. Both versions
//     reuse the same LogFileControllerBase pattern (per `LogFileControllerBase.cs:30-52`)
//     so the per-file projection is byte-equal.
//
// SortKey behaviour (GET /log): v5 controller stores `id` internally
// for the SQL query but rewrites the response SortKey to `time`
// so the React UI sees the user-facing key (per
// `LogController.cs:39-42` + `:73`). The v3 Go-port handler at
// `internal/api/v3/log.go:36-38` does the same rewrite. Path-alias
// preserves the behaviour.
//
// `level` query-param filter cascade (fatal → error → warn → info
// → debug → trace, each enabling everything above): v5 implements
// it per `LogController.cs:46-69`. v3 Go-port handler ignores the
// filter (currently emits empty page regardless) — invisible at
// empty-corpus. When log-DB lands, both versions widen.
//
// Wave 3 v5 endpoint family port — 286 LOC C# source. J273.
package log

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// Resource is an alias of the v3 LogResource wire shape.
//
// v3 carries 8 fields including `method?` which v5 omits. At empty-
// corpus this is invisible (records `[]`). When the log-DB port
// lands, v5 will need a v5-specific Resource that omits `method`;
// noted in the package doc.
type Resource = apiv3.LogResource

// FileResource is an alias of the v3 LogFileResource wire shape
// (IDENTICAL between v3 and v5 — same 5 fields).
type FileResource = apiv3.LogFileResource

// FileLister is an alias of the v3 log-folder + URL-base lookup
// interface used by LogFileController.
type FileLister = apiv3.LogFileLister

// UpdateFileLister is an alias of the v3 update-log-folder + URL-base
// lookup interface used by UpdateLogFileController. Same shape as
// FileLister but with `UpdateLogFolder()` instead of `LogFolder()` —
// production wiring snapshots both folders so one struct satisfies
// both interfaces via duck-typing.
type UpdateFileLister = apiv3.UpdateLogFileLister

// ----- handlers (all path-aliases at empty-corpus level) -----

// LogHandler returns GET /api/v5/log. Same paged envelope + status
// (200) as v3. SortKey rewrite (`id` → `time` on response) preserved
// via v3 handler.
var LogHandler = apiv3.LogHandler

// FileListHandler returns GET /api/v5/log/file. Same envelope +
// status (200) as v3. Walks the log folder, sorts by LastWriteTime
// DESC, emits []LogFileResource.
var FileListHandler = apiv3.LogFileHandler

// FileByNameHandler returns GET /api/v5/log/file/{filename}. Same
// envelope + status (200 text/plain OR 404) as v3. Filename
// regex-validated against `^[-.a-zA-Z0-9]+\.txt$` for path-
// traversal protection.
var FileByNameHandler = apiv3.LogFileByNameHandler

// UpdateFileListHandler returns GET /api/v5/log/file/update. Same
// envelope + status (200) as v3 — walks the update-log folder.
var UpdateFileListHandler = apiv3.UpdateLogFileHandler

// UpdateFileByNameHandler returns GET /api/v5/log/file/update/{filename}.
// Same envelope + status (200 text/plain OR 404) as v3.
var UpdateFileByNameHandler = apiv3.UpdateLogFileByNameHandler
