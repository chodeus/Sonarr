package v3

import (
	"context"
	"encoding/json"
	"net/http"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
)

// ParseResource mirrors `src/Sonarr.Api.V3/Parse/ParseResource.cs`.
// The full populated shape carries Series / Episodes / Languages /
// CustomFormats joined fields; J344 (Arc 2, slice 1) wires Series +
// Episodes + Languages via parsingService.Map. CustomFormats remains
// DEFERRED — needs the aggregation.Service.Augment + custom-format
// calculator chain off RemoteEpisode (the next slice in the arc).
//
// The customFormats[] + customFormatScore fields are wired at the
// struct level here (J299, Wave 1 calculator wire-through slice 4)
// so future populated-row mappers can emit the C# wire shape
// without re-widening; the underlying calculator engine
// ([sonarr.tv/sonarr/internal/customformats.Calculate] +
// [sonarr.tv/sonarr/internal/qualityprofiles.CalculateCustomFormatScore])
// is already ported. CustomFormats uses `omitempty` to match C#
// `JsonIgnore(WhenWritingNull)` on the nullable `List<CustomFormatResource>?`
// — at the current deferred state the field is nil and omitted from
// the wire. CustomFormatScore is plain `int` with no omitempty so
// the wire always carries `customFormatScore: 0` — matches C# v3's
// non-nullable `int CustomFormatScore` which serializes the zero
// value.
//
// J344 adds Series + Episodes + Languages with omitempty. C# emits
// these via `JsonIgnore(WhenWritingNull)` on the nullable List<>
// fields — when parsingService.Map returns no series, C# omits the
// keys entirely. Go-port matches via omitempty: nil slice / nil
// pointer drops the JSON key. Existing minimal-shape callers stay
// byte-equivalent.
type ParseResource struct {
	ID                int                       `json:"id,omitempty"`
	Title             string                    `json:"title"`
	ParsedEpisodeInfo *parser.ParsedEpisodeInfo `json:"parsedEpisodeInfo,omitempty"`
	Series            *SeriesResource           `json:"series,omitempty"`
	Episodes          []EpisodeResource         `json:"episodes,omitempty"`
	Languages         []language.Language       `json:"languages,omitempty"`
	CustomFormats     []CustomFormatResource    `json:"customFormats,omitempty"`
	CustomFormatScore int                       `json:"customFormatScore"`
}

// ParseMapper is the narrow parsing-service surface used by the Parse
// handler's populated-mapper wire-through. Satisfied by
// `*parsingservice.Service.Map` via Go structural typing — no adapter
// needed at the wire site. Nil-tolerant from the handler: when nil,
// the handler emits the minimal Title + ParsedEpisodeInfo shape (pre-
// J344 behaviour preserved).
//
// J344.
type ParseMapper interface {
	Map(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, tvdbID, tvRageID int, imdbID string, searchCriteria *decisionengine.SearchCriteria) (*decisionengine.Subject, error)
}

// ParsePopulator is the optional-extension type-assertion surface for
// computing CustomFormats + CustomFormatScore against a populated
// Subject. Mirrors C# `ParseController.cs:53-54` where
// `_formatCalculator.ParseCustomFormat(remoteEpisode, 0)` + the
// QualityProfile-driven `CalculateCustomFormatScore` run after the
// Map projection completes. Satisfied by a wrapper struct in
// cmd/sonarr that bundles the [parsingservice.Service.Map] result with
// the calculator deps (customformats.Service.All + qualityprofiles.
// Service.Get) — the same composition pattern J339 / J341 used for
// EpisodeFile + SignalR broadcasts.
//
// The handler type-asserts mapper for this interface (J338 / J339 /
// J342 idiom) so production wiring can swap the wrapper in without
// changing handler signatures; test fakes that satisfy ParseMapper
// but NOT ParsePopulator stay green (empty CustomFormats + 0 score
// continue to flow).
//
// J345.
type ParsePopulator interface {
	PopulateParseCustomFormats(ctx context.Context, subject *decisionengine.Subject) ([]CustomFormatResource, int)
}

// ParseHandler returns GET /api/v3/parse. Mirrors C#
// `ParseController.Parse` at ParseController.cs:31-77 which:
//
//  1. Empty title -> null response (200 with body `null`).
//  2. ParseTitle/ParsePath -> ParsedEpisodeInfo.
//  3. If parsingService.Map succeeds -> populated Resource with
//     Series/Episodes/Languages/CustomFormats.
//  4. Otherwise -> Resource{Title, ParsedEpisodeInfo}.
//
// J344 wires step 3 for Series + Episodes + Languages via the new
// optional ParseMapper dep. CustomFormats remains DEFERRED — needs
// the aggregation.Service.Augment + custom-format calculator chain
// (next slice in the Arc 2 sub-arc).
//
// The mapper is nil-tolerant: when nil, the handler emits the pre-
// J344 minimal shape (Title + ParsedEpisodeInfo only). When non-nil
// and Map returns a non-nil Subject with non-nil Series, projects
// Series via ToSeriesResource, each Episode via ToEpisodeResource,
// and Languages from the subject's parser-set list. When Map returns
// a nil Subject OR a Subject with nil Series, falls back to the
// minimal shape (matches C# `:51` `if (remoteEpisode != null)` else
// branch). Map errors degrade silently to the minimal shape — a
// parsing-service hiccup shouldn't fail the whole request.
func ParseHandler(mapper ParseMapper) http.Handler {
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
		// J344 — Map wire-through. Only attempt when both the mapper is
		// wired AND parsing yielded a non-nil ParsedEpisodeInfo (Map's
		// internal contract requires non-nil parsedInfo).
		if mapper != nil && parsed != nil {
			subject, err := mapper.Map(r.Context(), parsed, 0, 0, "", nil)
			if err == nil && subject != nil && subject.Series != nil {
				seriesRes := ToSeriesResource(*subject.Series)
				out.Series = &seriesRes
				if len(subject.Episodes) > 0 {
					eps := make([]EpisodeResource, 0, len(subject.Episodes))
					for _, e := range subject.Episodes {
						eps = append(eps, ToEpisodeResource(e))
					}
					out.Episodes = eps
				}
				out.Languages = subject.Languages
				// J345 — CustomFormats + Score via optional-extension
				// type-assertion. Populator non-nil → invoke against the
				// just-mapped Subject; populator nil → CustomFormats stays
				// nil + Score stays 0 (J338/J339/J342 pattern).
				if populator, ok := mapper.(ParsePopulator); ok && populator != nil {
					cf, score := populator.PopulateParseCustomFormats(r.Context(), subject)
					out.CustomFormats = cf
					out.CustomFormatScore = score
				}
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
