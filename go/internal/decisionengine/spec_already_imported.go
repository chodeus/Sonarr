package decisionengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
)

// AlreadyImportedSpecificationName is the canonical Spec.Name()
// identifier.
const AlreadyImportedSpecificationName = "AlreadyImported"

// HistoryByEpisodeFinder is the minimal history dependency
// [AlreadyImportedSpecification] needs — every history row for a
// given EpisodeId, in date-DESC order. Production binding:
// [*history.Service.FindByEpisodeId] (slice 3.AL) satisfies this
// interface via Go's structural typing.
type HistoryByEpisodeFinder interface {
	FindByEpisodeId(ctx context.Context, episodeID int) ([]history.Entry, error)
}

// AlreadyImportedCDHReader is the narrow config dependency that
// surfaces the user's CompletedDownloadHandling toggle. Production
// binding: [*config.Provider].EnableCompletedDownloadHandling
// satisfies this interface via Go's structural typing.
type AlreadyImportedCDHReader interface {
	EnableCompletedDownloadHandling() bool
}

// AlreadyImportedSpecification rejects re-grabs of releases whose
// previous Grabbed→Imported pair already produced a same-quality
// (or same-torrent-hash, or same-title) imported file for the
// episode.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/AlreadyImportedSpecification.cs:1-99`.
//
// # Algorithm (mirrors C# `:29-97`)
//
//  1. CDH disabled → accept (the user opted out of the cycle this
//     spec is guarding against).
//  2. Per episode in Subject.Episodes:
//     a. Episode without a file (EpisodeFileID==0/nil) → skip.
//     b. Walk history-for-episode; first row with EventType=Grabbed
//     becomes lastGrabbed. None → skip.
//     c. Walk again for the matching DownloadFolderImported row
//     whose DownloadId equals lastGrabbed.DownloadId. None → skip.
//     d. lastGrabbed.Quality == imported.Quality → skip
//     (re-import-of-same-quality is the explicit safe case).
//     e. Torrent: lastGrabbed.DownloadId equals
//     uppercase(InfoHash) → reject ReasonAlreadyImportedSameHash.
//     f. release.Title equals lastGrabbed.SourceTitle (case-insensitive)
//     → reject ReasonAlreadyImportedSameName.
//
// The "first row" walks rely on date-DESC ordering from the repo —
// `FindByEpisodeId` guarantees it. The per-episode loop short-circuits
// on the first matching reject so the spec returns at most one
// rejection per Subject.
//
// # Quality equality semantics
//
// History.Quality is the raw embedded JSON the C# tree wrote at grab
// + import time. C# `QualityModel.Equals` compares Quality + Revision
// only; the Go port walks both rows through [quality.Model] (its
// `UnmarshalJSON` strips Detection-source fields) so two rows with
// identical Quality+Revision compare equal even if their JSON byte
// shapes drifted (e.g. one row was written before a Detection-source
// schema migration).
//
// # Why this is a Permanent rejection
//
// Mirrors C# `:27` — once Sonarr has grabbed AND imported a file for
// the episode at the same quality, repeatedly grabbing different
// candidate releases that resolve to the same content is wasted work.
// The user has to either delete the imported file (which the
// EpisodeFileDeletedEvent handler clears history for) or explicitly
// blocklist the title to break the cycle.
type AlreadyImportedSpecification struct {
	cdh     AlreadyImportedCDHReader
	history HistoryByEpisodeFinder
	log     *slog.Logger
}

// NewAlreadyImportedSpecification builds the spec. log defaults to
// slog.Default when nil. Both deps must be non-nil — there's no
// useful "missing CDH" or "missing history" default that mirrors
// the C# behaviour.
func NewAlreadyImportedSpecification(cdh AlreadyImportedCDHReader, hist HistoryByEpisodeFinder, log *slog.Logger) (*AlreadyImportedSpecification, error) {
	if cdh == nil {
		return nil, fmt.Errorf("decisionengine: AlreadyImportedSpecification requires a non-nil AlreadyImportedCDHReader")
	}
	if hist == nil {
		return nil, fmt.Errorf("decisionengine: AlreadyImportedSpecification requires a non-nil HistoryByEpisodeFinder")
	}
	if log == nil {
		log = slog.Default()
	}
	return &AlreadyImportedSpecification{cdh: cdh, history: hist, log: log}, nil
}

// Name returns the canonical spec name.
func (*AlreadyImportedSpecification) Name() string { return AlreadyImportedSpecificationName }

