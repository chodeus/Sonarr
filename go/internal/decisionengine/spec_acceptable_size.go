package decisionengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// AcceptableSizeSpecificationName is the canonical Spec.Name() identifier.
// Mirrors the C# class name minus the `Specification` suffix.
const AcceptableSizeSpecificationName = "AcceptableSize"

// AcceptableSizeRuntimeFallbackMinutes is the per-episode runtime the
// spec falls back to when both the series runtime AND every episode
// runtime are zero AND the release is for the first-aired season.
// Mirrors the literal `45` at C# `AcceptableSizeSpecification.cs:58`.
const AcceptableSizeRuntimeFallbackMinutes = 45

// acceptableSizePilotGracePeriod is the window after the pilot episode's
// air-date during which a fallback runtime of 45 minutes is allowed.
// Mirrors the C# `.AddHours(24)` at `:52`.
const acceptableSizePilotGracePeriod = 24 * time.Hour

// AcceptableSizeProfileReader is the minimal config dependency
// AcceptableSizeSpecification needs to resolve the per-quality
// MinSize/MaxSize bytes-per-minute thresholds.
//
// Returns the parsed `*ProfileView` for a given `qualityProfileID`,
// `ok=false` when the profile doesn't exist, or `(nil, false, err)`
// when the lookup fails outright. Implementations typically wrap the
// `qualityprofiles` repo + cache; tests inject a small map stub.
type AcceptableSizeProfileReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// AcceptableSizeEpisodeFinder is the minimal episode-service
// dependency for the runtime-zero fallback path. Only fires when the
// series runtime AND every episode runtime in the candidate are zero.
//
// `FirstEpisodeOfSeason(ctx, seriesID, seasonNumber)` returns the
// pilot episode of the named season — used to derive the 24-hour
// grace-period anchor. Implementations typically wrap
// `episodes.Service.GetEpisodesBySeason` and pick the first row.
//
// Nil is tolerated: when the spec is constructed without an episode
// finder the runtime-zero fallback collapses to the "reject with
// UnknownRuntime reason" path. The C# tree always has an
// EpisodeService injected; the Go port makes the dep optional so
// early orchestrator wiring can ship without the full episode
// service ready.
type AcceptableSizeEpisodeFinder interface {
	FirstEpisodeOfSeason(ctx context.Context, seriesID, seasonNumber int) (episodes.Episode, bool, error)
}

// AcceptableSizeSpecification rejects releases whose size is outside
// the per-quality MinSize/MaxSize byte-rate limits scaled by the
// total episode runtime in the release. Permanent rejection — once a
// release is too small or too big for its quality bucket, retrying
// won't shrink or grow it.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/AcceptableSizeSpecification.cs:1-124`.
//
// The algorithm:
//
//  1. Subject.ParsedEpisodeInfo.Special → accept (specials skip the check).
//  2. Release.Size == 0 → accept (unknown size).
//  3. Compute total runtime: sum each episode's Runtime, falling back
//     to Series.Runtime when the episode runtime is 0. If the series
//     runtime is also 0 AND the release is for the first season AND
//     every episode in the release aired within 24 hours of the
//     pilot's air-date, default to 45 minutes per episode.
//  4. If runtime is still 0 → reject with UnknownRuntime.
//  5. Resolve the QualityItem (or grouped QualityItem) for the
//     parsed quality on the series' QualityProfile. Read MinSize /
//     MaxSize. nil MaxSize OR MaxSize == 0 → max disabled.
//  6. Reject if `Release.Size < MinSize.Megabytes() * runtime` or
//     `Release.Size > MaxSize.Megabytes() * runtime`.
type AcceptableSizeSpecification struct {
	profiles AcceptableSizeProfileReader
	finder   AcceptableSizeEpisodeFinder
	log      *slog.Logger
}

