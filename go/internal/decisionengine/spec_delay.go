package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/quality"
)

// DelaySpecificationName is the canonical Spec.Name() identifier.
const DelaySpecificationName = "Delay"

// DelayProfileViewReader resolves a series' quality-profile view by id
// for the highest-quality bypass. `profileViewLookupAdapter`
// (cmd/sonarr) satisfies it structurally — same shape as the other
// per-spec profile readers in this package.
type DelayProfileViewReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// PendingReleaseAgeReader resolves the age (minutes since the indexer
// publish date) of the OLDEST pending release intersecting the given
// series + season + episode-number set. Mirrors the C#
// `IPendingReleaseService.OldestPendingRelease(seriesId, episodeIds)`
// read DelaySpecification makes at `DelaySpecification.cs:100`, except
// the Go port keys on (season, episode-numbers) — the pending row
// stores EpisodeNumbers, not Ids (the same divergence documented on
// pendingreleases.Service.OldestPending). The adapter in cmd/sonarr
// unmarshals the matched row's stored ReleaseInfo to recover its
// PublishDate and returns AgeMinutes, keeping this package off the
// pendingreleases import path.
//
// found=false means no pending release matched (no bypass).
type PendingReleaseAgeReader interface {
	OldestPendingAgeMinutes(ctx context.Context, seriesID, seasonNumber int, episodeNumbers []int) (ageMinutes float64, found bool, err error)
}

// DelaySpecification holds a release that's younger than the series'
// delay-profile wait, so a better-quality (or preferred-protocol)
// release has a chance to appear first. TEMPORARY rejection — the
// release becomes acceptable once its age crosses the delay (or the
// orchestrator routes it to the pending-release queue and re-evaluates
// after the wait).
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/DelaySpecification.cs:11-117`.
//
// # Algorithm (C# `:32-114`)
//
//  1. User-invoked search → Accept (`:34-38`; an explicit search ignores
//     the delay).
//  2. Compute the protocol delay from the best delay-profile for the
//     series' tags; delay == 0 → Accept (`:46-50`).
//  3. If the protocol is the profile's preferred AND propers/repacks are
//     preferred-and-upgraded: a same-quality revision upgrade over any
//     existing episode file → Accept (`:56-70`).
//  4. BypassIfHighestQuality + preferred protocol + candidate quality ≥
//     the profile's last-allowed quality → Accept (`:73-83`).
//  5. BypassIfAboveCustomFormatScore + preferred protocol + the
//     candidate's custom-format score ≥ the profile minimum → Accept
//     (`:86-96`).
//  6. The oldest pending release for these episodes has already waited
//     longer than the delay → Accept (`:98-106`).
//  7. Candidate release age < delay → Reject TEMPORARY
//     [ReasonMinimumAgeDelay] (`:108-112`).
//  8. Otherwise → Accept (`:114`).
type DelaySpecification struct {
	delayProfiles DelayProfileResolver
	profiles      DelayProfileViewReader
	upgradable    UpgradableRevisionChecker
	cfg           PropersAndRepacksConfigReader
	files         EpisodeFileResolver
	pending       PendingReleaseAgeReader
	log           *slog.Logger
}

// NewDelaySpecification builds the spec. All deps except log/pending
// are REQUIRED. pending may be nil — when nil the OldestPending
// age-bypass (step 6) is skipped (the rest of the gate still applies);
// production always wires it. log defaults to slog.Default.
func NewDelaySpecification(
	delayProfiles DelayProfileResolver,
	profiles DelayProfileViewReader,
	upgradable UpgradableRevisionChecker,
	cfg PropersAndRepacksConfigReader,
	files EpisodeFileResolver,
	pending PendingReleaseAgeReader,
	log *slog.Logger,
) (*DelaySpecification, error) {
	if delayProfiles == nil {
		return nil, fmt.Errorf("decisionengine: DelaySpecification requires a non-nil DelayProfileResolver")
	}
	if profiles == nil {
		return nil, fmt.Errorf("decisionengine: DelaySpecification requires a non-nil DelayProfileViewReader")
	}
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: DelaySpecification requires a non-nil UpgradableRevisionChecker")
	}
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: DelaySpecification requires a non-nil PropersAndRepacksConfigReader")
	}
	if files == nil {
		return nil, fmt.Errorf("decisionengine: DelaySpecification requires a non-nil EpisodeFileResolver")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DelaySpecification{
		delayProfiles: delayProfiles,
		profiles:      profiles,
		upgradable:    upgradable,
		cfg:           cfg,
		files:         files,
		pending:       pending,
		log:           log,
	}, nil
}

// Name returns the canonical spec name.
func (*DelaySpecification) Name() string { return DelaySpecificationName }

// Kind reports Temporary — a too-young release becomes acceptable once
// the delay elapses (C# `Type => RejectionType.Temporary`, `:30`).
func (*DelaySpecification) Kind() RejectionKind { return RejectionTemporary }

// Priority returns Database — performs per-episode file + pending-queue
// lookups (C# `Priority => SpecificationPriority.Database`, `:29`).
func (*DelaySpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:32-114`.
func (s *DelaySpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}

	// C# `:34-38` — user-invoked search ignores the delay.
	if c, ok := info.SearchCriteria.(*SearchCriteria); ok && c != nil && c.UserInvokedSearch {
		s.log.Debug("delay: ignoring delay for user-invoked search")
		return Accept()
	}

	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}
	protocol := rel.DownloadProtocol

	profile, found, err := s.delayProfiles.BestForTags(ctx, subject.Series.Tags)
	if err != nil {
		// Don't block a grab on a delay-profile lookup failure — the C#
		// service always resolves a profile (a built-in default exists),
		// so an error here is infrastructural; degrade open.
		s.log.Warn("delay: delay-profile lookup failed; accepting", "err", err)
		return Accept()
	}
	if !found {
		return Accept()
	}

	// C# `DelayProfile.GetProtocolDelay`: Torrent → TorrentDelay, else
	// UsenetDelay.
	delay := profile.UsenetDelay
	if protocol == indexers.DownloadProtocolTorrent {
		delay = profile.TorrentDelay
	}
	isPreferredProtocol := int(protocol) == profile.PreferredProtocol

	if delay == 0 {
		s.log.Debug("delay: profile requires no waiting period", "protocol", protocol)
		return Accept()
	}

	candidate := subject.ParsedEpisodeInfo.Quality
	preferPropers := s.cfg.DownloadPropersAndRepacks() == quality.ProperPreferAndUpgrade

	// C# `:56-70` — preferred-protocol proper/repack upgrade skips the delay.
	if isPreferredProtocol && preferPropers {
		if s.candidateIsRevisionUpgrade(ctx, subject, candidate) {
			s.log.Debug("delay: candidate is a better revision for an existing file; skipping delay")
			return Accept()
		}
	}

	// C# `:73-83` — candidate at/above the profile's highest allowed
	// quality on the preferred protocol skips the delay. The
	// quality-profile view is resolved ONLY when this bypass is enabled
	// (the default-off case avoids a per-release profile lookup). A
	// lookup miss/error skips the branch (degrade toward delaying).
	if profile.BypassIfHighestQuality && isPreferredProtocol {
		if view, vok, verr := s.profiles.LookupProfile(ctx, subject.Series.QualityProfileID); verr != nil {
			s.log.Warn("delay: quality-profile lookup failed; skipping highest-quality bypass",
				"profileId", subject.Series.QualityProfileID, "err", verr)
		} else if vok && view != nil {
			if best, ok := view.LastAllowedQuality(); ok &&
				compareQuality(view.Items, candidate.Quality, best, false) >= 0 {
				s.log.Debug("delay: candidate is highest quality in profile; skipping delay")
				return Accept()
			}
		}
	}

	// C# `:86-96` — candidate custom-format score at/above the profile
	// minimum on the preferred protocol skips the delay.
	if profile.BypassIfAboveCustomFormatScore && isPreferredProtocol {
		minimum := 0
		if profile.MinimumCustomFormatScore != nil {
			minimum = *profile.MinimumCustomFormatScore
		}
		if subject.CustomFormatScore >= minimum {
			s.log.Debug("delay: candidate custom-format score meets minimum; skipping delay",
				"score", subject.CustomFormatScore, "minimum", minimum)
			return Accept()
		}
	}

	// C# `:98-106` — a release for these episodes has already been
	// pending longer than the delay; grab the new one now.
	if s.pending != nil {
		epNums := episodeNumbersForDelay(subject)
		if len(epNums) > 0 {
			ageMin, ok, perr := s.pending.OldestPendingAgeMinutes(ctx, subject.Series.ID, subject.MappedSeasonNumber, epNums)
			if perr != nil {
				s.log.Warn("delay: OldestPending lookup failed; ignoring bypass", "err", perr)
			} else if ok && ageMin > float64(delay) {
				s.log.Debug("delay: oldest pending release older than delay; accepting",
					"pendingAgeMinutes", ageMin, "delay", delay)
				return Accept()
			}
		}
	}

	// C# `:108-112` — candidate younger than the delay → temporary reject.
	if rel.AgeMinutes() < float64(delay) {
		s.log.Debug("delay: waiting for a better release",
			"delay", delay, "protocol", protocol, "ageMinutes", rel.AgeMinutes())
		return RejectTemporary(ReasonMinimumAgeDelay, "Waiting for better quality release")
	}

	return Accept()
}

