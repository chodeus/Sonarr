// Package seriesimport ports the v5 SeriesImportController at
// /api/v5/series/import.
//
// Source: src/Sonarr.Api.V5/Series/SeriesImportController.cs (29 LOC).
// Single POST endpoint that accepts a List<SeriesResource> and
// returns the persisted list. Wraps the v3 SeriesImportHandler with
// a 201→200 status demote on non-empty success (empty input already
// emits 200 on both surfaces).
//
// Wire shape:
//
//	POST /api/v5/series/import → 200 OK + List<SeriesResource>
//	                           (empty input → 200 + `[]`; v3 same)
//	                           (non-empty success → v3 emits 201; v5 wraps to 200)
//
// SeriesResource shape is shared with the v3 surface (the full Series
// family is deferred per the loop contract — both v3 and v5 emit the
// current v3 Resource projection). When the full v5 Series family
// lands the Resource shape will widen for both surfaces via the
// shared service / mapper port.
//
// Validation chain ported from v3 verbatim — Title + Path +
// QualityProfileId per-row check; first-failure-aborts-batch
// semantics; prefixed [i]. error path attribution per the React UI's
// batch-wizard expectation.
package seriesimport

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// Writer is the persistence surface — re-exports v3 SeriesWriter so
// the v5 handler can share the same backing service.
type Writer = apiv3.SeriesWriter

// Emitter is the SignalR Created broadcast surface — re-exports v3
// SeriesEmitter for the same reason.
type Emitter = apiv3.SeriesEmitter

// PostHandler returns POST /api/v5/series/import. Wraps the v3
// handler with a 201→200 demote on non-empty success. Empty input
// passes through unchanged (v3 already emits 200). Validation
// failures (400) and 500-class errors also pass through verbatim
// because the demote predicate only matches 201.
func PostHandler(svc Writer, emitter Emitter) http.Handler {
	return rewriteCreatedToOK(apiv3.SeriesImportHandler(svc, emitter))
}

// rewriteCreatedToOK wraps a handler whose happy path emits 201
// Created + body and demotes the status to 200 OK while preserving
// the body byte-for-byte. Analogous to v5util.RewriteAcceptedToOK
// (202→200) — same pattern shifted to a different source status.
// Kept inline for the first consumer; consolidate into v5util if a
// second 201→200 case appears.
func rewriteCreatedToOK(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := v5util.NewBufferingWriter()
		h.ServeHTTP(rec, r)
		v5util.CopyHeaders(w.Header(), rec.Header())
		status := rec.Status()
		if status == http.StatusCreated {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(rec.Body())
	})
}
