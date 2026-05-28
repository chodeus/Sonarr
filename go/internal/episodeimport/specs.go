package episodeimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
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

// FreeSpaceConfig is the slice of [config.Provider] the
// [FreeSpaceSpecification] reaches for. Two getters: the on/off
// toggle and the threshold (in megabytes — matches C# storage at
// `Configuration/ConfigService.cs:202-207`).
type FreeSpaceConfig interface {
	SkipFreeSpaceCheckWhenImporting() bool
	MinimumFreeSpaceWhenImporting() int
}

// AvailableSpaceProvider is the slice of [disk.Provider] the
// [FreeSpaceSpecification] reaches for. `GetAvailableSpace` returns
// `(int64, error)` — the spec treats any error as "couldn't
// determine, accept defensively" (matches C# `!HasValue` arm at
// `Specifications/FreeSpaceSpecification.cs:38-42`).
type AvailableSpaceProvider interface {
	GetAvailableSpace(path string) (int64, error)
}

// FreeSpaceSpecification ports
// `Specifications/FreeSpaceSpecification.cs:11-67`.
//
// Skip arms:
//
//   - SkipFreeSpaceCheckWhenImporting → Accept.
//   - LocalEpisode.ExistingFile → Accept.
//   - GetAvailableSpace returns error OR Series.Path is empty →
//     Accept (defensive: don't bounce a batch on a transient stat
//     failure, matching C# `!freeSpace.HasValue` and the catch-all
//     `try/catch` at `:55-62`).
//
// Reject when free space < (file size + minimum-free-space-MB).
// `MinimumFreeSpaceWhenImporting` is in megabytes; the spec
// multiplies by 1<<20 before the compare.
type FreeSpaceSpecification struct {
	cfg  FreeSpaceConfig
	disk AvailableSpaceProvider
	log  *slog.Logger
}

// NewFreeSpaceSpecification binds the two deps + logger. Returns an
// error on either nil dep.
func NewFreeSpaceSpecification(cfg FreeSpaceConfig, disk AvailableSpaceProvider, log *slog.Logger) (*FreeSpaceSpecification, error) {
	if cfg == nil {
		return nil, errors.New("episodeimport: NewFreeSpaceSpecification: nil FreeSpaceConfig")
	}
	if disk == nil {
		return nil, errors.New("episodeimport: NewFreeSpaceSpecification: nil AvailableSpaceProvider")
	}
	if log == nil {
		log = slog.Default()
	}
	return &FreeSpaceSpecification{cfg: cfg, disk: disk, log: log}, nil
}

// Name returns the C# class name verbatim.
func (s *FreeSpaceSpecification) Name() string { return "FreeSpaceSpecification" }