// candidateIsRevisionUpgrade reports whether the candidate is a
// same-quality revision upgrade over any existing episode file for the
// Subject (the C# proper-bypass loop at `:58-69`). Files that can't be
// resolved / decoded are skipped (defensive, like ProperSpecification).
func (s *DelaySpecification) candidateIsRevisionUpgrade(ctx context.Context, subject *Subject, candidate quality.Model) bool {
	for _, ep := range subject.Episodes {
		if ep.EpisodeFileID == nil || *ep.EpisodeFileID == 0 {
			continue
		}
		file, err := s.files.GetFile(ctx, *ep.EpisodeFileID)
		if err != nil {
			if !errors.Is(err, episodefiles.ErrNotFound) {
				s.log.Warn("delay: episode-file lookup failed; skipping",
					"episodeFileId", *ep.EpisodeFileID, "err", err)
			}
			continue
		}
		if file == nil {
			continue
		}
		fileQuality, err := decodeFileQuality(file)
		if err != nil {
			s.log.Warn("delay: decode existing-file quality; skipping",
				"episodeFileId", file.ID, "err", err)
			continue
		}
		if s.upgradable.IsRevisionUpgrade(fileQuality, candidate) {
			return true
		}
	}
	return false
}

// episodeNumbersForDelay projects the Subject's matched episodes into
// the EpisodeNumber list the OldestPending lookup keys on.
func episodeNumbersForDelay(subject *Subject) []int {
	out := make([]int, 0, len(subject.Episodes))
	for _, e := range subject.Episodes {
		out = append(out, e.EpisodeNumber)
	}
	return out
}

// Compile-time interface satisfaction.
var _ Specification = (*DelaySpecification)(nil)
