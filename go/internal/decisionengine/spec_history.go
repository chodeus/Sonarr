package decisionengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// HistorySpecificationName is the canonical Spec.Name() identifier.
const HistorySpecificationName = "History"

// historyRecentWindow is the "recently grabbed" window — a grab newer
// than this still counts even with CDH disabled. C# `DateTime.UtcNow.AddHours(-12)`
// at `HistorySpecification.cs:58`.
const historyRecentWindow = 12 * time.Hour

// HistoryProfileViewReader resolves a series' quality-profile view by
// id. `profileViewLookupAdapter` (cmd/sonarr) satisfies it — same shape
// as the other per-spec profile readers.
type HistoryProfileViewReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// HistoryFormatCalculator parses the custom formats of a history grab
// row (the C# `ICustomFormatCalculationService.ParseCustomFormat(EpisodeHistory,
// Series)`). `customFormatForHistoryAdapter` (cmd/sonarr) satisfies it
// via customformats.Service.All + customformats.ParseCustomFormatForHistory.
type HistoryFormatCalculator interface {
	ParseCustomFormat(ctx context.Context, entry *history.Entry, ser *series.Series) ([]customformats.CustomFormat, error)
}

// HistorySpecification rejects an RSS candidate when the episode's most
// recent history event is a GRABBED that already satisfies the quality
// profile — i.e. the episode was grabbed (but maybe not yet imported)
// at a quality/revision/custom-format that meets-or-beats the candidate.
// This guards the grab→import window the file-based specs (Upgrade*,
// Proper, AlreadyImported) can't yet see because no EpisodeFile exists.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/RssSync/HistorySpecification.cs:1-130`.
// Permanent rejection (C# `:38`), Database priority (C# `:37`).
//
// # Algorithm (C# `:41-128`)
//
//  1. SearchCriteria present → Accept (search bypasses the history gate).
//  2. Per episode: take the MOST RECENT history row (any type). If it
//     isn't a Grabbed → skip. (C# `MostRecentForEpisode` + `== Grabbed`.)
//  3. recent = grab newer than 12h. If !recent AND CDH enabled → skip
//     (CDH will reconcile it post-import).
//  4. Parse the grab's custom formats; compute CutoffNotMet + IsUpgradable
//     of the candidate vs the grabbed quality.
//  5. cutoff met → reject HistoryRecentCutoffMet / HistoryCdhDisabledCutoffMet.
//  6. else map the UpgradeableRejectReason → the matching History* reason.
type HistorySpecification struct {
	history    HistoryByEpisodeFinder
	upgradable *UpgradableSpecification
	profiles   HistoryProfileViewReader
	formats    HistoryFormatCalculator
	cdh        AlreadyImportedCDHReader
	now        func() time.Time
	log        *slog.Logger
}