// IsSatisfiedBy mirrors `:22-65`.
func (s *FreeSpaceSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil {
		return AcceptImportSpec()
	}
	if s.cfg.SkipFreeSpaceCheckWhenImporting() {
		s.log.Debug("episodeimport: free-space check disabled by config; skipping",
			"path", le.Path)
		return AcceptImportSpec()
	}
	if le.ExistingFile {
		s.log.Debug("episodeimport: existing episode; skipping free-space check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	if le.Series == nil || strings.TrimSpace(le.Series.Path) == "" {
		s.log.Debug("episodeimport: no series path; skipping free-space check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	// C# `Directory.GetParent(Series.Path).FullName` — Go equivalent
	// is `filepath.Dir`. The .Path stays absolute through normal
	// usage; the parent-of-parent corner case (root-level series
	// folder) collapses to the same root so `GetAvailableSpace` still
	// resolves a real mount.
	parent := filepath.Dir(le.Series.Path)
	free, err := s.disk.GetAvailableSpace(parent)
	if err != nil {
		s.log.Debug("episodeimport: free-space check failed; accepting defensively",
			"path", parent, "err", err)
		return AcceptImportSpec()
	}
	threshold := le.Size + int64(s.cfg.MinimumFreeSpaceWhenImporting())*(1<<20)
	if free < threshold {
		s.log.Warn("episodeimport: not enough free space to import",
			"path", le.Path, "free", free, "size", le.Size,
			"minMB", s.cfg.MinimumFreeSpaceWhenImporting())
		return RejectImportSpec(ImportRejectionMinimumFreeSpace, "Not enough free space")
	}
	return AcceptImportSpec()
}

// MatchesGrabSpecification ports
// `Specifications/MatchesGrabSpecification.cs:11-58`.
//
// Skip arms (priority order):
//
//   - LocalEpisode.ExistingFile → Accept (library rescan flow).
//   - LocalEpisode.Release nil OR Release.EpisodeIDs empty → Accept
//     (no grabbed-release constraint on this candidate).
//
// Otherwise: any candidate Episode whose ID isn't in EpisodeIDs is
// "unexpected"; reject with EpisodeNotFoundInRelease and a message
// listing the unexpected episodes (`SxxEyy` form) + grabbed title.
type MatchesGrabSpecification struct {
	log *slog.Logger
}

// NewMatchesGrabSpecification binds a logger.
func NewMatchesGrabSpecification(log *slog.Logger) *MatchesGrabSpecification {
	if log == nil {
		log = slog.Default()
	}
	return &MatchesGrabSpecification{log: log}
}

// Name returns the C# class name verbatim.
func (s *MatchesGrabSpecification) Name() string { return "MatchesGrabSpecification" }

// IsSatisfiedBy mirrors `:21-50`.
func (s *MatchesGrabSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.ExistingFile {
		return AcceptImportSpec()
	}
	if le.Release == nil || len(le.Release.EpisodeIDs) == 0 {
		return AcceptImportSpec()
	}
	expectedIDs := make(map[int]struct{}, len(le.Release.EpisodeIDs))
	for _, id := range le.Release.EpisodeIDs {
		expectedIDs[id] = struct{}{}
	}
	var unexpected []episodes.Episode
	for _, e := range le.Episodes {
		if _, ok := expectedIDs[e.ID]; !ok {
			unexpected = append(unexpected, e)
		}
	}
	if len(unexpected) == 0 {
		return AcceptImportSpec()
	}
	formatted := formatEpisodeShortIDs(unexpected)
	s.log.Debug("episodeimport: unexpected episodes in file",
		"path", le.Path, "unexpected", formatted)
	if len(unexpected) == 1 {
		return RejectImportSpec(ImportRejectionEpisodeNotFoundInRelease,
			"Episode %s was not found in the grabbed release: %s", formatted, le.Release.Title)
	}
	return RejectImportSpec(ImportRejectionEpisodeNotFoundInRelease,
		"Episodes %s were not found in the grabbed release: %s", formatted, le.Release.Title)
}

// formatEpisodeShortIDs renders a list of episodes as
// `S01E02, S01E03` for the rejection message. Mirrors C#
// `MatchesGrabSpecification.FormatEpisode` at `:53-56`
// (`{SeasonNumber}x{EpisodeNumber:00}` — note the `x` separator,
// not `S..E..`).
func formatEpisodeShortIDs(eps []episodes.Episode) string {
	parts := make([]string, len(eps))
	for i, e := range eps {
		parts[i] = fmt.Sprintf("%dx%02d", e.SeasonNumber, e.EpisodeNumber)
	}
	return strings.Join(parts, ", ")
}

// UnpackingConfig is the slice of [config.Provider] the
// [NotUnpackingSpecification] reaches for. The stored value is a
// `|`-separated prefix list (default `"_UNPACK_|_FAILED_"`).
type UnpackingConfig interface {
	DownloadClientWorkingFolders() string
}

// FileLastWriteProvider is the slice of [disk.Provider] the
// [NotUnpackingSpecification] reaches for on Windows. Returns the
// candidate file's last-modified time; the spec treats a recent mtime
// (within 5 minutes) as "still being unpacked".
type FileLastWriteProvider interface {
	FileGetLastWrite(path string) (time.Time, error)
}

// NotUnpackingSpecification ports
// `Specifications/NotUnpackingSpecification.cs:12-65`.
//
// Walks the candidate file's parent chain looking for any directory
// whose name starts with a configured working-folder prefix
// (`_UNPACK_`, `_FAILED_`, …). When a match is found:
//
//   - On non-Windows: reject Unpacking immediately. The C# version
//     assumes unpackers atomic-rename the final file out of the
//     prefix dir on POSIX, so any candidate whose ancestor chain
//     STILL contains a prefix dir is in flight.
//   - On Windows: also check `FileGetLastWrite` — if the file's
//     mtime is within 5 minutes of now the unpacker is likely still
//     writing. Older mtimes accept (the unpacker may have died
//     leaving the dir behind).
//
// Skip arm: ExistingFile → Accept (library rescan flow).
type NotUnpackingSpecification struct {
	cfg   UnpackingConfig
	disk  FileLastWriteProvider
	clock func() time.Time
	log   *slog.Logger

	// isWindows is captured at construction so tests can pin both
	// branches deterministically. Production wiring uses
	// `runtime.GOOS == "windows"`.
	isWindows bool
}

// NewNotUnpackingSpecification binds the deps + clock. Returns an
// error on either nil dep. clock defaults to [time.Now]; isWindows
// defaults to `runtime.GOOS == "windows"`.
func NewNotUnpackingSpecification(cfg UnpackingConfig, disk FileLastWriteProvider, clock func() time.Time, log *slog.Logger) (*NotUnpackingSpecification, error) {
	if cfg == nil {
		return nil, errors.New("episodeimport: NewNotUnpackingSpecification: nil UnpackingConfig")
	}
	if disk == nil {
		return nil, errors.New("episodeimport: NewNotUnpackingSpecification: nil FileLastWriteProvider")
	}
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &NotUnpackingSpecification{
		cfg:       cfg,
		disk:      disk,
		clock:     clock,
		log:       log,
		isWindows: runtime.GOOS == "windows",
	}, nil
}

// SetWindowsForTest overrides the OS-detection result. Test-only —
// production code never calls this. The flag drives the
// FileGetLastWrite-vs-immediate-reject branch in [IsSatisfiedBy].
func (s *NotUnpackingSpecification) SetWindowsForTest(v bool) { s.isWindows = v }

// Name returns the C# class name verbatim.
func (s *NotUnpackingSpecification) Name() string { return "NotUnpackingSpecification" }

// IsSatisfiedBy mirrors `:21-63`.
func (s *NotUnpackingSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil {
		return AcceptImportSpec()
	}
	if le.ExistingFile {
		s.log.Debug("episodeimport: existing file in series folder; skipping unpacking check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	prefixes := strings.Split(s.cfg.DownloadClientWorkingFolders(), "|")
	for _, prefix := range prefixes {
		if strings.TrimSpace(prefix) == "" {
			continue
		}
		// Walk the parent chain — `filepath.Dir` returns its arg
		// when at the root (e.g. "/"), so we stop on a fixed-point.
		parent := filepath.Dir(le.Path)
		for {
			base := filepath.Base(parent)
			if strings.HasPrefix(base, prefix) {
				if !s.isWindows {
					s.log.Debug("episodeimport: file is still being unpacked",
						"path", le.Path, "matchedDir", parent)
					return RejectImportSpec(ImportRejectionUnpacking,
						"File is still being unpacked")
				}
				mtime, err := s.disk.FileGetLastWrite(le.Path)
				if err != nil {
					// Stat failure on Windows path — propagate as
					// reject too: the C# version implicitly does the
					// same since FileGetLastWrite throws on missing
					// file.
					s.log.Debug("episodeimport: FileGetLastWrite failed during unpacking check; rejecting",
						"path", le.Path, "err", err)
					return RejectImportSpec(ImportRejectionUnpacking,
						"File is still being unpacked")
				}
				if mtime.After(s.clock().UTC().Add(-5 * time.Minute)) {
					s.log.Debug("episodeimport: file appears to be unpacking still",
						"path", le.Path, "mtime", mtime)
					return RejectImportSpec(ImportRejectionUnpacking,
						"File is still being unpacked")
				}
				break
			}
			next := filepath.Dir(parent)
			if next == parent {
				break
			}
			parent = next
		}
	}
	return AcceptImportSpec()
}

// UpgradeQualityProfileResolver is the slice of the
// `series.Series.QualityProfile` lazy-load the C# spec dereferences
// at `Specifications/UpgradeSpecification.cs:30`. The Go-port
// `series.Series` carries `QualityProfileID`; this resolver hands
// back the parsed [decisionengine.ProfileView] (Items + FormatItems
// already parsed) keyed by series ID.
type UpgradeQualityProfileResolver interface {
	GetProfileViewBySeriesID(ctx context.Context, seriesID int) (*decisionengine.ProfileView, error)
}

// EpisodeFileResolver is the slice of `IEpisodeFileService.Get` the
// C# spec dereferences via the `Episode.EpisodeFile.Value` lazy-load
// at `:32`. Returns the persisted EpisodeFile by ID. nil result with
// nil error means "row not found" — the spec treats that as the
// `episodeFile == null` continue-arm at C# `:34-38`.
type EpisodeFileResolver interface {
	GetByID(ctx context.Context, id int) (*episodefiles.File, error)
}

// CustomFormatCalculator is the slice of
// `ICustomFormatCalculationService.ParseCustomFormat(EpisodeFile, …)`
// the spec calls at C# `:62`. Production wiring composes the
// existing [internal/customformats] machinery against the
// EpisodeFile — for now the spec accepts the dependency as a
// callback so the wiring slice can swap in the real calculation
// later.
type CustomFormatCalculator interface {
	ParseCustomFormat(ctx context.Context, file *episodefiles.File) ([]customformats.CustomFormat, error)
}

// UpgradeConfig is the slice of [config.Provider] the
// [UpgradeSpecification] reaches for — the user's
// proper/repack preference.
type UpgradeConfig interface {
	DownloadPropersAndRepacks() quality.ProperDownloadType
}

// UpgradeSpecification ports
// `Specifications/UpgradeSpecification.cs:13-104`.
//
// Per-existing-EpisodeFile-on-disk veto pass:
//
//   - For each candidate episode with EpisodeFileID > 0 (i.e. there's
//     already a file on disk for this episode):
//   - Resolve the EpisodeFile. nil → continue (C# trace + skip).
//   - Quality compare: candidate < existing → reject NotQualityUpgrade.
//   - Same quality + propers ON + new revision < existing →
//     reject NotRevisionUpgrade.
//   - Same quality + new format score < existing format score:
//   - If the pre-rename score WOULD have improved →
//     reject NotCustomFormatUpgradeAfterRename.
//   - Otherwise → reject NotCustomFormatUpgrade.
//
// All rejection messages mirror the C# format-string shape so log
// lines match the C# tree.
//
// Slice 5 ships the spec wired against narrow interfaces; production
// adapters land alongside the orchestrator slice (7) so the
// ProfileView-per-batch caching layer can sit between this spec and
// the [decisionengine.NewProfileView] call.
type UpgradeSpecification struct {
	cfg              UpgradeConfig
	profileResolver  UpgradeQualityProfileResolver
	fileResolver     EpisodeFileResolver
	formatCalculator CustomFormatCalculator
	log              *slog.Logger
}

// NewUpgradeSpecification binds the four deps + logger. Returns an
// error on any nil dep — every dep is essential to the spec's
// reasoning.
func NewUpgradeSpecification(
	cfg UpgradeConfig,
	profileResolver UpgradeQualityProfileResolver,
	fileResolver EpisodeFileResolver,
	formatCalculator CustomFormatCalculator,
	log *slog.Logger,
) (*UpgradeSpecification, error) {
	if cfg == nil {
		return nil, errors.New("episodeimport: NewUpgradeSpecification: nil UpgradeConfig")
	}
	if profileResolver == nil {
		return nil, errors.New("episodeimport: NewUpgradeSpecification: nil UpgradeQualityProfileResolver")
	}
	if fileResolver == nil {
		return nil, errors.New("episodeimport: NewUpgradeSpecification: nil EpisodeFileResolver")
	}
	if formatCalculator == nil {
		return nil, errors.New("episodeimport: NewUpgradeSpecification: nil CustomFormatCalculator")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpgradeSpecification{
		cfg:              cfg,
		profileResolver:  profileResolver,
		fileResolver:     fileResolver,
		formatCalculator: formatCalculator,
		log:              log,
	}, nil
}

// Name returns the C# class name verbatim.
func (s *UpgradeSpecification) Name() string { return "UpgradeSpecification" }

// IsSatisfiedBy mirrors `:23-102`. Lookup errors translate to Accept
// + a Warn log so a transient DB outage doesn't bounce every
// candidate file in a batch (defensive posture matching the
// SameEpisodes / EpisodeTitle specs).
func (s *UpgradeSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.Series == nil {
		return AcceptImportSpec()
	}
	view, err := s.profileResolver.GetProfileViewBySeriesID(context.Background(), le.Series.ID)
	if err != nil || view == nil {
		s.log.Warn("episodeimport: UpgradeSpec profile lookup failed; accepting defensively",
			"path", le.Path, "seriesId", le.Series.ID, "err", err)
		return AcceptImportSpec()
	}
	downloadPropers := s.cfg.DownloadPropersAndRepacks()

	for _, e := range le.Episodes {
		if e.EpisodeFileID == nil || *e.EpisodeFileID == 0 {
			continue
		}
		file, err := s.fileResolver.GetByID(context.Background(), *e.EpisodeFileID)
		if err != nil {
			s.log.Warn("episodeimport: UpgradeSpec EpisodeFile lookup failed; accepting defensively",
				"path", le.Path, "episodeFileId", *e.EpisodeFileID, "err", err)
			return AcceptImportSpec()
		}
		if file == nil {
			s.log.Debug("episodeimport: UpgradeSpec — episodeFile row not found; continuing",
				"episodeId", e.ID, "episodeFileId", *e.EpisodeFileID)
			continue
		}

		fileQuality, err := decodeEpisodeFileQuality(file)
		if err != nil {
			s.log.Warn("episodeimport: UpgradeSpec EpisodeFile.Quality decode failed; accepting defensively",
				"path", le.Path, "episodeFileId", file.ID, "err", err)
			return AcceptImportSpec()
		}

		qualityCompare := decisionengine.CompareQualityModels(view.Items, le.Quality, fileQuality, false)
		// CompareQualityModels also factors in revisions; we need
		// the pure-quality compare for the C# `qualityCompare<0`
		// arm, then a separate revision compare for the
		// propers-and-repacks arm. Re-derive both from the
		// constituents.
		qualityOnlyCompare := decisionengine.CompareQualityModels(
			view.Items,
			quality.Model{Quality: le.Quality.Quality},
			quality.Model{Quality: fileQuality.Quality},
			false,
		)
		_ = qualityCompare // CompareQualityModels with revisions left for future use; unused below.

		if qualityOnlyCompare < 0 {
			s.log.Debug("episodeimport: file isn't a quality upgrade for all episodes",
				"path", le.Path, "existing", fileQuality.Quality, "new", le.Quality.Quality)
			return RejectImportSpec(ImportRejectionNotQualityUpgrade,
				"Not an upgrade for existing episode file(s). Existing quality: %s. New Quality %s.",
				fileQuality.Quality, le.Quality.Quality)
		}

		if qualityOnlyCompare == 0 &&
			downloadPropers != quality.ProperDoNotPrefer &&
			le.Quality.Revision.Compare(fileQuality.Revision) < 0 {
			s.log.Debug("episodeimport: file isn't a quality revision upgrade",
				"path", le.Path, "existing", fileQuality.Revision, "new", le.Quality.Revision)
			return RejectImportSpec(ImportRejectionNotRevisionUpgrade,
				"Not a quality revision upgrade for existing episode file(s)")
		}

		currentFormats, err := s.formatCalculator.ParseCustomFormat(context.Background(), file)
		if err != nil {
			s.log.Warn("episodeimport: UpgradeSpec ParseCustomFormat failed; accepting defensively",
				"path", le.Path, "err", err)
			return AcceptImportSpec()
		}
		currentFormatScore := scoreCustomFormatsForUpgrade(view, currentFormats)
		newFormatScore := le.CustomFormatScore
		newBeforeRenameScore := le.OriginalFileNameCustomFormatScore

		if qualityOnlyCompare == 0 && newFormatScore < currentFormatScore {
			s.log.Debug("episodeimport: new item's custom formats do not improve",
				"path", le.Path, "newScore", newFormatScore, "currentScore", currentFormatScore)
			if newBeforeRenameScore > currentFormatScore {
				return RejectImportSpec(ImportRejectionNotCustomFormatUpgradeAfterRename,
					"Not a Custom Format upgrade for existing episode file(s). AfterRename: [%s] (%d) do not improve on Existing: [%s] (%d) even though BeforeRename: [%s] (%d) did.",
					formatNames(le.CustomFormats), newFormatScore,
					formatNames(currentFormats), currentFormatScore,
					formatNames(le.OriginalFileNameCustomFormats), newBeforeRenameScore)
			}
			return RejectImportSpec(ImportRejectionNotCustomFormatUpgrade,
				"Not a Custom Format upgrade for existing episode file(s). New: [%s] (%d) do not improve on Existing: [%s] (%d)",
				formatNames(le.CustomFormats), newFormatScore,
				formatNames(currentFormats), currentFormatScore)
		}
	}
	return AcceptImportSpec()
}

// formatNames renders a custom-format list as
// `Name1, Name2` for inclusion in rejection messages. Mirrors the
// C# `IEnumerable<CustomFormat>.ConcatToString()` helper at
// `NzbDrone.Common/Extensions/IEnumerableExtensions.cs` (which
// defaults to `", "` separator). Empty slice → empty string.
func formatNames(formats []customformats.CustomFormat) string {
	if len(formats) == 0 {
		return ""
	}
	parts := make([]string, len(formats))
	for i, f := range formats {
		parts[i] = f.Name
	}
	return strings.Join(parts, ", ")
}

// decodeEpisodeFileQuality unmarshals the embedded JSON Quality
// column on an EpisodeFile row into a typed [quality.Model]. Empty /
// `"{}"` Quality strings are valid input and decode to a zero
// Model — the upgrade comparator handles zero-quality as
// "lower than anything" via the registry's Unknown lookup.
func decodeEpisodeFileQuality(file *episodefiles.File) (quality.Model, error) {
	if file == nil || strings.TrimSpace(file.Quality) == "" || file.Quality == "{}" {
		return quality.Model{}, nil
	}
	var m quality.Model
	if err := json.Unmarshal([]byte(file.Quality), &m); err != nil {
		return quality.Model{}, fmt.Errorf("episodeimport: decode EpisodeFile.Quality: %w", err)
	}
	return m, nil
}

// scoreCustomFormatsForUpgrade computes the cached profile-score for a
// list of CustomFormats. Mirrors the C#
// `qualityProfile.CalculateCustomFormatScore(formats)` pattern via
// the existing Go-port [qualityprofiles.CalculateCustomFormatScore]
// helper (which expects a `present-id → bool` set).
func scoreCustomFormatsForUpgrade(view *decisionengine.ProfileView, formats []customformats.CustomFormat) int {
	if view == nil {
		return 0
	}
	if len(formats) == 0 {
		return qualityprofiles.CalculateCustomFormatScore(view.FormatItems, nil)
	}
	present := make(map[int]bool, len(formats))
	for _, f := range formats {
		present[f.ID] = true
	}
	return qualityprofiles.CalculateCustomFormatScore(view.FormatItems, present)
}

// ImportHistoryFinder is the slice of `IHistoryService` the
// [AlreadyImportedSpecification] reaches for at
// `Specifications/AlreadyImportedSpecification.cs:41` —
// `FindByEpisodeId(int)`. Pulled out as a narrow interface so the spec
// is testable without a real `*history.Service`. Production wiring
// binds `*history.Service.FindByEpisodeId` directly (the lowercase-'d'
// spelling is preserved verbatim across the Go tree for grep parity
// with the C# enum).
type ImportHistoryFinder interface {
	FindByEpisodeId(ctx context.Context, episodeID int) ([]history.Entry, error)
}

// AlreadyImportedSpecification ports
// `Specifications/AlreadyImportedSpecification.cs:11-79`.
//
// Rejects a download-client-driven candidate whose episodes already
// have an on-disk file that was imported from the SAME download id
// after the last grab of that download id — i.e. the file was already
// successfully imported once and re-processing it would double-import.
//
// Skip arms (ordered, matching C# `:27-52`):
//
//   - nil download item → Accept (manual / library-rescan flow has no
//     download id to correlate history against).
//   - per-episode: !HasFile → continue (no on-disk file to have been
//     imported).
//   - per-episode: no prior DownloadFolderImported event for this
//     download id → continue.
//   - per-episode: a later Grabbed event exists for the same download
//     id (release was re-grabbed after the import) → continue.
//
// Reject [ImportRejectionEpisodeAlreadyImported] when a prior import
// for this download id exists AND (there's no matching grab OR the
// import post-dates the matching grab).
//
// Like the other history/parsing-driven specs, lookup errors translate
// to Accept + a Warn log so a transient DB outage doesn't bounce every
// candidate file in a batch (the C# tree lets the exception bubble to
// the orchestrator's EvaluateSpec catch → DecisionError; the Go port
// matches the established defensive idiom across SameEpisodes /
// EpisodeTitle / Upgrade instead).
type AlreadyImportedSpecification struct {
	finder ImportHistoryFinder
	log    *slog.Logger
}

// NewAlreadyImportedSpecification binds the history finder + logger.
// Returns an error on nil finder — the spec can't reason about prior
// imports without the lookup.
func NewAlreadyImportedSpecification(finder ImportHistoryFinder, log *slog.Logger) (*AlreadyImportedSpecification, error) {
	if finder == nil {
		return nil, errors.New("episodeimport: NewAlreadyImportedSpecification: nil ImportHistoryFinder")
	}
	if log == nil {
		log = slog.Default()
	}
	return &AlreadyImportedSpecification{finder: finder, log: log}, nil
}

// Name returns the C# class name verbatim.
func (s *AlreadyImportedSpecification) Name() string { return "AlreadyImportedSpecification" }

// IsSatisfiedBy mirrors `:25-77`.
func (s *AlreadyImportedSpecification) IsSatisfiedBy(le *LocalEpisode, item *downloadclients.Item) *ImportSpecDecision {
	if le == nil || item == nil {
		// nil download item — the C# `:27-31` debug-and-accept arm.
		return AcceptImportSpec()
	}
	for _, e := range le.Episodes {
		// C# `episode.HasFile` ≡ `EpisodeFileId > 0`. The Go-port
		// EpisodeFileID is nullable; nil or 0 means "no on-disk file".
		if e.EpisodeFileID == nil || *e.EpisodeFileID == 0 {
			s.log.Debug("episodeimport: skipping already-imported check for episode without file",
				"episodeId", e.ID)
			continue
		}
		episodeHistory, err := s.finder.FindByEpisodeId(context.Background(), e.ID)
		if err != nil {
			s.log.Warn("episodeimport: AlreadyImported history lookup failed; accepting defensively",
				"path", le.Path, "episodeId", e.ID, "err", err)
			return AcceptImportSpec()
		}
		lastImported := firstHistoryEntry(episodeHistory, item.DownloadID, history.EventTypeDownloadFolderImported)
		lastGrabbed := firstHistoryEntry(episodeHistory, item.DownloadID, history.EventTypeGrabbed)
		if lastImported == nil {
			s.log.Debug("episodeimport: episode file has not been imported", "episodeId", e.ID)
			continue
		}
		if lastGrabbed != nil {
			// Release was grabbed again after importing → don't reject.
			if lastGrabbed.Date.After(lastImported.Date) {
				s.log.Debug("episodeimport: episode file was grabbed again after importing",
					"episodeId", e.ID)
				continue
			}
			// Imported after the last grab → reject.
			if lastImported.Date.After(lastGrabbed.Date) {
				s.log.Debug("episodeimport: episode file previously imported",
					"path", le.Path, "episodeId", e.ID, "importedAt", lastImported.Date)
				return RejectImportSpec(ImportRejectionEpisodeAlreadyImported,
					"Episode file already imported at %s", lastImported.Date.Local())
			}
		} else {
			s.log.Debug("episodeimport: episode file previously imported",
				"path", le.Path, "episodeId", e.ID, "importedAt", lastImported.Date)
			return RejectImportSpec(ImportRejectionEpisodeAlreadyImported,
				"Episode file already imported at %s", lastImported.Date.Local())
		}
	}
	return AcceptImportSpec()
}

// firstHistoryEntry returns a pointer to the first entry in date-DESC
// order matching the supplied download id + event type, or nil when
// none match. Mirrors the C# `episodeHistory.FirstOrDefault(...)`
// calls at `AlreadyImportedSpecification.cs:42-46`. The history repo
// guarantees date-DESC ordering (see [history.Service.FindByEpisodeId])
// so "first" is "most recent".
func firstHistoryEntry(entries []history.Entry, downloadID string, eventType history.EventType) *history.Entry {
	want := int(eventType)
	for i := range entries {
		e := &entries[i]
		if e.DownloadID == downloadID && e.EventType != nil && *e.EventType == want {
			return e
		}
	}
	return nil
}

// FolderMatchParsingService is the slice of `IParsingService` the
// [MatchesFolderSpecification] reaches for at
// `Specifications/MatchesFolderSpecification.cs:35-56` —
// `ParseSpecialEpisodeTitle` (scene-season-special re-parse) +
// `GetEpisodes` (parsed-info → matched-episode list). Pulled out as a
// narrow interface so the spec is testable without a real
// `*parsingservice.Service`. Production wiring binds
// `*parsingservice.Service` directly via Go's structural typing.
//
// The C# `ParseSpecialEpisodeTitle` overload takes
// `(fileInfo, releaseTitle, tvdbId, 0, null)`; the Go-port signature
// takes the resolved `*series.Series` instead of the raw tvdb id (the
// service resolves the series internally), so the narrow interface
// forwards the LocalEpisode's Series.
type FolderMatchParsingService interface {
	ParseSpecialEpisodeTitle(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, releaseTitle string, ser *series.Series) (*parser.ParsedEpisodeInfo, error)
	GetEpisodes(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, ser *series.Series, sceneSource bool) ([]episodes.Episode, error)
}

// MatchesFolderSpecification ports
// `Specifications/MatchesFolderSpecification.cs:12-86`.
//
// Rejects a candidate whose file-name parse maps to episodes that the
// containing folder name did NOT promise — a sign the file was
// misfiled into the wrong release folder. Only meaningful for
// download-folder imports where both the folder and the file carry
// parseable episode info.
//
// Skip arms (ordered, matching C# `:25-62`):
//
//   - LocalEpisode.ExistingFile → Accept (library rescan flow).
//   - folder info nil (after the optional special-title re-parse) →
//     Accept.
//   - file info nil (after the optional special-title re-parse) →
//     Accept.
//   - folder maps to zero episodes → Accept (no constraint to check).
//
// Otherwise: any file-episode whose ID isn't among the folder's
// episodes is "unexpected"; reject [ImportRejectionEpisodeUnexpected]
// with a message naming the unexpected episodes (`SxxEyy`-style via
// [formatEpisodeShortIDs]) + the folder release title.
//
// Lookup / parse errors translate to Accept + a Warn log (defensive
// posture matching the rest of the dep-bearing specs).
type MatchesFolderSpecification struct {
	parsing FolderMatchParsingService
	log     *slog.Logger
}

// NewMatchesFolderSpecification binds the parsing service + logger.
// Returns an error on nil parsing service — the spec can't resolve the
// folder/file episode lists without it.
func NewMatchesFolderSpecification(parsing FolderMatchParsingService, log *slog.Logger) (*MatchesFolderSpecification, error) {
	if parsing == nil {
		return nil, errors.New("episodeimport: NewMatchesFolderSpecification: nil FolderMatchParsingService")
	}
	if log == nil {
		log = slog.Default()
	}
	return &MatchesFolderSpecification{parsing: parsing, log: log}, nil
}

// Name returns the C# class name verbatim.
func (s *MatchesFolderSpecification) Name() string { return "MatchesFolderSpecification" }

// IsSatisfiedBy mirrors `:23-79`.
func (s *MatchesFolderSpecification) IsSatisfiedBy(le *LocalEpisode, _ *downloadclients.Item) *ImportSpecDecision {
	if le == nil || le.Series == nil {
		return AcceptImportSpec()
	}
	if le.ExistingFile {
		return AcceptImportSpec()
	}

	ctx := context.Background()
	fileInfo := le.FileEpisodeInfo
	folderInfo := le.FolderEpisodeInfo

	// Scene-season-special re-parse: a "Season N Special" style title
	// needs the special-episode title resolved against the series
	// before its episode numbers mean anything (C# `:33-41`).
	if fileInfo != nil && fileInfo.IsPossibleSceneSeasonSpecial() {
		reparsed, err := s.parsing.ParseSpecialEpisodeTitle(ctx, fileInfo, fileInfo.ReleaseTitle, le.Series)
		if err != nil {
			s.log.Warn("episodeimport: MatchesFolder file special-title parse failed; accepting defensively",
				"path", le.Path, "err", err)
			return AcceptImportSpec()
		}
		fileInfo = reparsed
	}
	if folderInfo != nil && folderInfo.IsPossibleSceneSeasonSpecial() {
		reparsed, err := s.parsing.ParseSpecialEpisodeTitle(ctx, folderInfo, folderInfo.ReleaseTitle, le.Series)
		if err != nil {
			s.log.Warn("episodeimport: MatchesFolder folder special-title parse failed; accepting defensively",
				"path", le.Path, "err", err)
			return AcceptImportSpec()
		}
		folderInfo = reparsed
	}

	if folderInfo == nil {
		s.log.Debug("episodeimport: no folder ParsedEpisodeInfo; skipping matches-folder check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	if fileInfo == nil {
		s.log.Debug("episodeimport: no file ParsedEpisodeInfo; skipping matches-folder check",
			"path", le.Path)
		return AcceptImportSpec()
	}

	folderEpisodes, err := s.parsing.GetEpisodes(ctx, folderInfo, le.Series, true)
	if err != nil {
		s.log.Warn("episodeimport: MatchesFolder folder GetEpisodes failed; accepting defensively",
			"path", le.Path, "err", err)
		return AcceptImportSpec()
	}
	if len(folderEpisodes) == 0 {
		s.log.Debug("episodeimport: no episode numbers in folder ParsedEpisodeInfo; skipping check",
			"path", le.Path)
		return AcceptImportSpec()
	}
	fileEpisodes, err := s.parsing.GetEpisodes(ctx, fileInfo, le.Series, true)
	if err != nil {
		s.log.Warn("episodeimport: MatchesFolder file GetEpisodes failed; accepting defensively",
			"path", le.Path, "err", err)
		return AcceptImportSpec()
	}

	folderIDs := make(map[int]struct{}, len(folderEpisodes))
	for _, e := range folderEpisodes {
		folderIDs[e.ID] = struct{}{}
	}
	var unexpected []episodes.Episode
	for _, e := range fileEpisodes {
		if _, ok := folderIDs[e.ID]; !ok {
			unexpected = append(unexpected, e)
		}
	}
	if len(unexpected) == 0 {
		return AcceptImportSpec()
	}
	formatted := formatEpisodeShortIDs(unexpected)
	s.log.Debug("episodeimport: unexpected episode(s) in file considering the folder name",
		"path", le.Path, "unexpected", formatted)
	if len(unexpected) == 1 {
		return RejectImportSpec(ImportRejectionEpisodeUnexpected,
			"Episode %s was unexpected considering the %s folder name", formatted, folderInfo.ReleaseTitle)
	}
	return RejectImportSpec(ImportRejectionEpisodeUnexpected,
		"Episodes %s were unexpected considering the %s folder name", formatted, folderInfo.ReleaseTitle)
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
	_ Specification = (*FreeSpaceSpecification)(nil)
	_ Specification = (*MatchesGrabSpecification)(nil)
	_ Specification = (*NotUnpackingSpecification)(nil)
	_ Specification = (*UpgradeSpecification)(nil)
	_ Specification = (*AlreadyImportedSpecification)(nil)
	_ Specification = (*MatchesFolderSpecification)(nil)
)