// NewAcceptableSizeSpecification builds the spec. log defaults to
// slog.Default when nil. profiles MUST be non-nil — there's no
// useful default for the quality-profile lookup. finder may be nil;
// when nil, the runtime-zero fallback collapses to "reject with
// UnknownRuntime" without attempting the pilot-episode lookup.
func NewAcceptableSizeSpecification(profiles AcceptableSizeProfileReader, finder AcceptableSizeEpisodeFinder, log *slog.Logger) (*AcceptableSizeSpecification, error) {
	if profiles == nil {
		return nil, fmt.Errorf("decisionengine: AcceptableSizeSpecification requires a non-nil AcceptableSizeProfileReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &AcceptableSizeSpecification{profiles: profiles, finder: finder, log: log}, nil
}

// Name returns the canonical spec name.
func (*AcceptableSizeSpecification) Name() string { return AcceptableSizeSpecificationName }

// Kind reports Permanent.
func (*AcceptableSizeSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Default — pure config + arithmetic, no I/O beyond
// the profile + (occasional) pilot lookup.
func (*AcceptableSizeSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// IsSatisfiedBy mirrors the C# method at `:23-122`. Returns Accept
// when the release passes; Reject(reason, msg) when it doesn't.
//
// further would obscure the spec-vs-code parity. Each branch maps to
// a numbered step in the type doc-comment.
//
//nolint:gocyclo // Mirrors the C# control-flow shape verbatim — splitting
func (s *AcceptableSizeSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	rel := releaseFromSubject(subject)
	if rel == nil || subject == nil {
		return Accept()
	}

	if subject.ParsedEpisodeInfo == nil {
		s.log.Debug("acceptableSize: no parsed info; accepting", "title", rel.Title)
		return Accept()
	}

	if subject.ParsedEpisodeInfo.Special {
		s.log.Debug("acceptableSize: special release; accepting", "title", rel.Title)
		return Accept()
	}

	if rel.Size == 0 {
		s.log.Debug("acceptableSize: release size unknown; accepting", "title", rel.Title)
		return Accept()
	}

	runtime, runtimeMsg, decision := s.computeRuntime(ctx, subject, rel.Title)
	if !decision.Accepted {
		return decision
	}
	if runtime == 0 {
		// computeRuntime should have already rejected; defensive belt-and-braces.
		return Reject(ReasonUnknownRuntime, "Runtime of all episodes is 0, unable to validate size until it is available")
	}

	if subject.Series == nil || subject.Series.QualityProfileID == nil {
		s.log.Debug("acceptableSize: series has no quality profile; accepting", "title", rel.Title)
		return Accept()
	}
	view, ok, err := s.profiles.LookupProfile(ctx, *subject.Series.QualityProfileID)
	if err != nil {
		s.log.Debug("acceptableSize: profile lookup failed", "err", err, "profileID", *subject.Series.QualityProfileID)
		return Accept()
	}
	if !ok || view == nil {
		s.log.Debug("acceptableSize: profile not found; accepting", "profileID", *subject.Series.QualityProfileID)
		return Accept()
	}

	item, ok := resolveQualityItem(view.Items, subject.ParsedEpisodeInfo.Quality.Quality.ID)
	if !ok {
		s.log.Debug("acceptableSize: quality not in profile; accepting",
			"qualityID", subject.ParsedEpisodeInfo.Quality.Quality.ID)
		return Accept()
	}

	if item.MinSize != nil {
		minBytes := int64(*item.MinSize * 1024 * 1024 * float64(runtime))
		if rel.Size < minBytes {
			msg := fmt.Sprintf("%s is smaller than minimum allowed %s (for %s)",
				formatSize(rel.Size), formatSize(minBytes), runtimeMsg)
			s.log.Debug("acceptableSize: rejecting too-small release",
				"title", rel.Title, "size", rel.Size, "min", minBytes)
			return Reject(ReasonBelowMinimumSize, msg)
		}
	}

	if item.MaxSize != nil && *item.MaxSize > 0 {
		maxBytes := int64(*item.MaxSize * 1024 * 1024 * float64(runtime))
		if rel.Size > maxBytes {
			msg := fmt.Sprintf("%s is larger than maximum allowed %s (for %s)",
				formatSize(rel.Size), formatSize(maxBytes), runtimeMsg)
			s.log.Debug("acceptableSize: rejecting too-big release",
				"title", rel.Title, "size", rel.Size, "max", maxBytes)
			return Reject(ReasonAboveMaximumSize, msg)
		}
	}

	s.log.Debug("acceptableSize: release within size bounds", "title", rel.Title)
	return Accept()
}

// computeRuntime mirrors the C# block at `:41-75`. Returns the total
// runtime in minutes, a human-readable runtime descriptor for
// rejection messages, and a SpecDecision — which is `Accept()` when
// the runtime is computable and a `Reject(ReasonUnknownRuntime, ...)`
// when it isn't.
//
// The pilot-episode fallback fires when:
//
//   - Series.Runtime is 0
//   - The release is for the first non-special season
//   - Every episode in the release either has no AirDateUtc OR
//     aired within 24 hours of the pilot's air-date
//   - An [AcceptableSizeEpisodeFinder] is wired
//
// All four conditions must hold; otherwise the fallback skips and the
// spec rejects with UnknownRuntime when the runtime is genuinely 0.
func (s *AcceptableSizeSpecification) computeRuntime(ctx context.Context, subject *Subject, title string) (int, string, SpecDecision) {
	seriesRuntime := 0
	if subject.Series != nil {
		seriesRuntime = subject.Series.Runtime
	}

	if seriesRuntime == 0 {
		seriesRuntime = s.runtimeFromPilotFallback(ctx, subject, title)
	}

	total := 0
	for _, e := range subject.Episodes {
		if e.Runtime > 0 {
			total += e.Runtime
		} else {
			total += seriesRuntime
		}
	}

	count := len(subject.Episodes)
	var msg string
	if count == 1 {
		msg = fmt.Sprintf("%dmin", total)
	} else {
		msg = fmt.Sprintf("%d episodes totalling %dmin", count, total)
	}

	if total == 0 {
		s.log.Debug("acceptableSize: runtime of all episodes is 0; rejecting", "title", title)
		return 0, msg, Reject(ReasonUnknownRuntime,
			"Runtime of all episodes is 0, unable to validate size until it is available")
	}
	return total, msg, Accept()
}

// runtimeFromPilotFallback returns 45 if the C# fallback conditions
// hold, 0 otherwise.
//
// Conditions: finder is non-nil; Series.Seasons can be parsed; the
// first non-special season number exists; the pilot episode for that
// season exists; and every episode in the release either has no
// AirDateUtc OR aired before pilot.AirDateUtc + 24h (or
// release.PublishDate + 24h when the pilot's AirDateUtc is nil).
func (s *AcceptableSizeSpecification) runtimeFromPilotFallback(ctx context.Context, subject *Subject, title string) int {
	if s.finder == nil || subject == nil || subject.Series == nil || len(subject.Episodes) == 0 {
		return 0
	}
	firstSeasonNumber, ok := firstNonSpecialSeasonNumber(subject.Series.Seasons)
	if !ok {
		return 0
	}
	pilot, ok, err := s.finder.FirstEpisodeOfSeason(ctx, subject.Series.ID, firstSeasonNumber)
	if err != nil || !ok {
		s.log.Debug("acceptableSize: pilot lookup failed", "err", err, "ok", ok, "seriesID", subject.Series.ID, "season", firstSeasonNumber)
		return 0
	}
	if subject.Episodes[0].SeasonNumber != pilot.SeasonNumber {
		return 0
	}

	var graceAnchor time.Time
	if pilot.AirDateUtc != nil {
		graceAnchor = *pilot.AirDateUtc
	} else if rel := releaseFromSubject(subject); rel != nil {
		graceAnchor = rel.PublishDate
	} else {
		return 0
	}
	graceEnd := graceAnchor.Add(acceptableSizePilotGracePeriod)

	for _, e := range subject.Episodes {
		if e.AirDateUtc == nil {
			continue
		}
		if !e.AirDateUtc.Before(graceEnd) {
			s.log.Debug("acceptableSize: episode outside 24h grace; runtime fallback skipped",
				"title", title, "epAirDate", *e.AirDateUtc, "graceEnd", graceEnd)
			return 0
		}
	}
	s.log.Debug("acceptableSize: applying 45min runtime fallback", "title", title)
	return AcceptableSizeRuntimeFallbackMinutes
}

// resolveQualityItem mirrors the C# `qualityProfile.GetIndex(...)`
// + nested-group dereference at `:78-80`. Returns the inner
// QualityItem carrying MinSize/MaxSize for the requested quality.
//
// Walks the typed Items slice ourselves so the spec doesn't depend
// on `qualityprofiles.LookupIndex` — flat lookup is straightforward
// and the group-member path is what we actually need.
func resolveQualityItem(items []qualityprofiles.QualityItem, qualityID int) (qualityprofiles.QualityItem, bool) {
	for _, item := range items {
		if !item.IsGroup() && item.Quality.ID == qualityID {
			return item, true
		}
		if item.IsGroup() {
			for _, child := range item.Items {
				if child.Quality.ID == qualityID {
					return child, true
				}
			}
		}
	}
	return qualityprofiles.QualityItem{}, false
}

// firstNonSpecialSeasonNumber decodes a Series.Seasons JSON blob and
// returns the smallest SeasonNumber > 0. Returns (0, false) when the
// blob is empty, malformed, or contains only specials (SeasonNumber
// == 0). Mirrors the C# LINQ at `:46`:
//
//	subject.Series.Seasons.Where(s => s.SeasonNumber > 0).Min(s => s.SeasonNumber)
//
// Defined here rather than in the series package because the
// decisionengine is the only consumer; lifting it into series would
// also need updating that package's existing private decodeSeasons
// helper which is intentionally narrow.
func firstNonSpecialSeasonNumber(seasonsJSON string) (int, bool) {
	if seasonsJSON == "" {
		return 0, false
	}
	var seasons []struct {
		SeasonNumber int `json:"seasonNumber"`
	}
	if err := json.Unmarshal([]byte(seasonsJSON), &seasons); err != nil {
		return 0, false
	}
	first := -1
	for _, s := range seasons {
		if s.SeasonNumber <= 0 {
			continue
		}
		if first < 0 || s.SeasonNumber < first {
			first = s.SeasonNumber
		}
	}
	if first < 0 {
		return 0, false
	}
	return first, true
}
