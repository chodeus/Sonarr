package episodeimport

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// FullSeasonSpecification ports
// `src/NzbDrone.Core/MediaFiles/EpisodeImport/Specifications/FullSeasonSpecification.cs:7-32`.
//
// Rejects a single file whose parsed name says "this is a season pack
// containing every episode" — those should be unpacked + re-parsed
// rather than imported as one giant file. Defensive accept on nil
// FileEpisodeInfo (the orchestrator's parse failed; downstream
// InvalidSeasonOrEpisode rejection will catch the file).
type FullSeasonSpecification struct {
	log *slog.Logger
}

// NewFullSeasonSpecification binds a logger. nil log defaults to
// slog.Default. Mirrors the C# 1-arg ctor at `:11-14`.
func NewFullSeasonSpecification(log *slog.Logger) *FullSeasonSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &FullSeasonSpecification{log: log}
}

// Name returns the C# class name verbatim — log lines show the same
// spec identifier on both ports.
func (s *FullSeasonSpecification) Name() string { return "FullSeasonSpecification" }

// IsSatisfiedBy mirrors `:16-30`. Accept on nil FileEpisodeInfo /
// FileEpisodeInfo.FullSeason=false; reject otherwise.
func (s *FullSeasonSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.FileEpisodeInfo == nil {
		return AcceptImportSpec()
	}
	if le.FileEpisodeInfo.FullSeason {
		s.log.Debug("episodeimport: single file detected as containing all episodes in the season",
			"path", le.Path)
		return RejectImportSpec(ImportRejectionFullSeason,
			"Single episode file contains all episodes in seasons. Review file name or manually import")
	}
	return AcceptImportSpec()
}

// SplitEpisodeSpecification ports
// `Specifications/SplitEpisodeSpecification.cs:7-32`.
//
// Rejects a file whose parsed name says "this is one logical episode
// split across multiple files" — Sonarr's library model is one
// EpisodeFile per episode, so a split-episode pair would either
// double-import or mismatch the EpisodeFile→Episode FK. Better to
// reject early and surface the user-action required (manual import).
//
// Defensive accept on nil FileEpisodeInfo (handled downstream).
type SplitEpisodeSpecification struct {
	log *slog.Logger
}

// NewSplitEpisodeSpecification binds a logger.
func NewSplitEpisodeSpecification(log *slog.Logger) *SplitEpisodeSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &SplitEpisodeSpecification{log: log}
}

// Name returns the C# class name verbatim.
func (s *SplitEpisodeSpecification) Name() string { return "SplitEpisodeSpecification" }

// IsSatisfiedBy mirrors `:16-30`.
func (s *SplitEpisodeSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.FileEpisodeInfo == nil {
		return AcceptImportSpec()
	}
	if le.FileEpisodeInfo.IsSplitEpisode {
		s.log.Debug("episodeimport: single episode split into multiple files", "path", le.Path)
		return RejectImportSpec(ImportRejectionSplitEpisode,
			"Single episode split into multiple files")
	}
	return AcceptImportSpec()
}

