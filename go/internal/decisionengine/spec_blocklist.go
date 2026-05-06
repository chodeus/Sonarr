package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
)

// BlocklistSpecificationName is the canonical Spec.Name() identifier.
const BlocklistSpecificationName = "Blocklist"

// blocklistPublishedDateGrace is the symmetric window around a
// blocklist row's PublishedDate within which a candidate's PublishDate
// is considered "the same". Mirrors the C# `AddMinutes(-2)` /
// `AddMinutes(2)` at `Parser/ReleaseComparer.cs:53-54`.
const blocklistPublishedDateGrace = 2 * time.Minute

// blocklistSizeToleranceBytes is the absolute byte tolerance when
// comparing release sizes for "is this the same release?" — 2 MB.
// Mirrors the C# `2.Megabytes()` at `Parser/ReleaseComparer.cs:66`.
const blocklistSizeToleranceBytes int64 = 2 * 1024 * 1024

// BlocklistChecker is the minimal dependency BlocklistSpecification
// needs — the "is this candidate release blocklisted?" predicate.
//
// Implementations typically wrap the `blocklist` repo + a per-series
// cache; the spec is engine-agnostic. Tests inject a tiny stub.
//
// The release argument matches Subject.Release (`any`); callers must
// route through [parser.AsTorrentInfo] internally to access torrent-
// only fields (InfoHash).
type BlocklistChecker interface {
	IsBlocklisted(ctx context.Context, seriesID int, release any) (bool, error)
}

// BlocklistSpecification rejects releases that match a blocklist row
// for the same series. Permanent rejection — the user explicitly
// asked never to redownload this release.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/BlocklistSpecification.cs:1-32`.
//
// Priority is `Database` (vs the cheaper `Default`) because the
// underlying check hits the persistence layer.
type BlocklistSpecification struct {
	checker BlocklistChecker
	log     *slog.Logger
}

// NewBlocklistSpecification builds the spec. log defaults to
// slog.Default when nil. checker MUST be non-nil — there's no
// useful default for the blocklist lookup.
func NewBlocklistSpecification(checker BlocklistChecker, log *slog.Logger) (*BlocklistSpecification, error) {
	if checker == nil {
		return nil, fmt.Errorf("decisionengine: BlocklistSpecification requires a non-nil BlocklistChecker")
	}
	if log == nil {
		log = slog.Default()
	}
	return &BlocklistSpecification{checker: checker, log: log}, nil
}

// Name returns the canonical spec name.
func (*BlocklistSpecification) Name() string { return BlocklistSpecificationName }

