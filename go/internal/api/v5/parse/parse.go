// Package parse is the v5 wire surface for /api/v5/parse.
//
// The v5 controller (src/Sonarr.Api.V5/Parse/ParseController.cs) is a
// single-GET endpoint at 101 LOC of C# source. Body shape mostly
// matches v3 — both `ParseResource` carry Title + ParsedEpisodeInfo
// plus the (currently-deferred) joined fields Series / Episodes /
// Languages / CustomFormats / CustomFormatScore. The Go-port v3
// handler emits the minimal shape (Title + ParsedEpisodeInfo only —
// per `internal/api/v3/parse.go` doc-comment lines 10-16); v5 inherits
// the same deferral until the parsing-service Map + format calculator
// port lands.
//
// THE ONE DIVERGENCE that requires a v5-specific handler: the
// empty-input case.
//
//	v3: empty title AND empty path → 200 + literal `null` (JSON)
//	v5: empty title AND empty path → 200 + `{"title": <input-or-empty>}`
//
// Per the C# v5 source (ParseController.cs:35-41):
//
//	if (title.IsNullOrWhiteSpace())
//	{
//	    return TypedResults.Ok(new ParseResource { Title = title });
//	}
//
// The "object with Title-only" shape vs v3's literal `null` is the
// difference. Other branches (parsed-info returned by Parser, or
// parsed-info is null) match v3 line-for-line.
//
// Implementation: variable-alias would NOT work here — the empty-case
// shape differs. Define a v5-specific handler that uses the v3
// Resource type (no field divergence on the populated branch) but
// emits the v5 empty-case shape.
//
// Wave 3 v5 endpoint family port — 101 LOC C# source. J258.
package parse

import (
	"encoding/json"
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/parser"
)

// Resource is an alias of the v3 wire shape. Both versions carry
// `id,omitempty` + `title` + `parsedEpisodeInfo,omitempty` on the wire;
// the C# v5 Resource defines extra fields (series, episodes, languages,
// customFormats, customFormatScore) but all are nullable + WhenWritingNull
// in C#, so when the Go-port emits the v3 minimal shape the wire is
// byte-equivalent (the unset fields don't appear) — matching what C#
// emits when its parsing-service Map returns no joined data.
type Resource = apiv3.ParseResource

// GetHandler returns GET /api/v5/parse.
//
// Mirrors `ParseController.Parse` at
// `src/Sonarr.Api.V5/Parse/ParseController.cs:31-77`:
//
//  1. Empty title (after whitespace trim) → 200 + ParseResource{Title}
//     (Title=input; other fields nil/absent via JSON omitempty).
//     Differs from v3 which emits literal `null`.
//  2. Path supplied → Parser.ParsePath; else Parser.ParseTitle.
//  3. Parser returns nil → 200 + ParseResource{Title} (same shape as
//     empty case; the ParsedEpisodeInfo field stays nil).
//  4. Parser returns non-nil → 200 + ParseResource{Title, ParsedEpisodeInfo}.
//
// J344 — Series + Episodes + Languages population wired via the
// optional ParseMapper dep (same shape as v3). CustomFormats remains
// DEFERRED — needs aggregation.Service.Augment + custom-format
// calculator chain (next slice in the Arc 2 sub-arc).
//
// J258. J344 — Map wire-through.
func GetHandler(mapper apiv3.ParseMapper) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		title := q.Get("title")
		path := q.Get("path")
		// v5 EMPTY-INPUT branch — return a ParseResource object with
		// Title set (potentially to empty string), NOT a literal null.
		// This is the wire divergence vs v3 that motivates this slice.
		if title == "" && path == "" {
			writeJSON(w, Resource{Title: title})
			return
		}
		// Path takes precedence (matches C# `if (path.IsNotNullOrWhiteSpace())`).
		var parsed *parser.ParsedEpisodeInfo
		if path != "" {
			parsed = parser.ParsePath(path)
		} else {
			parsed = parser.ParseTitle(title)
		}
		out := Resource{Title: title, ParsedEpisodeInfo: parsed}
		// J344 — Map wire-through. Nil-tolerant + nil-result-tolerant
		// like the v3 handler.
		if mapper != nil && parsed != nil {
			subject, err := mapper.Map(r.Context(), parsed, 0, 0, "", nil)
			if err == nil && subject != nil && subject.Series != nil {
				seriesRes := apiv3.ToSeriesResource(*subject.Series)
				out.Series = &seriesRes
				if len(subject.Episodes) > 0 {
					eps := make([]apiv3.EpisodeResource, 0, len(subject.Episodes))
					for _, e := range subject.Episodes {
						eps = append(eps, apiv3.ToEpisodeResource(e))
					}
					out.Episodes = eps
				}
				out.Languages = subject.Languages
				// J345 — CustomFormats + Score via optional-extension
				// type-assertion (same pattern as v3).
				if populator, ok := mapper.(apiv3.ParsePopulator); ok && populator != nil {
					cf, score := populator.PopulateParseCustomFormats(r.Context(), subject)
					out.CustomFormats = cf
					out.CustomFormatScore = score
				}
			}
		}
		writeJSON(w, out)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	buf, _ := json.Marshal(v)
	_, _ = w.Write(buf)
}