// SameEpisodesEpisodeFinder is the slice of `IEpisodeService` the
// shared SameEpisodes helper reaches for at C#
// `DecisionEngine/Specifications/SameEpisodesSpecification.cs:24` —
// `GetEpisodesByFileId(int)`. Pulled out so the helper is testable
// without a real `*episodes.Service`. Production wiring binds
// `*episodes.Service.GetEpisodesByFileID` directly.
type SameEpisodesEpisodeFinder interface {
	GetEpisodesByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// IsSameEpisodes ports the shared C# helper at
// `DecisionEngine/Specifications/SameEpisodesSpecification.cs:17-33`.
// Returns true when every file currently linked to one of the
// candidate's episodes covers a subset of the candidate's episode
// list — i.e. importing this file won't leave an existing on-disk
// file orphaned with episodes the new file doesn't cover.
//
// Lives in episodeimport rather than decisionengine because:
//
//   - SameEpisodesImportSpecification (this slice) is the only ported
//     consumer; SameEpisodesGrabSpecification (per-release) hasn't
//     ported yet and would need its own decisionengine.Specification
//     adapter when it does.
//   - The C# class lives in DecisionEngine/Specifications but
//     implements no Specification interface — it's a plain helper
//     class consumed by both per-import and per-release specs. Go
//     port mirrors that as a free function with the EpisodeFinder
//     dep injected at the call site.
//
// Errors propagate verbatim — the caller (SameEpisodesImportSpecification)
// translates "couldn't load" into Accept (defensive: don't block
// imports on a transient lookup error).
func IsSameEpisodes(ctx context.Context, finder SameEpisodesEpisodeFinder, eps []episodes.Episode) (bool, error) {
	if finder == nil {
		return false, errInvalidArg("IsSameEpisodes: nil finder")
	}
	if len(eps) == 0 {
		return true, nil
	}

	episodeIDs := make(map[int]struct{}, len(eps))
	for _, e := range eps {
		episodeIDs[e.ID] = struct{}{}
	}

	// Distinct EpisodeFileIDs the candidate's episodes are linked to.
	// EpisodeFileID is nullable in the Go port; nil means "not yet
	// linked" which the C# port skips via `c.EpisodeFileId != 0`.
	seenFiles := make(map[int]struct{})
	for _, e := range eps {
		if e.EpisodeFileID == nil || *e.EpisodeFileID == 0 {
			continue
		}
		seenFiles[*e.EpisodeFileID] = struct{}{}
	}

	for fileID := range seenFiles {
		linkedEpisodes, err := finder.GetEpisodesByFileID(ctx, fileID)
		if err != nil {
			return false, err
		}
		for _, le := range linkedEpisodes {
			if _, ok := episodeIDs[le.ID]; !ok {
				// Existing on-disk file covers an episode not in the
				// candidate's set → importing this candidate would
				// orphan that episode.
				return false, nil
			}
		}
	}
	return true, nil
}

// errInvalidArg keeps the misuse path explicit at the boundary; the
// helper rejects nil-finder calls loudly rather than silently returning
// `(false, nil)` which would be indistinguishable from a real reject.
func errInvalidArg(msg string) error { return invalidArgError(msg) }

type invalidArgError string

func (e invalidArgError) Error() string { return string(e) }

// SameEpisodesImportSpecification ports
// `Specifications/SameEpisodesImportSpecification.cs:11-32`.
//
// Wraps [IsSameEpisodes] with the import-context translation: a false
// result becomes an `ExistingFileHasMoreEpisodes` rejection. Lookup
// errors are logged-and-accepted so a transient DB outage doesn't
// bounce every candidate file in a batch.
type SameEpisodesImportSpecification struct {
	finder SameEpisodesEpisodeFinder
	log    *slog.Logger
}

// NewSameEpisodesImportSpecification binds an EpisodeFinder + logger.
// Returns an error when finder is nil — the spec can't make a
// meaningful decision without the lookup, and silently accepting
// would let same-episodes orphans into the library.
func NewSameEpisodesImportSpecification(finder SameEpisodesEpisodeFinder, log *slog.Logger) (*SameEpisodesImportSpecification, error) {
	if finder == nil {
		return nil, errInvalidArg("NewSameEpisodesImportSpecification: nil EpisodeFinder")
	}
	if log == nil {
		log = slog.Default()
	}
	return &SameEpisodesImportSpecification{finder: finder, log: log}, nil
}

// Name returns the C# class name verbatim.
func (s *SameEpisodesImportSpecification) Name() string { return "SameEpisodesImportSpecification" }

// IsSatisfiedBy mirrors `:24-31`. Lookup errors translate to Accept
// + a Warn log so the spec doesn't auto-reject a file whose linked
// episodes happened to be unreachable.
//
// Spec context note: this method has no `ctx` parameter on the C#
// side — the Go port adds it via the LocalEpisode pipeline. For now
// the spec uses [context.Background] because the surrounding
// orchestrator's IsSatisfiedBy interface is ctx-less per the
// existing decisionengine.Specification surface. When the
// orchestrator slice (slice 7) plumbs ctx through Specification.IsSatisfiedBy,
// this falls into place.
func (s *SameEpisodesImportSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil {
		return AcceptImportSpec()
	}
	ok, err := IsSameEpisodes(context.Background(), s.finder, le.Episodes)
	if err != nil {
		s.log.Warn("episodeimport: SameEpisodes lookup failed; accepting defensively",
			"path", le.Path, "err", err)
		return AcceptImportSpec()
	}
	if ok {
		return AcceptImportSpec()
	}
	s.log.Debug("episodeimport: existing file on disk covers episodes the candidate does not",
		"path", le.Path)
	return RejectImportSpec(ImportRejectionExistingFileHasMoreEpisodes,
		"Episode file on disk contains more episodes than this file contains")
}

// UnverifiedSceneNumberingSpecification ports
// `Specifications/UnverifiedSceneNumberingSpecification.cs:7-32`.
//
// Rejects a not-yet-existing file whose any matched episode carries
// the `UnverifiedSceneNumbering` flag — Sonarr won't auto-import
// against scene numbering that hasn't been confirmed on TheXEM
// because the matching could be wrong. Existing files (library
// rescan flow) skip the check since the user has already ack'd
// the file's place in the library.
type UnverifiedSceneNumberingSpecification struct {
	log *slog.Logger
}

// NewUnverifiedSceneNumberingSpecification binds a logger.
func NewUnverifiedSceneNumberingSpecification(log *slog.Logger) *UnverifiedSceneNumberingSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &UnverifiedSceneNumberingSpecification{log: log}
}

