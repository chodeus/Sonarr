package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/quality"
)

// queueStateFailedPending mirrors `trackeddownloads.StateFailedPending`
// — ordinal 5 per `TrackedDownload.cs:51-58`. Replicated as a const
// here rather than imported from trackeddownloads because that
// package imports decisionengine (for Subject in slice 3.69), so the
// reverse import would cycle. The ordinal is wire-stable per the
// trackeddownloads.State doc-comment so the duplication is safe; a
// follow-up that splits the State enum into a leaf package can
// remove this.
const queueStateFailedPending = 5

// QueueSpecificationName is the canonical Spec.Name() identifier.
const QueueSpecificationName = "Queue"

// QueueItem is the narrow projection of a queue row the
// [QueueSpecification] reads. The full queue.Queue shape carries 22
// fields; the spec only needs four (RemoteEpisode for the matched
// release context, Size for the custom-format calculator, and
// TrackedDownloadState for the FailedPending skip arm).
//
// Defining the narrow shape inside decisionengine avoids a
// decisionengine→queue import edge — queue already imports
// decisionengine for the Subject type, so the reverse would cycle.
// The cmd/sonarr boot-site adapter projects queue.Queue rows onto
// QueueItem on every spec evaluation.
type QueueItem struct {
	// RemoteEpisode is the parsed Subject that produced the queued
	// grab. nil when the producer didn't attach one — those rows
	// are skipped by the matcher (the C# tree's `?.` short-circuit
	// at `:39`).
	RemoteEpisode *Subject
	// Size is the queued release's reported byte size (the
	// download client's TotalSize, not the on-disk file size).
	// Used as the second arg to the per-queue-item custom-format
	// re-parse.
	Size int64
	// TrackedDownloadState mirrors the per-row lifecycle stage as
	// the underlying int ordinal of `trackeddownloads.State`. The
	// spec skips rows whose state is FailedPending (ordinal 5) per
	// C# `:53-56` to avoid a race with a parallel re-search.
	// `*int` mirrors the C# nullable shape — the cmd/sonarr adapter
	// converts trackeddownloads.State → int via int(state) when
	// projecting queue rows onto QueueItem.
	TrackedDownloadState *int
}

// QueueReader narrows the queue-fetch dep for [QueueSpecification].
// Single-method interface so the production binding can wrap a
// `*queue.Service` without exposing the broader read/write surface
// (Find / SetQueue / Remove) the spec doesn't touch.
//
// Returns a snapshot; the caller iterates without holding any lock
// the underlying Service might use.
type QueueReader interface {
	GetQueue() []QueueItem
}

// QueueProfileReader narrows the profile-resolver dep — same shape as
// the slice-3.65 / 3.AO / 3.AR / 3.AS siblings. Defined separately so
// the compile-time interface satisfaction stays scoped to one spec.
type QueueProfileReader interface {
	LookupProfile(ctx context.Context, qualityProfileID int) (*ProfileView, bool, error)
}

// QueueUpgradableChecker narrows three [UpgradableSpecification]
// methods — CutoffNotMet, IsUpgradable, IsRevisionUpgrade. Bundled
// because the QueueSpec walks all three branches per matched queue
// row (CutoffNotMet first, then IsUpgradable for the upgrade-reject
// arms, then IsRevisionUpgrade for the propers branch).
type QueueUpgradableChecker interface {
	CutoffNotMet(view *ProfileView, currentQuality quality.Model, currentCustomFormats []customformats.CustomFormat, newQuality *quality.Model) bool
	IsUpgradable(
		view *ProfileView,
		currentQuality quality.Model,
		currentCustomFormats []customformats.CustomFormat,
		newQuality *quality.Model,
		newCustomFormats []customformats.CustomFormat,
	) UpgradeableRejectReason
	IsRevisionUpgrade(currentQuality, newQuality quality.Model) bool
}

// QueuePropersConfigReader narrows the DownloadPropersAndRepacks
// config getter. Same posture as the slice-3.AS PropersAndRepacksConfigReader
// (already defined in upgradable.go); the QueueSpec uses the same
// reader but declares its own narrow interface for clarity.
type QueuePropersConfigReader interface {
	DownloadPropersAndRepacks() quality.ProperDownloadType
}

