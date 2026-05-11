package decisionengine

import (
	"math"
	"sort"

	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// decisionComparerSizeBucketBytes is the size bucket Sonarr rounds to
// when comparing release sizes. Releases within ±200 MB are treated
// as the same "size class" for sort purposes — picking 1.5 GB over
// 1.6 GB is noise, but 1.5 GB over 4 GB is signal.
//
// Mirrors the C# `200.Megabytes()` constants at
// `DownloadDecisionComparer.cs:198, 202`.
const decisionComparerSizeBucketBytes int64 = 200 * 1024 * 1024

// ComparerProfileLookup is the synchronous quality-profile resolver
// the comparer calls per Decision pair. Returns the parsed
// `*ProfileView` for the supplied profileID, or nil when the lookup
// fails / no row exists.
//
// Synchronous because Go's sort.Slice doesn't pass a context. Callers
// that need an async lookup should wrap their async repo call in a
// per-Sort cache (typical pattern: pre-load every Decision's
// QualityProfileID once, then close over the resulting map).
type ComparerProfileLookup func(profileID int) *ProfileView

// ComparerDelayProfileLookup is the synchronous delay-profile
// resolver. Same caching responsibility as
// [ComparerProfileLookup]. Returns the matched profile + ok=true,
// or zero+false when no profile matches the tag set.
type ComparerDelayProfileLookup func(tagIDs []int) (delayprofiles.Profile, bool)

// DecisionComparer ports the C# `DownloadDecisionComparer` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecisionComparer.cs:1-209`.
// Implements a multi-stage comparator: each stage returns -1/0/1, and
// the first non-zero result wins (the C# `FirstOrDefault(result =>
// result != 0)` shape at `:43`).
//
// Stage order, mirroring the C# list at `:30-41`:
//
//  1. Quality (profile-relative quality index, then Revision when
//     DownloadPropersAndRepacks ≠ DoNotPrefer).
//  2. CustomFormatScore (higher score wins).
//  3. Protocol (matches DelayProfile.PreferredProtocol wins).
//  4. EpisodeCount (FullSeason first; then for non-Anime FEWER
//     episodes ranks higher [the C# CompareByReverse], for Anime
//     MORE episodes ranks higher [absolute-numbering bundles are
//     typically larger packs]).
//  5. EpisodeNumber (lower min-episode ranks higher — earlier
//     episodes preferred when sizes / qualities tie).
//  6. IndexerPriority (lower IndexerPriority NUMBER ranks higher;
//     Sonarr UI shows 1 as top priority, 50 as bottom).
//  7. Peers (torrent only — Math.Round(Math.Log10(seeders)),
//     ditto peers, both higher better).
//  8. Age (usenet only — fresh releases preferred via tiered scoring;
//     past 7d penalty grows logarithmically).
//  9. Size (per-profile PreferredSize × runtime; closer to preferred
//     wins; nil PreferredSize falls back to "larger wins").
//
// All stages are pure given the injected lookups + the Subject
// fields. The comparer is goroutine-safe; share one instance across
// concurrent Sort calls.
type DecisionComparer struct {
	cfg           ConfigReader
	profileLookup ComparerProfileLookup
	delayLookup   ComparerDelayProfileLookup
}

// NewDecisionComparer builds a DecisionComparer.
//
// cfg supplies the user's `DownloadPropersAndRepacks` preference
// (read once per Compare call). profileLookup + delayLookup may be
// nil; when nil, the corresponding stage degrades to "no signal"
// (returns 0). Production wiring supplies all three; tests can
// inject just enough to exercise the stage under test.
func NewDecisionComparer(cfg ConfigReader, profileLookup ComparerProfileLookup, delayLookup ComparerDelayProfileLookup) *DecisionComparer {
	return &DecisionComparer{cfg: cfg, profileLookup: profileLookup, delayLookup: delayLookup}
}

// Compare implements the sort.Interface-style comparator: returns
// -1 if x ranks lower than y, 0 for tie, +1 if x ranks higher.
//
// Convention: HIGHER ranks first. Use [Sort] to sort a Decision slice
// best-first; or pass `Compare` to a custom sort.Slice with the
// arguments reversed.
//
// Approved/Rejected status is NOT part of the comparison — the
// caller is expected to filter rejections out before sorting.
func (c *DecisionComparer) Compare(x, y Decision) int {
	// Stages run in C# list order; first non-zero wins.
	stages := []func(x, y Decision) int{
		c.compareQuality,
		c.compareCustomFormatScore,
		c.compareProtocol,
		c.compareEpisodeCount,
		c.compareEpisodeNumber,
		c.compareIndexerPriority,
		c.comparePeersIfTorrent,
		c.compareAgeIfUsenet,
		c.compareSize,
	}
	for _, stage := range stages {
		if r := stage(x, y); r != 0 {
			return r
		}
	}
	return 0
}

// Sort sorts decisions in-place, best-first. Equivalent to
// `sort.SliceStable(decisions, func(i,j) { return c.Compare(j, i) > 0 })`
// — callers that want descending best-first ordering use this helper.
func (c *DecisionComparer) Sort(decisions []Decision) {
	sort.SliceStable(decisions, func(i, j int) bool {
		return c.Compare(decisions[i], decisions[j]) > 0
	})
}

// === stage helpers ===

// subjectOf extracts the *Subject from a Decision, returning nil for
// type-assert mismatches. Most stages defensive-return 0 on nil.
func subjectOf(d Decision) *Subject {
	if s, ok := d.Subject.(*Subject); ok {
		return s
	}
	return nil
}

// qualityProfileOf wraps profileLookup with subject guards.
func (c *DecisionComparer) qualityProfileOf(s *Subject) *ProfileView {
	if c.profileLookup == nil || s == nil || s.Series == nil || s.Series.QualityProfileID == 0 {
		return nil
	}
	return c.profileLookup(s.Series.QualityProfileID)
}

// compareQuality stages 1: profile-relative quality index, then
// Revision when DoNotPrefer is not set. Mirrors C# `:71-81`.
func (c *DecisionComparer) compareQuality(x, y Decision) int {
	xs, ys := subjectOf(x), subjectOf(y)
	xIdx := c.qualityIndex(xs)
	yIdx := c.qualityIndex(ys)
	if r := compareInt(xIdx, yIdx); r != 0 {
		return r
	}
	mode := quality.ProperDownloadType(0)
	if c.cfg != nil {
		mode = c.cfg.DownloadPropersAndRepacks()
	}
	if mode == quality.ProperDoNotPrefer {
		return 0
	}
	xr, yr := revisionOf(xs), revisionOf(ys)
	return xr.Compare(yr)
}

// qualityIndex returns the profile-relative quality index — higher
// index means better quality. Returns 0 when the profile / quality
// is unresolvable; the comparator interprets that as "no signal".
func (c *DecisionComparer) qualityIndex(s *Subject) int {
	view := c.qualityProfileOf(s)
	if view == nil || s.ParsedEpisodeInfo == nil {
		return 0
	}
	idx, _ := qualityprofiles.LookupIndex(view.Items, s.ParsedEpisodeInfo.Quality.Quality, false)
	return idx.Index
}

// revisionOf extracts the parsed Revision; zero-value on missing.
// Calls `quality.Revision.Compare` directly which mirrors the C#
// `Revision.CompareTo` semantics (Real before Version).
func revisionOf(s *Subject) quality.Revision {
	if s == nil || s.ParsedEpisodeInfo == nil {
		return quality.Revision{}
	}
	return s.ParsedEpisodeInfo.Quality.Revision
}

// compareCustomFormatScore stages 2: higher score wins. Mirrors C# `:83-86`.
func (c *DecisionComparer) compareCustomFormatScore(x, y Decision) int {
	xs, ys := subjectOf(x), subjectOf(y)
	xv, yv := 0, 0
	if xs != nil {
		xv = xs.CustomFormatScore
	}
	if ys != nil {
		yv = ys.CustomFormatScore
	}
	return compareInt(xv, yv)
}

// compareProtocol stages 3: 1 if matches DelayProfile.PreferredProtocol,
// 0 otherwise. The C# `CompareBy` over a bool returns -1/0/1; matching
// protocol comes back as 1 vs 0 → favored. Mirrors C# `:88-98`.
func (c *DecisionComparer) compareProtocol(x, y Decision) int {
	xs, ys := subjectOf(x), subjectOf(y)
	xv, yv := c.protocolPreferred(xs), c.protocolPreferred(ys)
	return compareBool(xv, yv)
}

func (c *DecisionComparer) protocolPreferred(s *Subject) bool {
	if c.delayLookup == nil || s == nil || s.Series == nil {
		return false
	}
	rel := releaseFromSubject(s)
	if rel == nil {
		return false
	}
	profile, ok := c.delayLookup(s.Series.Tags)
	if !ok {
		return false
	}
	return int(rel.DownloadProtocol) == profile.PreferredProtocol
}

// compareEpisodeCount stages 4. Mirrors C# `:100-118`.
//
//   - FullSeason packs win over single-episode releases.
//   - Within the non-FullSeason class:
//   - Anime: MORE episodes ranks higher (the C# `CompareBy`).
//     Anime absolute-numbering bundles are typically larger packs.
//   - Non-Anime: FEWER episodes ranks higher (the C# `CompareByReverse`).
//     Once the FullSeason gate fired, individual-episode releases
//     are preferred over multi-episode bundles.
func (c *DecisionComparer) compareEpisodeCount(x, y Decision) int {
	xs, ys := subjectOf(x), subjectOf(y)
	xFull := isFullSeason(xs)
	yFull := isFullSeason(ys)
	if r := compareBool(xFull, yFull); r != 0 {
		return r
	}
	xCount, yCount := episodeCount(xs), episodeCount(ys)
	if isAnime(xs) && isAnime(ys) {
		return compareInt(xCount, yCount) // anime: more wins
	}
	return compareInt(yCount, xCount) // non-anime: fewer wins
}

// compareEpisodeNumber stages 5: lower min-episode wins (earlier
// episodes preferred when other facets tie). Mirrors C# `:120-123`.
func (c *DecisionComparer) compareEpisodeNumber(x, y Decision) int {
	xs, ys := subjectOf(x), subjectOf(y)
	xMin, yMin := minEpisodeNumber(xs), minEpisodeNumber(ys)
	return compareInt(yMin, xMin) // reverse: lower wins
}

// compareIndexerPriority stages 6: lower IndexerPriority NUMBER ranks
// higher (Sonarr UI shows 1 as top, 50 as bottom). Mirrors C#
// `CompareByReverse` at `:66-69`.
func (c *DecisionComparer) compareIndexerPriority(x, y Decision) int {
	xRel, yRel := releaseFromSubject(subjectOf(x)), releaseFromSubject(subjectOf(y))
	xv, yv := 0, 0
	if xRel != nil {
		xv = xRel.IndexerPriority
	}
	if yRel != nil {
		yv = yRel.IndexerPriority
	}
	return compareInt(yv, xv) // reverse: lower priority NUMBER wins
}

// comparePeersIfTorrent stages 7: log10(seeders) then log10(peers).
// Both must be Torrent — protocol mismatch returns 0 (the C# code
// notes the protocol stage already discriminated). Mirrors C# `:125-148`.
func (c *DecisionComparer) comparePeersIfTorrent(x, y Decision) int {
	xRel, yRel := releaseFromSubject(subjectOf(x)), releaseFromSubject(subjectOf(y))
	if xRel == nil || yRel == nil {
		return 0
	}
	if xRel.DownloadProtocol != indexers.DownloadProtocolTorrent ||
		yRel.DownloadProtocol != indexers.DownloadProtocolTorrent {
		return 0
	}
	xSeed := log10Bucket(seedersOf(subjectOf(x)))
	ySeed := log10Bucket(seedersOf(subjectOf(y)))
	if r := compareFloat(xSeed, ySeed); r != 0 {
		return r
	}
	xPeer := log10Bucket(peersOf(subjectOf(x)))
	yPeer := log10Bucket(peersOf(subjectOf(y)))
	return compareFloat(xPeer, yPeer)
}

// compareAgeIfUsenet stages 8: tiered freshness scoring. Mirrors C# `:150-180`.
//
//	ageHours < 1   → 1000  (extremely fresh)
//	ageHours <= 24 → 100
//	ageDays <= 7   → 10
//	older          → -log10(days), penalty grows with age.
//
// Both must be Usenet — protocol mismatch returns 0.
func (c *DecisionComparer) compareAgeIfUsenet(x, y Decision) int {
	xRel, yRel := releaseFromSubject(subjectOf(x)), releaseFromSubject(subjectOf(y))
	if xRel == nil || yRel == nil {
		return 0
	}
	if xRel.DownloadProtocol != indexers.DownloadProtocolUsenet ||
		yRel.DownloadProtocol != indexers.DownloadProtocolUsenet {
		return 0
	}
	return compareFloat(usenetFreshness(xRel), usenetFreshness(yRel))
}

func usenetFreshness(rel *parser.ReleaseInfo) float64 {
	ageHours := rel.AgeHours()
	age := rel.Age()
	switch {
	case ageHours < 1:
		return 1000
	case ageHours <= 24:
		return 100
	case age <= 7:
		return 10
	default:
		if age <= 0 {
			return 0
		}
		return math.Round(math.Log10(float64(age))) * -1
	}
}

// compareSize stages 9. Mirrors C# `:182-207`. Two modes:
//
//   - Profile has a PreferredSize for the parsed quality AND
//     Series.Runtime > 0 → score is `-|releaseSize - preferred*runtime|`
//     bucketed to 200MB. Closer-to-preferred wins.
//   - Otherwise → score is `releaseSize` bucketed to 200MB. Larger
//     wins (the C# fallback at `:202`).
func (c *DecisionComparer) compareSize(x, y Decision) int {
	xs, ys := subjectOf(x), subjectOf(y)
	return compareInt64(c.sizeScore(xs), c.sizeScore(ys))
}

func (c *DecisionComparer) sizeScore(s *Subject) int64 {
	rel := releaseFromSubject(s)
	if rel == nil {
		return 0
	}
	view := c.qualityProfileOf(s)
	if view != nil && s != nil && s.ParsedEpisodeInfo != nil && s.Series != nil && s.Series.Runtime > 0 {
		if item, ok := resolveQualityItem(view.Items, s.ParsedEpisodeInfo.Quality.Quality.ID); ok {
			if item.PreferredSize != nil {
				preferredEpisode := int64(*item.PreferredSize * 1024 * 1024)
				preferred := preferredEpisode * int64(s.Series.Runtime)
				delta := rel.Size - preferred
				if delta < 0 {
					delta = -delta
				}
				return -roundToBucket(delta, decisionComparerSizeBucketBytes)
			}
		}
	}
	return roundToBucket(rel.Size, decisionComparerSizeBucketBytes)
}

// === field accessors ===

func isFullSeason(s *Subject) bool {
	return s != nil && s.ParsedEpisodeInfo != nil && s.ParsedEpisodeInfo.FullSeason
}

func isAnime(s *Subject) bool {
	return s != nil && s.Series != nil && s.Series.SeriesType == series.SeriesTypeAnime
}

func episodeCount(s *Subject) int {
	if s == nil {
		return 0
	}
	return len(s.Episodes)
}

func minEpisodeNumber(s *Subject) int {
	if s == nil || len(s.Episodes) == 0 {
		return 0
	}
	min := s.Episodes[0].EpisodeNumber
	for _, e := range s.Episodes[1:] {
		if e.EpisodeNumber < min {
			min = e.EpisodeNumber
		}
	}
	return min
}

func seedersOf(s *Subject) int {
	if s == nil {
		return 0
	}
	if t := parser.AsTorrentInfo(s.Release); t != nil && t.Seeders != nil {
		return *t.Seeders
	}
	return 0
}

func peersOf(s *Subject) int {
	if s == nil {
		return 0
	}
	if t := parser.AsTorrentInfo(s.Release); t != nil && t.Peers != nil {
		return *t.Peers
	}
	return 0
}

// log10Bucket mirrors the C# `Math.Round(Math.Log10(n))` shape used
// in the peer/seeder stage. Zero or negative inputs return 0.
func log10Bucket(n int) float64 {
	if n <= 0 {
		return 0
	}
	return math.Round(math.Log10(float64(n)))
}

// roundToBucket mirrors `Int64Extensions.Round(bucket)` from the
// Sonarr Common library: divides by bucket, rounds nearest, then
// multiplies back. Mirrors the C# `:198, 202` calls.
func roundToBucket(n, bucket int64) int64 {
	if bucket == 0 {
		return n
	}
	half := bucket / 2
	if n >= 0 {
		return ((n + half) / bucket) * bucket
	}
	return -(((-n + half) / bucket) * bucket)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}