// Name returns the C# class name verbatim.
func (s *UnverifiedSceneNumberingSpecification) Name() string {
	return "UnverifiedSceneNumberingSpecification"
}

// IsSatisfiedBy mirrors `:16-30`.
func (s *UnverifiedSceneNumberingSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil {
		return AcceptImportSpec()
	}
	if le.ExistingFile {
		s.log.Debug("episodeimport: skipping scene numbering check for existing episode", "path", le.Path)
		return AcceptImportSpec()
	}
	for _, e := range le.Episodes {
		if e.UnverifiedSceneNumbering {
			s.log.Debug("episodeimport: file uses unverified scene numbers; skipping auto-import",
				"path", le.Path)
			return RejectImportSpec(ImportRejectionUnverifiedSceneMapping,
				"This show has individual episode mappings on TheXEM but the mapping for this episode has not been confirmed yet by their administrators. TheXEM needs manual input.")
		}
	}
	return AcceptImportSpec()
}

// HasAudioTrackSpecification ports
// `Specifications/HasAudioTrackSpecification.cs:7-33`.
//
// Rejects a file whose MediaInfo blob reports zero audio tracks — a
// silent video file is almost certainly a corrupt grab. Defensive
// accept on nil MediaInfo (ffprobe missing / pre-aggregation):
// matches C# `:18-22` which logs a debug and accepts the file.
type HasAudioTrackSpecification struct {
	log *slog.Logger
}

// NewHasAudioTrackSpecification binds a logger.
func NewHasAudioTrackSpecification(log *slog.Logger) *HasAudioTrackSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &HasAudioTrackSpecification{log: log}
}

// Name returns the C# class name verbatim.
func (s *HasAudioTrackSpecification) Name() string { return "HasAudioTrackSpecification" }

// IsSatisfiedBy mirrors `:16-31`.
func (s *HasAudioTrackSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.MediaInfo == nil {
		// MediaInfo unavailable — log + accept matches C# defensive
		// posture. The downstream FFprobe + aggregation slice may
		// re-evaluate.
		if le != nil {
			s.log.Debug("episodeimport: no MediaInfo on file; skipping audio-track check",
				"path", le.Path)
		}
		return AcceptImportSpec()
	}
	if len(le.MediaInfo.AudioStreams) == 0 {
		s.log.Debug("episodeimport: no audio tracks found in file", "path", le.Path)
		return RejectImportSpec(ImportRejectionNoAudio, "No audio tracks detected")
	}
	return AcceptImportSpec()
}

// SampleChecker is the slice of [SampleDetector] the
// [NotSampleSpecification] reaches for — `IsSample(ctx, *LocalEpisode)`
// returning a tri-state. Pulled out as a narrow interface so the spec
// is testable without binding the full SampleDetector ceremony in
// every fixture. Production wiring binds `*SampleDetector` directly.
type SampleChecker interface {
	IsSample(ctx context.Context, le *LocalEpisode) DetectSampleResult
}

// NotSampleSpecification ports
// `Specifications/NotSampleSpecification.cs:8-49`.
//
// Skips the sample check for files already living in the library
// (ExistingFile=true) and for files without a configured detector.
// Otherwise consults [SampleChecker] and rejects Sample /
// SampleIndeterminate verdicts.
//
// Like [SameEpisodesImportSpecification], this spec uses
// [context.Background] internally — the orchestrator's
// [Specification.IsSatisfiedBy] surface is ctx-less; slice 7 (the
// orchestrator port) plumbs ctx through end-to-end.
type NotSampleSpecification struct {
	detector SampleChecker
	log      *slog.Logger
}