// Kind reports Permanent — see the type-level docstring rationale.
// Mirrors C# `:27`.
func (*AlreadyImportedSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — the spec hits persistence on every
// evaluation. Mirrors C# `:26`.
func (*AlreadyImportedSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:29-97`.
//
// Repo errors propagate as accept (defensive — same as the rest of
// the database-priority specs). The C# tree has no try/catch around
// `FindByEpisodeId` (the C# IoC container's transient-error policy
// handles it at a layer above), but the Go port follows the
// surrounding pattern: a transient DB blip on one episode shouldn't
// fail the candidate.
func (s *AlreadyImportedSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if !s.cdh.EnableCompletedDownloadHandling() {
		s.log.Debug("alreadyImported: CDH disabled; accepting")
		return Accept()
	}
	if subject == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	for _, ep := range subject.Episodes {
		if !hasFile(ep) {
			s.log.Debug("alreadyImported: episode without file; skipping", "episodeID", ep.ID)
			continue
		}

		rows, err := s.history.FindByEpisodeId(ctx, ep.ID)
		if err != nil {
			s.log.Debug("alreadyImported: history lookup failed; accepting (defensive)",
				"err", err, "episodeID", ep.ID)
			return Accept()
		}

		lastGrabbed, ok := firstByEventType(rows, history.EventTypeGrabbed)
		if !ok {
			continue
		}
		imported, ok := firstImportedForGrab(rows, lastGrabbed.DownloadID)
		if !ok {
			continue
		}

		if sameQualityModel(lastGrabbed.Quality, imported.Quality) {
			continue
		}

		if rel.DownloadProtocol == indexers.DownloadProtocolTorrent {
			if torrent := parser.AsTorrentInfo(subject.Release); torrent != nil {
				hash := strings.TrimSpace(torrent.InfoHash)
				if hash != "" && strings.EqualFold(hash, lastGrabbed.DownloadID) {
					s.log.Debug("alreadyImported: same torrent hash already grabbed+imported; rejecting",
						"episodeID", ep.ID, "downloadID", lastGrabbed.DownloadID)
					return Reject(ReasonAlreadyImportedSameHash, "Has same torrent hash as a grabbed and imported release")
				}
			}
		}

		if strings.EqualFold(rel.Title, lastGrabbed.SourceTitle) {
			s.log.Debug("alreadyImported: same release name already grabbed+imported; rejecting",
				"episodeID", ep.ID, "title", rel.Title)
			return Reject(ReasonAlreadyImportedSameName, "Has same release name as a grabbed and imported release")
		}
	}
	return Accept()
}

// hasFile reports whether the episode has an attached EpisodeFile.
// Mirrors the C# `episode.HasFile` at `Tv/Episode.cs` which returns
// `EpisodeFileId > 0`. The Go shape uses *int (nullable column);
// nil OR zero both read as "no file".
func hasFile(ep episodes.Episode) bool {
	return ep.EpisodeFileID != nil && *ep.EpisodeFileID > 0
}

// firstByEventType returns the first row matching the requested
// EventType (rows arrive date-DESC, so first == most recent).
func firstByEventType(rows []history.Entry, t history.EventType) (history.Entry, bool) {
	for _, r := range rows {
		if r.EventType == nil {
			continue
		}
		if history.EventType(*r.EventType) == t {
			return r, true
		}
	}
	return history.Entry{}, false
}

// firstImportedForGrab returns the first DownloadFolderImported row
// whose DownloadID matches the supplied id. Empty downloadID never
// matches — guards against legacy rows where the grab predates the
// DownloadId column.
func firstImportedForGrab(rows []history.Entry, downloadID string) (history.Entry, bool) {
	if strings.TrimSpace(downloadID) == "" {
		return history.Entry{}, false
	}
	for _, r := range rows {
		if r.EventType == nil {
			continue
		}
		if history.EventType(*r.EventType) != history.EventTypeDownloadFolderImported {
			continue
		}
		if r.DownloadID != downloadID {
			continue
		}
		return r, true
	}
	return history.Entry{}, false
}

// sameQualityModel parses both raw History.Quality JSON strings into
// [quality.Model] and compares via Model.Equal — Quality + Revision
// only, ignoring Detection-source fields. Either parse failure
// degrades to byte equality so legacy rows with non-standard shapes
// still produce a stable verdict.
func sameQualityModel(a, b string) bool {
	if a == b {
		return true
	}
	var ma, mb quality.Model
	if err := json.Unmarshal([]byte(a), &ma); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &mb); err != nil {
		return false
	}
	return ma.Equal(mb)
}
