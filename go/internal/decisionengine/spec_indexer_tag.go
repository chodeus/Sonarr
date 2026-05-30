package decisionengine

import (
	"context"
	"fmt"
	"log/slog"
)

// IndexerTagSpecificationName is the canonical Spec.Name() identifier.
const IndexerTagSpecificationName = "IndexerTag"

// IndexerTagFinder resolves an indexer's configured tag set by id.
// Mirrors the C# `IIndexerFactory.Get(indexerId).Tags` lookup. found is
// false when no indexer with that id exists (C# `ModelNotFoundException`
// → skip the check). A non-nil err is an unexpected lookup failure.
type IndexerTagFinder interface {
	IndexerTags(ctx context.Context, indexerID int) (tags []int, found bool, err error)
}

// IndexerTagSpecification rejects a release from an indexer that carries
// tags none of which are present on the series — i.e. the user scoped
// that indexer to a tag set the series isn't in (e.g. a region- or
// language-specific tracker). Without this gate, a tagged indexer's
// releases would be grabbed for every series.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/IndexerTagSpecification.cs:1-56`.
//
// # Algorithm (C# `:24-54`)
//
//  1. No release, nil series, or IndexerId == 0 → Accept (nothing to
//     check against).
//  2. Indexer id not found → Accept (skip; the indexer was deleted).
//  3. Indexer has tags AND none intersect the series' tags → Reject
//     [ReasonNoMatchingTag].
//  4. Otherwise (indexer untagged, or a tag matches) → Accept.
//
// On the nil-vs-empty tag distinction: C# guards `Series?.Tags == null`,
// but in practice Tags is a never-null (possibly empty) set, so a
// tag-less series falls through to step 3 and IS rejected by a tagged
// indexer. The Go port mirrors that BEHAVIOUR (guard only nil Series;
// treat nil/empty Tags as "no tags" and proceed) rather than the literal
// null-check, so a tag-less series is correctly rejected by a tagged
// indexer.
//
// Permanent rejection — the indexer's + series' tag sets are stable for
// a given Subject.
type IndexerTagSpecification struct {
	indexers IndexerTagFinder
	log      *slog.Logger
}

// NewIndexerTagSpecification builds the spec. indexers MUST be non-nil;
// log defaults to [slog.Default] when nil.
func NewIndexerTagSpecification(indexers IndexerTagFinder, log *slog.Logger) (*IndexerTagSpecification, error) {
	if indexers == nil {
		return nil, fmt.Errorf("decisionengine: IndexerTagSpecification requires a non-nil IndexerTagFinder")
	}
	if log == nil {
		log = slog.Default()
	}
	return &IndexerTagSpecification{indexers: indexers, log: log}, nil
}

// Name returns the canonical spec name.
func (*IndexerTagSpecification) Name() string { return IndexerTagSpecificationName }

// Kind reports Permanent.
func (*IndexerTagSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — needs the per-indexer tag lookup.
func (*IndexerTagSpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:24-54`.
func (s *IndexerTagSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil || rel.IndexerID == 0 {
		return Accept()
	}

	indexerTags, found, err := s.indexers.IndexerTags(ctx, rel.IndexerID)
	if err != nil {
		s.log.Warn("indexerTag: tag lookup failed; accepting defensively",
			"indexerId", rel.IndexerID, "err", err)
		return Accept()
	}
	if !found {
		s.log.Debug("indexerTag: indexer not found, skipping", "indexerId", rel.IndexerID)
		return Accept()
	}
	if len(indexerTags) == 0 {
		// Untagged indexer applies to all series.
		return Accept()
	}

	seriesTags := make(map[int]struct{}, len(subject.Series.Tags))
	for _, t := range subject.Series.Tags {
		seriesTags[t] = struct{}{}
	}
	for _, t := range indexerTags {
		if _, ok := seriesTags[t]; ok {
			return Accept()
		}
	}

	s.log.Debug("indexerTag: indexer tags do not match series tags, rejecting",
		"indexerId", rel.IndexerID, "seriesId", subject.Series.ID)
	return Reject(ReasonNoMatchingTag, "Series tags do not match any of the indexer tags")
}

// Compile-time interface satisfaction.
var _ Specification = (*IndexerTagSpecification)(nil)