// NewNotSampleSpecification binds a [SampleChecker] + logger. nil
// detector is allowed — the spec then accepts every candidate (matches
// the pre-aggregation case where SampleDetector hasn't been wired
// yet).
func NewNotSampleSpecification(detector SampleChecker, log *slog.Logger) *NotSampleSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &NotSampleSpecification{detector: detector, log: log}
}

// Name returns the C# class name verbatim.
func (s *NotSampleSpecification) Name() string { return "NotSampleSpecification" }

// IsSatisfiedBy mirrors `:20-47`. Defensive accept on nil
// LocalEpisode (the orchestrator never feeds nil but the spec must
// behave the same as the rest of the batch).
//
// The C# version catches `InvalidSeasonException` and logs+accepts;
// the Go [SampleDetector.IsSample] internalises the multi-season
// short-circuit (returns Indeterminate) so no equivalent try/catch
// is needed here.
func (s *NotSampleSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil {
		return AcceptImportSpec()
	}
	if le.ExistingFile {
		s.log.Debug("episodeimport: existing file, skipping sample check", "path", le.Path)
		return AcceptImportSpec()
	}
	if s.detector == nil {
		s.log.Debug("episodeimport: no sample detector wired; skipping sample check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	switch s.detector.IsSample(context.Background(), le) {
	case DetectSampleSample:
		return RejectImportSpec(ImportRejectionSample, "Sample")
	case DetectSampleIndeterminate:
		return RejectImportSpec(ImportRejectionSampleIndeterminate,
			"Unable to determine if file is a sample")
	}
	return AcceptImportSpec()
}

// FileNamer is the slice of `IBuildFileNames` the absolute / episode-
// title specs reach for. Implementations are typically backed by the
// active naming-config row + the [organizer] free functions
// (`organizer.RequiresAbsoluteEpisodeNumber`,
// `organizer.RequiresEpisodeTitle`).
//
// Mirrors the relevant subset of C# `IBuildFileNames` at
// `Organizer/FileNameBuilder.cs:21-31`. Production wiring composes a
// thin adapter over [namingconfig.Service] — the adapter loads the
// active config row once per import batch and forwards into the
// organizer helpers. Specs themselves stay stateless.
type FileNamer interface {
	// RequiresAbsoluteEpisodeNumber returns true when the active
	// AnimeEpisodeFormat needs `{absolute}`.
	RequiresAbsoluteEpisodeNumber() bool

	// RequiresEpisodeTitle returns true when the active per-series-type
	// pattern includes an `{Episode Title}` / `{Episode CleanTitle}`
	// token. Series + episodes select which pattern applies (see
	// `organizer.RequiresEpisodeTitle`).
	RequiresEpisodeTitle(s *series.Series, eps []episodes.Episode) bool
}

// AbsoluteEpisodeNumberSpecification ports
// `Specifications/AbsoluteEpisodeNumberSpecification.cs:11-58`.
//
// Rejects an anime import when the active naming pattern requires the
// absolute-episode-number token AND any matched episode is recently
// aired (within 1 day) without an absolute episode number.
//
// Skip arms (ordered):
//
//   - Series.SeriesType ≠ Anime → Accept (the pattern's anime-only
//     token doesn't apply).
//   - !FileNamer.RequiresAbsoluteEpisodeNumber() → Accept (user's
//     pattern doesn't reference {absolute}).
//   - Episode aired > 1 day ago → continue (TheTVDB sometimes lags
//     for non-anime episodes; the C# test fixture matches).
//   - AbsoluteEpisodeNumber=nil + within window → Reject.
type AbsoluteEpisodeNumberSpecification struct {
	namer FileNamer
	clock func() time.Time
	log   *slog.Logger
}

// NewAbsoluteEpisodeNumberSpecification binds a [FileNamer]. Returns
// an error on nil namer — the spec can't make a decision without it.
//
// clock is the time source used by the "aired > 1 day ago" arm; nil
// defaults to [time.Now]. Tests override to pin a deterministic
// reference point.
func NewAbsoluteEpisodeNumberSpecification(namer FileNamer, clock func() time.Time, log *slog.Logger) (*AbsoluteEpisodeNumberSpecification, error) {
	if namer == nil {
		return nil, errors.New("episodeimport: NewAbsoluteEpisodeNumberSpecification: nil FileNamer")
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &AbsoluteEpisodeNumberSpecification{namer: namer, clock: clock, log: log}, nil
}

// Name returns the C# class name verbatim.
func (s *AbsoluteEpisodeNumberSpecification) Name() string {
	return "AbsoluteEpisodeNumberSpecification"
}

// IsSatisfiedBy mirrors `:22-56`.
func (s *AbsoluteEpisodeNumberSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.Series == nil {
		return AcceptImportSpec()
	}
	if le.Series.SeriesType != series.SeriesTypeAnime {
		s.log.Debug("episodeimport: series is not anime; skipping absolute-episode-number check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	if !s.namer.RequiresAbsoluteEpisodeNumber() {
		s.log.Debug("episodeimport: file-name format does not require absolute episode number; skipping",
			"path", le.Path)
		return AcceptImportSpec()
	}
	cutoff := s.clock().UTC().AddDate(0, 0, -1)
	for _, e := range le.Episodes {
		if e.AirDateUtc != nil && e.AirDateUtc.Before(cutoff) {
			s.log.Debug("episodeimport: episode aired more than 1 day ago",
				"episodeId", e.ID)
			continue
		}
		if e.AbsoluteEpisodeNumber == nil {
			s.log.Debug("episodeimport: episode missing absolute episode number and recently aired",
				"path", le.Path, "episodeId", e.ID)
			return RejectImportSpec(ImportRejectionMissingAbsoluteEpisodeNumber,
				"Episode does not have an absolute episode number and recently aired")
		}
	}
	return AcceptImportSpec()
}

// EpisodeTitleConfig is the slice of [config.Provider] the
// [EpisodeTitleSpecification] reaches for. Returns the typed enum so
// the spec doesn't re-parse on every call. Production wiring composes
// `func() EpisodeTitleRequiredType { return ParseEpisodeTitleRequiredType(p.EpisodeTitleRequired()) }`.
type EpisodeTitleConfig interface {
	EpisodeTitleRequired() EpisodeTitleRequiredType
}

// EpisodeFinderBySeason is the slice of `IEpisodeService` the
// [EpisodeTitleSpecification] reaches for —
// `GetEpisodesBySeason(seriesId, seasonNumber)`. Pulled out as a
// narrow interface so the spec is testable without a real
// `*episodes.Service`.
type EpisodeFinderBySeason interface {
	GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error)
}

// EpisodeTitleSpecification ports
// `Specifications/EpisodeTitleSpecification.cs:13-97`.
//
// Rejects an import when the active naming pattern needs the episode
// title AND a matched episode is recently aired (within 48h) without
// a real title (or with a literal "TBA" title).
//
// Skip arms (ordered):
//
//   - LocalEpisode.ExistingFile=true → Accept (library rescan flow).
//   - EpisodeTitleRequired=Never → Accept.
//   - !FileNamer.RequiresEpisodeTitle(series, eps) → Accept.
//   - EpisodeTitleRequired=BulkSeasonReleases AND
//     all-episodes-aired-on-the-same-day AND
//     <4 episodes air on that day in the season → Accept (this is a
//     non-bulk release).
//   - per-episode: airDate < now-48h → continue.
//   - per-episode: title="" → Reject TitleMissing.
//   - per-episode: title="TBA" → Reject TitleTba.
//
// C# faithfulness note: the C# call at
// `EpisodeTitleSpecification.cs:55` is
// `_episodeService.GetEpisodesBySeason(firstEpisode.SeriesId,
// firstEpisode.EpisodeNumber)` — passing EpisodeNumber where the
// signature expects seasonNumber. The Go port mirrors this verbatim
// (passing the candidate's first episode's EpisodeNumber as the
// seasonNumber argument) so behaviour matches the C# tree exactly,
// even though it looks like a long-standing typo. Changing it here
// would silently diverge from the C# spec under test parity.
type EpisodeTitleSpecification struct {
	cfg    EpisodeTitleConfig
	namer  FileNamer
	finder EpisodeFinderBySeason
	clock  func() time.Time
	log    *slog.Logger
}

// NewEpisodeTitleSpecification binds the three deps + clock + logger.
// Returns an error on any nil dep — the spec can't make a meaningful
// decision without all three.
//
// clock defaults to [time.Now] when nil.
func NewEpisodeTitleSpecification(cfg EpisodeTitleConfig, namer FileNamer, finder EpisodeFinderBySeason, clock func() time.Time, log *slog.Logger) (*EpisodeTitleSpecification, error) {
	if cfg == nil {
		return nil, errors.New("episodeimport: NewEpisodeTitleSpecification: nil EpisodeTitleConfig")
	}
	if namer == nil {
		return nil, errors.New("episodeimport: NewEpisodeTitleSpecification: nil FileNamer")
	}
	if finder == nil {
		return nil, errors.New("episodeimport: NewEpisodeTitleSpecification: nil EpisodeFinderBySeason")
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &EpisodeTitleSpecification{cfg: cfg, namer: namer, finder: finder, clock: clock, log: log}, nil
}

// Name returns the C# class name verbatim.
func (s *EpisodeTitleSpecification) Name() string { return "EpisodeTitleSpecification" }

// IsSatisfiedBy mirrors `:31-95`. Lookup errors from the season-finder
// translate to Accept (defensive: a transient DB outage shouldn't
// reject every candidate file in a batch).
func (s *EpisodeTitleSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.Series == nil || len(le.Episodes) == 0 {
		return AcceptImportSpec()
	}
	if le.ExistingFile {
		s.log.Debug("episodeimport: existing file in series folder; skipping episode-title check",
			"path", le.Path)
		return AcceptImportSpec()
	}

	required := s.cfg.EpisodeTitleRequired()
	if required == EpisodeTitleRequiredNever {
		s.log.Debug("episodeimport: EpisodeTitleRequired=Never; skipping check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	if !s.namer.RequiresEpisodeTitle(le.Series, le.Episodes) {
		s.log.Debug("episodeimport: file-name format does not require episode title; skipping",
			"path", le.Path)
		return AcceptImportSpec()
	}

	first := le.Episodes[0]
	if required == EpisodeTitleRequiredBulkSeasonReleases && first.AirDateUtc != nil {
		// All candidate episodes air on the same day as the first.
		allSameDay := true
		for _, e := range le.Episodes {
			if e.AirDateUtc != nil && !e.AirDateUtc.Equal(*first.AirDateUtc) {
				allSameDay = false
				break
			}
		}
		if allSameDay {
			// C# passes EpisodeNumber as the seasonNumber arg — mirror
			// verbatim. See type-level note above.
			seasonEps, err := s.finder.GetEpisodesBySeason(context.Background(), first.SeriesID, first.EpisodeNumber)
			if err != nil {
				s.log.Warn("episodeimport: GetEpisodesBySeason failed; accepting defensively",
					"path", le.Path, "err", err)
				return AcceptImportSpec()
			}
			sameDayCount := 0
			for _, e := range seasonEps {
				if e.AirDateUtc == nil || e.AirDateUtc.Equal(*first.AirDateUtc) {
					sameDayCount++
				}
			}
			if sameDayCount < 4 {
				s.log.Debug("episodeimport: bulk-season threshold not met; skipping check",
					"path", le.Path, "sameDayCount", sameDayCount)
				return AcceptImportSpec()
			}
		}
	}

	cutoff := s.clock().UTC().Add(-48 * time.Hour)
	for _, e := range le.Episodes {
		if e.AirDateUtc != nil && e.AirDateUtc.Before(cutoff) {
			s.log.Debug("episodeimport: episode aired more than 48 hours ago",
				"episodeId", e.ID)
			continue
		}
		title := strings.TrimSpace(e.Title)
		if title == "" {
			s.log.Debug("episodeimport: episode has no title and recently aired",
				"path", le.Path, "episodeId", e.ID)
			return RejectImportSpec(ImportRejectionTitleMissing,
				"Episode does not have a title and recently aired")
		}
		// C# uses `title.Equals("TBA")` (case-sensitive, no trim) at
		// `:87`. Mirror exactly — `e.Title == "TBA"` would diverge if
		// the field had whitespace, which the C# version would also
		// pass through.
		if e.Title == "TBA" {
			s.log.Debug("episodeimport: episode has TBA title and recently aired",
				"path", le.Path, "episodeId", e.ID)
			return RejectImportSpec(ImportRejectionTitleTba,
				"Episode has a TBA title and recently aired")
		}
	}
	return AcceptImportSpec()
}

// Compile-time interface satisfaction guards. A signature drift on
// Specification surfaces here at the package boundary rather than at
// the orchestrator's per-spec dispatch site.
var (
	_ Specification = (*FullSeasonSpecification)(nil)
	_ Specification = (*SplitEpisodeSpecification)(nil)
	_ Specification = (*SameEpisodesImportSpecification)(nil)
	_ Specification = (*UnverifiedSceneNumberingSpecification)(nil)
	_ Specification = (*HasAudioTrackSpecification)(nil)
	_ Specification = (*NotSampleSpecification)(nil)
	_ Specification = (*AbsoluteEpisodeNumberSpecification)(nil)
	_ Specification = (*EpisodeTitleSpecification)(nil)
)