// QueueSpecification rejects candidates whose matched episodes are
// already queued at a higher quality / revision / custom-format
// score. Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/QueueSpecification.cs:12-114`.
//
// # Algorithm (mirrors C# `:36-112`)
//
//  1. Read the live queue snapshot via [QueueReader.GetQueue].
//  2. Filter to rows whose RemoteEpisode.Series.ID matches the
//     candidate Subject's Series.ID AND whose episode-id set
//     intersects the candidate's.
//  3. Resolve the candidate's QualityProfile via the narrow reader.
//  4. For each matching queue row:
//     - Skip if TrackedDownloadState == FailedPending (race-avoidance
//     per C# `:53-56`).
//     - Reject ReasonQueueCutoffMet when CutoffNotMet returns false
//     (the queued grab already meets cutoff).
//     - Reject one of seven typed reasons per IsUpgradable's
//     UpgradeableRejectReason value (BetterQuality →
//     QueueHigherPreference, BetterRevision → QueueHigherRevision,
//     QualityCutoff → QueueCutoffMet, CustomFormatCutoff →
//     QueueCustomFormatCutoffMet, CustomFormatScore →
//     QueueCustomFormatScore, MinCustomFormatScore →
//     QueueCustomFormatScoreIncrement, UpgradesNotAllowed →
//     QueueUpgradesNotAllowed).
//     - Reject ReasonQueuePropersDisabled when the candidate is a
//     revision-upgrade AND the user disabled propers
//     (DownloadPropersAndRepacks == DoNotUpgrade).
//  5. Accept when no matching row triggers a reject.
//
// # CustomFormat divergence
//
// C# `_formatService.ParseCustomFormat(remoteEpisode, queueItem.Size)`
// re-parses the queued item's custom formats against its reported
// Size. The Go-port reads `queueItem.RemoteEpisode.CustomFormats`
// directly — the augmenter pipeline pre-populates this on the
// Subject at queue-row materialisation time (slice 3.BE
// MapQueueItem). Documented divergence; a follow-up slice that
// ships a Subject-driven ParseCustomFormat overload can swap to the
// re-parse arm if a regression hits.
//
// # Defensive arms
//
// Permanent rejection kind. Default priority. nil-Subject /
// nil-Series / nil-ParsedEpisodeInfo / empty Episodes / nil
// QualityProfileID / profile-resolver error / profile-not-found
// → accept (defensive — the matcher loop uses these and a missing
// link means nothing to compare against). Empty queue → accept.
type QueueSpecification struct {
	queue      QueueReader
	profiles   QueueProfileReader
	upgradable QueueUpgradableChecker
	propers    QueuePropersConfigReader
	log        *slog.Logger
}

