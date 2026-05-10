package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/parser"
)

// TorrentSeedingSpecificationName is the canonical Spec.Name() identifier.
const TorrentSeedingSpecificationName = "TorrentSeeding"

// MinimumSeedersReader is the narrow projection of the indexer-factory
// surface this spec needs — given a per-indexer ID, returns the
// configured MinimumSeeders threshold and whether the indexer is a
// torrent indexer (with the torrent settings shape that carries the
// MinimumSeeders field).
//
// Mirrors the C# `IIndexerFactory.Get(id).Settings as
// ITorrentIndexerSettings` pattern at TorrentSeedingSpecification.cs:34-46
// — the Go port flattens the two-step (Get → as-cast) into one
// narrow read so the spec doesn't depend on the entire indexer
// factory surface. The wiring layer (cmd/sonarr) implements this
// against the live indexers.Repo + provider.Factory.
//
// Return contract:
//   - (n, true, nil)  → indexer is a torrent indexer with MinimumSeeders=n.
//   - (0, false, nil) → indexer is non-torrent OR was renamed/uninstalled
//     (mirrors the C# ModelNotFoundException accept-path at :36-39).
//   - (0, false, err) → transient lookup failure; spec accepts defensively.
type MinimumSeedersReader interface {
	MinimumSeeders(ctx context.Context, indexerID int) (int, bool, error)
}

// ErrTorrentIndexerNotFound is the sentinel a MinimumSeedersReader
// implementation may return to mean "indexer was renamed or
// uninstalled". The spec converts it to (0, false, nil) so the C#
// ModelNotFoundException accept-path is mirrored verbatim. Callers
// that don't surface this distinction can return (0, false, nil)
// directly.
var ErrTorrentIndexerNotFound = errors.New("decisionengine: torrent indexer not found")

// TorrentSeedingSpecification rejects torrent releases whose seeder
// count is below the per-indexer MinimumSeeders threshold. Permanent
// rejection — a release with insufficient seeders won't gain seeders
// just by waiting in the pending-releases retry loop.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/TorrentSeedingSpecification.cs:8-58`.
//
// Behaviour shape per the C# IsSatisfiedBy at :22-55:
//   - Non-torrent release → accept (the spec is a no-op for Newznab).
//   - Torrent with IndexerId=0 → accept (the C# `torrentInfo.IndexerId
//     == 0` short-circuit at :26).
//   - Lookup miss (renamed/uninstalled) → accept (the C# catch
//     ModelNotFoundException at :36-39).
//   - Non-torrent settings shape (Indexer Implementation changed under
//     us — shouldn't happen but defensive) → accept.
//   - torrentInfo.Seeders nil → accept (the C# `Seeders.HasValue` guard
//     at :48 — torznab feeds occasionally omit the seeders element).
//   - Seeders < MinimumSeeders → reject Permanent with reason.
//   - Else → accept.
type TorrentSeedingSpecification struct {
	reader MinimumSeedersReader
	log    *slog.Logger
}

// NewTorrentSeedingSpecification builds the spec. reader MUST be
// non-nil — there's no useful default for the per-indexer settings
// lookup. log defaults to slog.Default when nil.
func NewTorrentSeedingSpecification(reader MinimumSeedersReader, log *slog.Logger) (*TorrentSeedingSpecification, error) {
	if reader == nil {
		return nil, fmt.Errorf("decisionengine: TorrentSeedingSpecification requires a non-nil MinimumSeedersReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &TorrentSeedingSpecification{reader: reader, log: log}, nil
}

// Name returns the canonical spec name.
func (*TorrentSeedingSpecification) Name() string { return TorrentSeedingSpecificationName }

// Kind reports Permanent — a release whose seeder count is below the
// threshold won't gain seeders by waiting.
func (*TorrentSeedingSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — the underlying check hits a (typically
// cached) settings lookup, not disk or external IO.
func (*TorrentSeedingSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:22-55`. The Go port
// flattens the two-step `IIndexerFactory.Get(id).Settings as
// ITorrentIndexerSettings` cast into a single MinimumSeeders read.
func (s *TorrentSeedingSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Release == nil {
		return Accept()
	}
	torrent := parser.AsTorrentInfo(subject.Release)
	if torrent == nil || torrent.IndexerID == 0 {
		return Accept()
	}

	min, ok, err := s.reader.MinimumSeeders(ctx, torrent.IndexerID)
	if err != nil {
		if errors.Is(err, ErrTorrentIndexerNotFound) {
			s.log.Debug("torrentSeeding: indexer not found, skipping seeders check",
				"indexerId", torrent.IndexerID)
			return Accept()
		}
		s.log.Debug("torrentSeeding: settings lookup failed; accepting (defensive)",
			"err", err, "indexerId", torrent.IndexerID)
		return Accept()
	}
	if !ok {
		// Non-torrent indexer (Newznab) — the C# tree's
		// `torrentIndexerSettings != null` guard at :44 covers this.
		return Accept()
	}

	if torrent.Seeders == nil {
		// Mirrors the C# `Seeders.HasValue` guard at :48 — feeds that
		// omit the seeders element pass the spec; downstream consumers
		// (RemoveQueueItemsBlocklisted, etc.) don't penalize the
		// missing data.
		return Accept()
	}

	seeders := *torrent.Seeders
	if seeders < min {
		s.log.Debug("torrentSeeding: not enough seeders",
			"seeders", seeders, "minimum", min, "indexerId", torrent.IndexerID)
		return Reject(ReasonMinimumSeeders,
			fmt.Sprintf("Not enough seeders: %d. Minimum seeders: %d", seeders, min))
	}
	return Accept()
}

// Compile-time guard.
var _ Specification = (*TorrentSeedingSpecification)(nil)
