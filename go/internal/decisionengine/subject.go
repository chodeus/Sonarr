package decisionengine

import (
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/series"
)

// Subject is the Go-port equivalent of the C# `RemoteEpisode` at
// `src/NzbDrone.Core/Parser/Model/RemoteEpisode.cs:1-56`. Carries
// every cross-domain context value a Specification needs to evaluate
// one candidate release.
//
// Renamed from the C# "RemoteEpisode" because:
//
//  1. The name is misleading — the type carries the parsed release
//     plus the matched series + episodes + custom formats, NOT just
//     "an episode that lives somewhere remote".
//  2. The Specification interface signature is `IsSatisfiedBy(Subject,
//     ReleaseDecisionInformation)` — `Subject` reads naturally as
//     "the thing under decision" while `RemoteEpisode` would read as
//     a Tv/Series concept it isn't.
//  3. The C# name pre-dates the multi-episode + season-pack support;
//     keeping it would propagate a misnomer through the whole
//     decisionengine surface.
//
// All slice fields are zero-length-not-nil after [NewSubject] so
// downstream specs can iterate without nil checks.
//
// Two notes on field types vs the C# tree:
//
//   - `Release` is `any` (the C# tree uses an inheritance-typed
//     `ReleaseInfo`). The Go port carries the concrete type as `any`
//     so callers can pass either `*parser.ReleaseInfo` (Newznab) or
//     `*parser.TorrentInfo` (Torznab) without an interface cast.
//     The torrent-only fields are reached via `parser.AsTorrentInfo`.
//   - `SceneMapping` is `*scenemappings.Mapping` (the existing port);
//     the C# `SceneMapping` carries one extra computed field
//     (TitleWithoutYear) that the Go port already inlines via
//     `parser.SeriesTitleInfo`.
type Subject struct {
	// Release is the indexer-side release info — `*parser.ReleaseInfo`
	// or `*parser.TorrentInfo`. Use `parser.AsTorrentInfo(s.Release)`
	// to access torrent-only fields.
	Release any
	// ParsedEpisodeInfo is the parser output for Release.Title.
	// Populated by ParsingService.Parse before Subject construction.
	ParsedEpisodeInfo *parser.ParsedEpisodeInfo
	// SceneMapping carries the matched-by-alias mapping (when the
	// parser found one); nil when the release matched canonically.
	SceneMapping *scenemappings.Mapping
	// MappedSeasonNumber is the season number AFTER scene-to-tvdb
	// translation. Equal to ParsedEpisodeInfo.SeasonNumber when no
	// scene mapping fired.
	MappedSeasonNumber int
	// Series is the matched persisted series row. Non-nil for any
	// Subject that reaches the Specification pipeline (the early
	// "unknown series" rejection short-circuits before construction).
	Series *series.Series
	// Episodes is the matched persisted episode rows. Multi-element
	// for multi-episode releases; one element for single-episode;
	// many for season packs.
	Episodes []episodes.Episode
	// EpisodeRequested tags Subjects that were created in response to
	// a user-initiated search (vs an RSS sweep). Specs that want to
	// behave more permissively for explicit searches read this.
	EpisodeRequested bool
	// DownloadAllowed is set by the orchestrator AFTER spec evaluation
	// when the Decision is approved AND no async pre-conditions
	// (queue contention, in-flight grab) are pending. Specs DO NOT
	// read this — it's an output flag, not an input.
	DownloadAllowed bool
	// SeedConfiguration carries the per-grab torrent-seed override.
	// Nil for non-torrent Subjects.
	SeedConfiguration *downloadclients.TorrentSeedConfiguration
	// CustomFormats is the set of CustomFormat rows that matched the
	// release per the customformats package's evaluation. Order is
	// non-significant; specs read by membership.
	CustomFormats []customformats.CustomFormat
	// CustomFormatScore is the precomputed integer score from
	// `qualityprofiles.CalculateCustomFormatScore` against
	// CustomFormats + the candidate's quality profile.
	CustomFormatScore int
	// SeriesMatchType records the path the series-match took (Title /
	// Alias / ID). Surfaced in the v3 history payload.
	SeriesMatchType series.MatchType
	// Languages is the list of detected languages on the release —
	// usually a single entry for non-dual-audio releases. Distinct
	// from ParsedEpisodeInfo.Languages: that's the parser's output;
	// this is the post-Series-edit-language-merge result.
	Languages []language.Language
	// ReleaseSource tags where in the pipeline the release entered
	// (RSS / Search / UserInvokedSearch / InteractiveSearch / Push).
	ReleaseSource ReleaseSourceType
}

// NewSubject builds a zero-value Subject with all slice fields
// allocated (non-nil zero-length). Mirrors the C# default constructor
// at `RemoteEpisode.cs:29-34` which initialises Episodes / CustomFormats
// / Languages to fresh empty Lists.
func NewSubject() *Subject {
	return &Subject{
		Episodes:      []episodes.Episode{},
		CustomFormats: []customformats.CustomFormat{},
		Languages:     []language.Language{},
	}
}

// IsRecentEpisode mirrors the C# helper at `RemoteEpisode.cs:36-39`:
// true iff at least one episode in the Subject aired within the last
// 14 days.
//
// Used by FreeSpaceSpecification + RetentionSpecification — recently-
// aired episodes get more permissive size + age tolerances. The
// 14-day window is a constant in the C# tree; the Go port preserves it.
//
// `now` is parameterised so tests can pin a deterministic clock. The
// C# tree reads DateTime.UtcNow.Date directly; Go callers should
// pass time.Now().UTC() in production and a fixed time in tests.
func (s *Subject) IsRecentEpisode(now time.Time) bool {
	cutoff := now.AddDate(0, 0, -14)
	for _, e := range s.Episodes {
		if e.AirDateUtc == nil {
			continue
		}
		if !e.AirDateUtc.Before(cutoff) {
			return true
		}
	}
	return false
}

// String mirrors the C# default ToString at `:41-44` — returns the
// release title for log lines.
func (s *Subject) String() string {
	if r, ok := s.Release.(*parser.ReleaseInfo); ok && r != nil {
		return r.Title
	}
	if r, ok := s.Release.(*parser.TorrentInfo); ok && r != nil {
		return r.Title
	}
	return "<no release>"
}