// NewQueueSpecification wires the four narrow deps. nil-required-dep
// returns a typed error; same posture as sibling spec constructors.
func NewQueueSpecification(
	queue QueueReader,
	profiles QueueProfileReader,
	upgradable QueueUpgradableChecker,
	propers QueuePropersConfigReader,
	log *slog.Logger,
) (*QueueSpecification, error) {
	if queue == nil {
		return nil, errors.New("decisionengine: QueueSpecification: queue is nil")
	}
	if profiles == nil {
		return nil, errors.New("decisionengine: QueueSpecification: profiles is nil")
	}
	if upgradable == nil {
		return nil, errors.New("decisionengine: QueueSpecification: upgradable is nil")
	}
	if propers == nil {
		return nil, errors.New("decisionengine: QueueSpecification: propers is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	return &QueueSpecification{
		queue:      queue,
		profiles:   profiles,
		upgradable: upgradable,
		propers:    propers,
		log:        log,
	}, nil
}

// Name returns the canonical spec identifier.
func (q *QueueSpecification) Name() string { return QueueSpecificationName }

// Priority returns the default tier (matches C# `:33`).
func (q *QueueSpecification) Priority() SpecPriority { return SpecPriorityDefault }

// Kind returns the permanent rejection kind (matches C# `:34`).
func (q *QueueSpecification) Kind() RejectionKind { return RejectionPermanent }

// IsSatisfiedBy walks the matching queue rows and returns the first
// reject decision, or accept when no row triggers one.
func (q *QueueSpecification) IsSatisfiedBy(ctx context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil || subject.ParsedEpisodeInfo == nil || len(subject.Episodes) == 0 {
		return Accept()
	}
	if subject.Series.QualityProfileID == 0 {
		return Accept()
	}

	queueRows := q.queue.GetQueue()
	if len(queueRows) == 0 {
		return Accept()
	}

	candidateEpisodeIDs := make(map[int]struct{}, len(subject.Episodes))
	for _, ep := range subject.Episodes {
		candidateEpisodeIDs[ep.ID] = struct{}{}
	}

	view, ok, err := q.profiles.LookupProfile(ctx, subject.Series.QualityProfileID)
	if err != nil {
		q.log.Debug("decisionengine.queue: profile lookup error — accepting defensively",
			"qualityProfileID", subject.Series.QualityProfileID, "err", err)
		return Accept()
	}
	if !ok || view == nil {
		q.log.Debug("decisionengine.queue: profile not found — accepting defensively",
			"qualityProfileID", subject.Series.QualityProfileID)
		return Accept()
	}

	for _, item := range queueRows {
		if !queueRowMatches(item, subject, candidateEpisodeIDs) {
			continue
		}

		// FailedPending skip — race-avoidance per C# `:53-56`.
		if item.TrackedDownloadState != nil && *item.TrackedDownloadState == queueStateFailedPending {
			continue
		}

		queuedQuality := item.RemoteEpisode.ParsedEpisodeInfo.Quality
		queuedCFs := append([]customformats.CustomFormat(nil), item.RemoteEpisode.CustomFormats...)
		newQuality := subject.ParsedEpisodeInfo.Quality

		// CutoffNotMet → reject ReasonQueueCutoffMet.
		if !q.upgradable.CutoffNotMet(view, queuedQuality, queuedCFs, &newQuality) {
			return Reject(ReasonQueueCutoffMet,
				fmt.Sprintf("Release in queue already meets cutoff: %s", queuedQuality.Quality.Name))
		}

		// IsUpgradable → 7 reject arms per UpgradeableRejectReason.
		switch q.upgradable.IsUpgradable(view, queuedQuality, queuedCFs, &newQuality, subject.CustomFormats) {
		case UpgradeableBetterQuality:
			return Reject(ReasonQueueHigherPreference,
				fmt.Sprintf("Release in queue is of equal or higher preference: %s", queuedQuality.Quality.Name))
		case UpgradeableBetterRevision:
			return Reject(ReasonQueueHigherRevision,
				fmt.Sprintf("Release in queue is of equal or higher revision: %v", queuedQuality.Revision))
		case UpgradeableQualityCutoff:
			return Reject(ReasonQueueCutoffMet,
				"Release in queue meets quality cutoff")
		case UpgradeableCustomFormatCutoff:
			return Reject(ReasonQueueCustomFormatCutoffMet,
				fmt.Sprintf("Release in queue meets Custom Format cutoff: %d", view.Profile.CutoffFormatScore))
		case UpgradeableCustomFormatScore:
			return Reject(ReasonQueueCustomFormatScore,
				"Release in queue has an equal or higher Custom Format score")
		case UpgradeableMinCustomFormatScore:
			return Reject(ReasonQueueCustomFormatScoreIncrement,
				fmt.Sprintf("Release in queue has Custom Format score within Custom Format score increment: %d", view.Profile.MinUpgradeFormatScore))
		case UpgradeableUpgradesNotAllowed:
			return Reject(ReasonQueueUpgradesNotAllowed,
				fmt.Sprintf("Release in queue and Quality Profile '%s' does not allow upgrades", view.Profile.Name))
		}

		// Revision-upgrade + propers-disabled arm.
		if q.upgradable.IsRevisionUpgrade(queuedQuality, newQuality) {
			if q.propers.DownloadPropersAndRepacks() == quality.ProperDoNotUpgrade {
				return Reject(ReasonQueuePropersDisabled,
					"Proper downloading is disabled")
			}
		}
	}

	return Accept()
}

// queueRowMatches mirrors the C# Where clause at `:39-42`:
//
//	q.RemoteEpisode?.Series != null &&
//	q.RemoteEpisode.Series.Id == subject.Series.Id &&
//	q.RemoteEpisode.Episodes.Select(e => e.Id).Intersect(subject.Episodes.Select(e => e.Id)).Any()
//
// Implemented as O(n+m) via the pre-built candidate-episode-id set.
func queueRowMatches(item QueueItem, subject *Subject, candidateEpisodeIDs map[int]struct{}) bool {
	if item.RemoteEpisode == nil || item.RemoteEpisode.Series == nil {
		return false
	}
	if item.RemoteEpisode.Series.ID != subject.Series.ID {
		return false
	}
	if item.RemoteEpisode.ParsedEpisodeInfo == nil {
		return false
	}
	for _, ep := range item.RemoteEpisode.Episodes {
		if _, ok := candidateEpisodeIDs[ep.ID]; ok {
			return true
		}
	}
	return false
}

// Compile-time guard — *UpgradableSpecification's three methods
// satisfy QueueUpgradableChecker via Go's structural typing.
var _ QueueUpgradableChecker = (*UpgradableSpecification)(nil)