// Kind reports Permanent — once-blocklisted stays blocklisted unless
// the user explicitly unblocks the row.
func (*BlocklistSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — the underlying check hits persistence.
func (*BlocklistSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:21-30`. Series-side errors
// are non-fatal: a checker error falls back to accept (defensive — a
// transient DB blip shouldn't fail every release at evaluation time).
func (s *BlocklistSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.Release == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	blocked, err := s.checker.IsBlocklisted(ctx, subject.Series.ID, subject.Release)
	if err != nil {
		s.log.Debug("blocklist: lookup failed; accepting (defensive)",
			"err", err, "seriesID", subject.Series.ID, "title", rel.Title)
		return Accept()
	}
	if blocked {
		s.log.Debug("blocklist: release matches blocklist; rejecting",
			"title", rel.Title, "seriesID", subject.Series.ID)
		return Reject(ReasonBlocklisted, "Release is blocklisted")
	}
	return Accept()
}

// blocklistRepoReader is the minimal Repo surface the default
// [RepoBlocklistChecker] needs. Decoupled from the concrete
// `*blocklist.Repo` so tests (which exercise the full matching logic)
// can inject in-memory rows without spinning up a DB.
type blocklistRepoReader interface {
	AllBySeriesID(ctx context.Context, seriesID int) ([]blocklist.Entry, error)
}

// RepoBlocklistChecker is the default BlocklistChecker implementation:
// it loads every Blocklist row for the candidate's seriesID and
// applies the C# `SameNzb`/`SameTorrent` matching rules. Suitable for
// production wiring against the real `blocklist.Repo`.
//
// Production callers wrap a `*blocklist.Repo` directly:
//
//	checker := decisionengine.NewRepoBlocklistChecker(blRepo)
//	spec, _ := decisionengine.NewBlocklistSpecification(checker, log)
//
// The implementation issues one SELECT per IsBlocklisted call —
// adequate for the per-release evaluation cadence; an upstream cache
// can wrap this if hot-loop pressure surfaces.
type RepoBlocklistChecker struct {
	repo blocklistRepoReader
}

// NewRepoBlocklistChecker builds a RepoBlocklistChecker.
func NewRepoBlocklistChecker(repo blocklistRepoReader) *RepoBlocklistChecker {
	return &RepoBlocklistChecker{repo: repo}
}

// IsBlocklisted mirrors `BlocklistService.Blocklisted` at
// `src/NzbDrone.Core/Blocklisting/BlocklistService.cs:38-62`.
// Behaviour:
//
//   - For Torrent releases without an InfoHash: title-match the
//     blocklist; require the row's Protocol == Torrent and the
//     SameTorrent predicate (indexer match).
//   - For Torrent releases WITH an InfoHash: still walk all rows for
//     the series; match any row whose TorrentInfoHash equals the
//     candidate (case-insensitive).
//   - For non-Torrent releases: title-match the blocklist; require
//     the row's Protocol == Usenet and the SameNzb predicate
//     (publish-date-or-(indexer+date+size) match).
//
// The C# tree uses dedicated DB filter methods
// (`BlocklistedByTorrentInfoHash`, `BlocklistedByTitle`) for the
// initial narrowing; the Go port walks the full AllBySeriesID slice
// in-process. Equivalent semantics — the spec's match correctness is
// identical, the cost differs (Go pulls all rows for one series; C#
// pulls a filtered subset). For the typical blocklist size
// (single-digit per series) the difference is negligible; if a
// pathologically large blocklist surfaces we can add the indexed
// queries to `blocklist.Repo` in a follow-up.
func (c *RepoBlocklistChecker) IsBlocklisted(ctx context.Context, seriesID int, release any) (bool, error) {
	rel := releaseFromAny(release)
	if rel == nil {
		return false, nil
	}
	rows, err := c.repo.AllBySeriesID(ctx, seriesID)
	if err != nil {
		return false, fmt.Errorf("blocklist: AllBySeriesID(%d): %w", seriesID, err)
	}

	if rel.DownloadProtocol == indexers.DownloadProtocolTorrent {
		torrent := parser.AsTorrentInfo(release)
		if torrent == nil {
			return false, nil
		}
		if strings.TrimSpace(torrent.InfoHash) != "" {
			for _, row := range rows {
				if blocklistRowProtocol(row) != indexers.DownloadProtocolTorrent {
					continue
				}
				if sameTorrent(row, torrent) {
					return true, nil
				}
			}
			return false, nil
		}
		for _, row := range rows {
			if blocklistRowProtocol(row) != indexers.DownloadProtocolTorrent {
				continue
			}
			if !strings.EqualFold(row.SourceTitle, rel.Title) {
				continue
			}
			if sameTorrent(row, torrent) {
				return true, nil
			}
		}
		return false, nil
	}

	for _, row := range rows {
		if blocklistRowProtocol(row) != indexers.DownloadProtocolUsenet {
			continue
		}
		if !strings.EqualFold(row.SourceTitle, rel.Title) {
			continue
		}
		if sameNzb(row, rel) {
			return true, nil
		}
	}
	return false, nil
}

// blocklistRowProtocol unwraps Entry.Protocol to the indexers enum.
// nil Protocol resolves to Unknown — defensive against legacy rows.
func blocklistRowProtocol(row blocklist.Entry) indexers.DownloadProtocol {
	if row.Protocol == nil {
		return indexers.DownloadProtocolUnknown
	}
	return indexers.DownloadProtocol(*row.Protocol)
}

// sameNzb mirrors C# `ReleaseComparer.SameNzb` at
// `Parser/ReleaseComparer.cs:9-24`. True iff:
//
//   - row.PublishedDate exactly equals release.PublishDate, OR
//   - the row has no Indexer (or the indexer matches inverted — see
//     C# `!HasSameIndexer`) AND the publish-date is within ±2min AND
//     the size is within ±2MB.
//
// The C# `!HasSameIndexer` arm is preserved verbatim — it's
// effectively "indexers differ but other facets agree", which is the
// "same release pulled from a different feed" case.
func sameNzb(row blocklist.Entry, release *parser.ReleaseInfo) bool {
	if row.PublishedDate != nil && row.PublishedDate.Equal(release.PublishDate) {
		return true
	}
	if !hasSameIndexer(row, release.Indexer) &&
		hasSamePublishedDate(row, release.PublishDate) &&
		hasSameSize(row, release.Size) {
		return true
	}
	return false
}

// sameTorrent mirrors C# `ReleaseComparer.SameTorrent` at `:26-34`.
// When the candidate has an InfoHash, that's the sole identity. When
// it doesn't, fall back to indexer match.
func sameTorrent(row blocklist.Entry, torrent *parser.TorrentInfo) bool {
	if strings.TrimSpace(torrent.InfoHash) != "" {
		return strings.EqualFold(row.TorrentInfoHash, torrent.InfoHash)
	}
	return hasSameIndexer(row, torrent.Indexer)
}

// hasSameIndexer mirrors C# `:36-44`. Empty-row-indexer matches anything.
func hasSameIndexer(row blocklist.Entry, indexer string) bool {
	if strings.TrimSpace(row.Indexer) == "" {
		return true
	}
	return strings.EqualFold(row.Indexer, indexer)
}

// hasSamePublishedDate mirrors C# `:46-55`. Nil PublishedDate
// matches anything; otherwise within ±2 min of the candidate.
func hasSamePublishedDate(row blocklist.Entry, publishDate time.Time) bool {
	if row.PublishedDate == nil {
		return true
	}
	low := row.PublishedDate.Add(-blocklistPublishedDateGrace)
	high := row.PublishedDate.Add(blocklistPublishedDateGrace)
	return !publishDate.Before(low) && !publishDate.After(high)
}

// hasSameSize mirrors C# `:57-67`. Zero stored size matches anything;
// otherwise within ±2 MB.
func hasSameSize(row blocklist.Entry, size int64) bool {
	stored := int64(0)
	if row.Size != nil {
		stored = *row.Size
	}
	if stored == 0 {
		return true
	}
	diff := stored - size
	if diff < 0 {
		diff = -diff
	}
	return diff <= blocklistSizeToleranceBytes
}

// releaseFromAny is a top-level alternative to [releaseFromSubject]
// that takes the raw Release value rather than walking from a
// Subject. Used by [RepoBlocklistChecker.IsBlocklisted] which is
// invoked with the release directly.
func releaseFromAny(release any) *parser.ReleaseInfo {
	switch r := release.(type) {
	case *parser.ReleaseInfo:
		return r
	case *parser.TorrentInfo:
		if r == nil {
			return nil
		}
		return &r.ReleaseInfo
	}
	return nil
}
