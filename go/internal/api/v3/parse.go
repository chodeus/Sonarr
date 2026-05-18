package v3

import (
	"encoding/json"
	"net/http"

	"sonarr.tv/sonarr/internal/parser"
)

// ParseResource mirrors `src/Sonarr.Api.V3/Parse/ParseResource.cs`.
// The full populated shape carries Series/Episodes/Languages/
// CustomFormats joined fields; the Go-port skeleton emits the
// title-and-parsed-info pair only (DEFERRED: the parsingService.Map
// pipeline that produces the joined fields - needs metadatasource +
// custom-format calculator integration not yet ported).
//
// `omitempty` semantics match C# `JsonIgnore(WhenWritingNull)` so the
// empty-result shape doesn't emit nil joined fields.
type ParseResource struct {
	ID                int                       `json:"id,omitempty"`
	Title             string                    `json:"title"`
	ParsedEpisodeInfo *parser.ParsedEpisodeInfo `json:"parsedEpisodeInfo,omitempty"`
}

// ParseHandler returns GET /api/v3/parse. Mirrors C#
// `ParseController.Parse` at ParseController.cs:31-77 which:
//
//  1. Empty title -> null response (200 with body `null`).
//  2. ParseTitle/ParsePath -> ParsedEpisodeInfo.
//  3. If parsingService.Map succeeds -> populated Resource with
//     Series/Episodes/Languages/CustomFormats. DEFERRED in Go port.
//  4. Otherwise -> Resource{Title, ParsedEpisodeInfo}.
//
// The Go port runs steps 1 + 2 + 4. The Map() projection is
// DEFERRED with rationale (the parsing-service + custom-format-
// calculator integration isn't ported yet).
func ParseHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		title := q.Get("title")
		path := q.Get("path")
		if title == "" && path == "" {
			// C# `title.IsNullOrWhiteSpace` -> return null. The wire
			// shape on null is JSON `null`.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`null`))
			return
		}
		// Title precedence: the C# action prefers path when supplied,
		// otherwise title. The Go port matches.
		var parsed *parser.ParsedEpisodeInfo
		if path != "" {
			parsed = parser.ParsePath(path)
		} else {
			parsed = parser.ParseTitle(title)
		}
		// C# `if (parsedEpisodeInfo == null) return ParseResource{Title}`.
		// We always emit Title; ParsedEpisodeInfo stays nil if parsing
		// yielded no match.
		out := ParseResource{Title: title, ParsedEpisodeInfo: parsed}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
