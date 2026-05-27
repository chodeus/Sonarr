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
	"bytes"
	"encoding/json"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Resource ports `src/Sonarr.Api.V5/Logs/LogResource.cs`. v5 carries
// 7 fields — id, time, exception?, exceptionType?, level, logger,
// message. v3's [apiv3.LogResource] has an additional `method?`
// field which v5 omits. Field order and JSON tag spelling are
// otherwise identical to v3 so a per-record JSON object emitted by
// v3's LogHandler can be projected onto v5 by stripping a single
// key.
//
// At the current empty-corpus state both surfaces emit
// `records: []`, making the divergence invisible on the wire. The
// wrapper applied by [LogHandler] strips `method` from each record
// as a forward pin — when the log DB plumbing lands and records
// arrive with the C# `method` field populated, the v5 wire stays
// 7-field clean without further changes.
type Resource struct {
	ID            int    `json:"id"`
	Time          string `json:"time"`
	Exception     string `json:"exception,omitempty"`
	ExceptionType string `json:"exceptionType,omitempty"`
	Level         string `json:"level"`
	Logger        string `json:"logger"`
	Message       string `json:"message"`
}

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
// (200) as v3 with SortKey rewrite (`id` → `time`) preserved via the
// underlying v3 handler. Wraps v3's response to strip the `method`
// key from each record so the v5 wire matches the C# v5 7-field
// LogResource (see [Resource]). At empty-corpus (records `[]`) the
// strip is a no-op.
func LogHandler() http.Handler { return stripMethod(apiv3.LogHandler()) }

// stripMethod buffers the wrapped handler's JSON response, removes
// the `method` key from each entry under `records`, and re-emits.
// Non-200 responses, bodies that are not a JSON object, or bodies
// lacking a `records` array pass through verbatim — failure-path
// envelopes (400/500) and unrelated shapes are never rewritten.
//
// Kept inline for the first consumer (v5 LogHandler). Consolidates
// into v5util if a second `paged-records-field-strip` need appears
// (J283 first-consumer rule — same posture as the GeneralSettings
// stripField helper).
func stripMethod(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := v5util.NewBufferingWriter()
		h.ServeHTTP(rec, r)
		body := rec.Body()
		v5util.CopyHeaders(w.Header(), rec.Header())
		if rec.Status() != http.StatusOK || !looksLikeJSONObject(body) {
			w.WriteHeader(rec.Status())
			_, _ = w.Write(body)
			return
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(body, &envelope); err != nil {
			w.WriteHeader(rec.Status())
			_, _ = w.Write(body)
			return
		}
		raw, ok := envelope["records"]
		if !ok {
			w.WriteHeader(rec.Status())
			_, _ = w.Write(body)
			return
		}
		var records []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			w.WriteHeader(rec.Status())
			_, _ = w.Write(body)
			return
		}
		for _, row := range records {
			delete(row, "method")
		}
		updated, err := json.Marshal(records)
		if err != nil {
			w.WriteHeader(rec.Status())
			_, _ = w.Write(body)
			return
		}
		envelope["records"] = updated
		out, err := json.Marshal(envelope)
		if err != nil {
			w.WriteHeader(rec.Status())
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	})
}

func looksLikeJSONObject(b []byte) bool {
	trimmed := bytes.TrimSpace(b)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

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
