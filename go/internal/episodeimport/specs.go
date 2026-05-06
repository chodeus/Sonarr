package episodeimport

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
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

// Compile-time interface satisfaction guards. A signature drift on
// Specification surfaces here at the package boundary rather than at
// the orchestrator's per-spec dispatch site.
var (
	_ Specification = (*FullSeasonSpecification)(nil)
	_ Specification = (*SplitEpisodeSpecification)(nil)
	_ Specification = (*SameEpisodesImportSpecification)(nil)
	_ Specification = (*UnverifiedSceneNumberingSpecification)(nil)
	_ Specification = (*HasAudioTrackSpecification)(nil)
)
