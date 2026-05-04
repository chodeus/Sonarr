package organizer

import (
	"strconv"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// QualityTitleResolver is the slice of `IQualityDefinitionService.Get(quality)`
// the C# `AddQualityTokens` calls at `:634`. Returns the user-overridable
// Title for the given Quality (e.g. `WEBDL-1080p` or a custom rename).
//
// `*qualitydefinitions.Service.Get` satisfies this interface up to a
// thin adapter — its return shape is `*Decorated{Quality, Title, ...}`.
// The host wires the adapter at construction time.
type QualityTitleResolver interface {
	GetQualityTitle(q quality.Quality) (string, bool)
}

// RegisterQualityTokens ports `AddQualityTokens` at `:632-642`.
// Registers `{Quality Title}`, `{Quality Proper}`, `{Quality Real}`,
// and `{Quality Full}` derived from the file's QualityModel + the
// series type (anime emits `vN` for Version > 1; non-anime emits
// `Proper`).
//
// `resolver` may be nil — the {Quality Title} token then falls back
// to the bare `Quality.Name` (matches the C# behaviour when the
// definition service is unavailable, plus the Go preference for
// not-failing on unset deps).
func RegisterQualityTokens(handlers map[string]TokenHandler, s *series.Series, model quality.Model, resolver QualityTitleResolver) {
	title := model.Quality.Name
	if resolver != nil {
		if t, ok := resolver.GetQualityTitle(model.Quality); ok && t != "" {
			title = t
		}
	}
	proper := getQualityProper(s, model)
	real := getQualityReal(model)

	handlers[NormalizeTokenKey("Quality Full")] = func(TokenMatch) string {
		return joinNonEmpty(" ", title, proper, real)
	}
	handlers[NormalizeTokenKey("Quality Title")] = func(TokenMatch) string { return title }
	handlers[NormalizeTokenKey("Quality Proper")] = func(TokenMatch) string { return proper }
	handlers[NormalizeTokenKey("Quality Real")] = func(TokenMatch) string { return real }
}

// getQualityProper ports `:1091-1104`. Anime → `vN`; non-anime → `Proper`
// when Revision.Version > 1; else empty.
//
// The C# `series.SeriesType == SeriesTypes.Anime` is mirrored here via
// the convention that anime Series have SeriesType == 2 (matches
// 2.58's [SeriesTypeAnime] constant). Series.SeriesType is an int
// field on the persisted row.
func getQualityProper(s *series.Series, m quality.Model) string {
	if m.Revision.Version <= 1 {
		return ""
	}
	if s != nil && SeriesType(s.SeriesType) == SeriesTypeAnime {
		return "v" + strconv.Itoa(m.Revision.Version)
	}
	return "Proper"
}

// getQualityReal ports `:1106-1114`. Revision.Real > 0 → `REAL`; else empty.
func getQualityReal(m quality.Model) string {
	if m.Revision.Real > 0 {
		return "REAL"
	}
	return ""
}

// joinNonEmpty joins `parts` with `sep`, skipping empty entries.
// Equivalent to the C# `string.Format("{0} {1} {2}", a, b, c)` with
// the extra-space artifact preserved when middle entries are empty —
// in C# this collapses via downstream FileNameCleanupRegex.
//
// For Go we collapse here so the output doesn't carry double spaces.
func joinNonEmpty(sep string, parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += p
	}
	return out
}