// NewHistorySpecification builds the spec. history / upgradable /
// profiles / formats / cdh MUST be non-nil. now defaults to time.Now;
// log to slog.Default.
func NewHistorySpecification(
	hist HistoryByEpisodeFinder,
	upgradable *UpgradableSpecification,
	profiles HistoryProfileViewReader,
	formats HistoryFormatCalculator,
	cdh AlreadyImportedCDHReader,
	now func() time.Time,
	log *slog.Logger,
) (*HistorySpecification, error) {
	if hist == nil {
		return nil, fmt.Errorf("decisionengine: HistorySpecification requires a non-nil HistoryByEpisodeFinder")
	}
	if upgradable == nil {
		return nil, fmt.Errorf("decisionengine: HistorySpecification requires a non-nil UpgradableSpecification")
	}
	if profiles == nil {
		return nil, fmt.Errorf("decisionengine: HistorySpecification requires a non-nil HistoryProfileViewReader")
	}
	if formats == nil {
		return nil, fmt.Errorf("decisionengine: HistorySpecification requires a non-nil HistoryFormatCalculator")
	}
	if cdh == nil {
		return nil, fmt.Errorf("decisionengine: HistorySpecification requires a non-nil AlreadyImportedCDHReader")
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &HistorySpecification{history: hist, upgradable: upgradable, profiles: profiles, formats: formats, cdh: cdh, now: now, log: log}, nil
}

// Name returns the canonical spec name.
func (*HistorySpecification) Name() string { return HistorySpecificationName }

// Kind reports Permanent (C# `:38`).
func (*HistorySpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Database — per-episode history lookups (C# `:37`).
func (*HistorySpecification) Priority() SpecPriority { return SpecPriorityDatabase }

// IsSatisfiedBy mirrors the C# method at `:41-128`.
func (s *HistorySpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, info ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.ParsedEpisodeInfo == nil {
		return Accept()
	}
	// C# `:43-47` — search bypasses the history gate.
	if c, ok := info.SearchCriteria.(*SearchCriteria); ok && c != nil {
		s.log.Debug("history: skipping history check during search")
		return Accept()
	}

	view, ok, err := s.profiles.LookupProfile(ctx, subject.Series.QualityProfileID)
	if err != nil || !ok || view == nil {
		// Without the profile we can't run the upgrade comparison; accept
		// (defensive — same posture as the other profile-dependent specs).
		if err != nil {
			s.log.Warn("history: quality-profile lookup failed; accepting", "profileId", subject.Series.QualityProfileID, "err", err)
		}
		return Accept()
	}

	cdhEnabled := s.cdh.EnableCompletedDownloadHandling()
	candidateQuality := subject.ParsedEpisodeInfo.Quality
	cutoff := s.now().Add(-historyRecentWindow)

	for _, ep := range subject.Episodes {
		rows, err := s.history.FindByEpisodeId(ctx, ep.ID)
		if err != nil {
			s.log.Debug("history: lookup failed; accepting (defensive)", "err", err, "episodeID", ep.ID)
			return Accept()
		}
		if len(rows) == 0 {
			continue
		}
		// C# `MostRecentForEpisode` = the single most-recent row (any
		// type); rows arrive date-DESC. Only a Grabbed is interesting.
		mostRecent := rows[0]
		if mostRecent.EventType == nil || history.EventType(*mostRecent.EventType) != history.EventTypeGrabbed {
			continue
		}

		recent := mostRecent.Date.After(cutoff)
		if !recent && cdhEnabled {
			// C# `:60-63` — an old grab is reconciled by CDH post-import.
			continue
		}

		histQuality, qErr := decodeHistoryQuality(mostRecent.Quality)
		if qErr != nil {
			s.log.Debug("history: decode grabbed quality failed; skipping row", "episodeID", ep.ID, "err", qErr)
			continue
		}
		histFormats, fErr := s.formats.ParseCustomFormat(ctx, &mostRecent, subject.Series)
		if fErr != nil {
			s.log.Debug("history: parse custom formats failed; treating as none", "episodeID", ep.ID, "err", fErr)
			histFormats = nil
		}

		cutoffUnmet := s.upgradable.CutoffNotMet(view, histQuality, histFormats, &candidateQuality)
		if !cutoffUnmet {
			if recent {
				return Reject(ReasonHistoryRecentCutoffMet,
					fmt.Sprintf("Recent grab event in history already meets cutoff: %s", histQuality.Quality.Name))
			}
			return Reject(ReasonHistoryCdhDisabledCutoffMet,
				fmt.Sprintf("CDH is disabled and grab event in history already meets cutoff: %s", histQuality.Quality.Name))
		}

		subj := "Recent"
		if !recent {
			subj = "CDH is disabled and"
		}
		switch s.upgradable.IsUpgradable(view, histQuality, histFormats, &candidateQuality, subject.CustomFormats) {
		case UpgradeableNone:
			continue
		case UpgradeableBetterQuality:
			return Reject(ReasonHistoryHigherPreference,
				fmt.Sprintf("%s grab event in history is of equal or higher preference: %s", subj, histQuality.Quality.Name))
		case UpgradeableBetterRevision:
			return Reject(ReasonHistoryHigherRevision,
				fmt.Sprintf("%s grab event in history is of equal or higher revision: v%d", subj, histQuality.Revision.Version))
		case UpgradeableQualityCutoff:
			return Reject(ReasonHistoryCutoffMet,
				fmt.Sprintf("%s grab event in history meets quality cutoff", subj))
		case UpgradeableCustomFormatCutoff:
			return Reject(ReasonHistoryCustomFormatCutoffMet,
				fmt.Sprintf("%s grab event in history meets Custom Format cutoff", subj))
		case UpgradeableCustomFormatScore:
			return Reject(ReasonHistoryCustomFormatScore,
				fmt.Sprintf("%s grab event in history has an equal or higher Custom Format score", subj))
		case UpgradeableMinCustomFormatScore:
			return Reject(ReasonHistoryCustomFormatScoreIncrement,
				fmt.Sprintf("%s grab event in history is within the Custom Format score increment", subj))
		case UpgradeableUpgradesNotAllowed:
			return Reject(ReasonHistoryUpgradesNotAllowed,
				fmt.Sprintf("%s grab event in history and the Quality Profile does not allow upgrades", subj))
		}
	}

	return Accept()
}

// decodeHistoryQuality parses a history row's raw Quality JSON column
// into a [quality.Model]. Mirrors the sameQualityModel decode in
// spec_already_imported.go.
func decodeHistoryQuality(raw string) (quality.Model, error) {
	var m quality.Model
	if raw == "" {
		return m, fmt.Errorf("empty quality")
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return m, err
	}
	return m, nil
}

// Compile-time interface satisfaction.
var _ Specification = (*HistorySpecification)(nil)
