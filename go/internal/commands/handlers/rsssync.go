// RssSync handler. Ports the IExecute<RssSyncCommand> port at
// `src/NzbDrone.Core/Indexers/RssSyncService.cs:13-66`.
//
// Slice 4.Q (queue [9]) ships the FetchRSS-only minimum: dispatch the
// indexer fleet's RSS feeds via indexersearch.Service.FetchRSS, log
// the aggregated count, publish RssSyncCompleteEvent on the bus, and
// return success. The full C# pipeline does more downstream:
//
//  1. FetchAndParseRss             ← THIS IS WHAT WE SHIP
//  2. PendingReleaseService.GetPending → concat with fresh RSS
//  3. IMakeDownloadDecision.GetRssDecision(reports) — runs each
//     ReleaseInfo through ParsingService.Map → Subject →
//     DecisionMaker.Decide pipeline
//  4. IProcessDownloadDecisions.ProcessDecisions(decisions) — sends
//     approved decisions to download clients, queues the rest as
//     pending releases
//
// Steps 2-4 are deferred. The decision-pipeline (step 3) needs a
// ParsingService.Map equivalent + customformats integration + the
// aggregation service that's not yet ported. The grab step (step 4)
// is HARD-GATED on the download-client family — that's a separate
// Phase-4 follow-up loop. Documented as `TODO(queue [9]
// follow-up)` at the call site.
//
// What this slice DOES close:
//
//   - The RssSyncCommand can be executed end-to-end via the registry
//     (the API/v3 command-push path + the scheduler tick).
//   - The orchestrator's FetchRSS surface is exercised by a real
//     consumer.
//   - RssSyncCompleteEvent fires on the bus, letting future
//     subscribers (indexerstatus.LastRssSyncReleaseInfo updater,
//     UI signalr push) start observing.
//   - DefaultScheduledTasks gains a RssSync entry (15-minute
//     interval matching the C# default at TaskManager.cs:132).

package handlers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// RssSyncName is the registry key + Commands.Name +
// ScheduledTasks.TypeName the scheduler pushes against. Mirrors the
// C# `Command.Name` short form — `RssSyncCommand` → `RssSync`.
const RssSyncName = "RssSync"

// RssSyncIntervalDefault is the 15-minute default the scheduler
// uses for RssSyncCommand. Mirrors the C# config setting
// `RssSyncInterval` default at NzbDrone.Core.Configuration —
// TaskManager.cs:132 calls GetRssSyncInterval() which clamps to 10
// minutes minimum and reads from the user-configurable
// RssSyncInterval setting. The Go port hardcodes the C# default
// here pending the config getter port (a small follow-up gated on
// the next slice that touches config getters).
const RssSyncIntervalDefault = 15

// RssFetcher is the indexersearch.Service projection the RssSync
// handler consumes. Defined as a narrow interface so the test suite
// can pass a stub without constructing the full Service.
//
// Production callers bind `*indexersearch.Service` directly; the
// FetchRSS method matches verbatim.
type RssFetcher interface {
	FetchRSS(ctx context.Context) ([]any, error)
}

// RssDecider is the decisionengine.DownloadDecisionMaker projection
// RssSync calls after FetchRSS returns. Mirrors the C# `RssSyncService`
// flow at `Indexers/RssSyncService.cs:80-95` — after the indexer fleet
// fan-out returns raw `ReleaseInfo`, the service dispatches each to
// `IMakeDownloadDecision.GetRssDecision`.
//
// Slice 4.U (queue [11] sub-arc 11c) lands the dep. Production wiring
// binds `*decisionengine.DownloadDecisionMaker` (slice 4.U) directly
// via Go's structural typing. nil decider is permitted: the handler
// logs the report count but does not run the decision pipeline —
// preserves backwards compatibility with the slice 4.Q wiring while
// the production binding lands.
type RssDecider interface {
	GetRssDecision(ctx context.Context, reports []any, pushedRelease bool) []decisionengine.Decision
}

// GrabProcessor is the narrow surface RssSync consumes for the
// batch-grab step that follows GetRssDecision. Mirrors C#
// `IProcessDownloadDecisions.ProcessDecisions` at
// `Download/IProcessDownloadDecisions.cs`. Production binds
// *download.ProcessDownloadDecisions via Go's structural typing.
// Optional: nil grabber skips the dispatch — the handler still
// publishes RssSyncCompleteEvent with the approved/rejected counts
// and zero Grabbed/Pending counts (preserves slice-4.U behaviour).
type GrabProcessor interface {
	ProcessDecisions(ctx context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions
}

// RssSyncCompleteEvent is published on the bus after Sync completes.
// Mirrors the C# `RssSyncCompleteEvent` at
// `src/NzbDrone.Core/Indexers/RssSyncCompleteEvent.cs`. Slice 4.U
// added Approved/Rejected; slice 6.A6d adds Grabbed/Pending from the
// downstream ProcessDownloadDecisions orchestrator — closes the
// missing C# `ProcessedDecisions.Grabbed` parity gap noted in the
// 4.U doc.
type RssSyncCompleteEvent struct {
	// ReportsFound is the aggregated release count returned by
	// indexersearch.Service.FetchRSS.
	ReportsFound int
	// ApprovedDecisions is the count of decisions with no rejections
	// after the spec gate. Zero when no RssDecider is wired. Note
	// this is the release-info-only signal; the orchestrator's
	// IsQualified gate (no resolvable Episodes) can subsequently push
	// some of these to Rejected — see GrabbedDecisions / PendingDecisions
	// for the post-dispatch bucketing.
	ApprovedDecisions int
	// RejectedDecisions is the count of decisions with at least one
	// rejection at the decider boundary. Zero when no RssDecider is
	// wired.
	RejectedDecisions int
	// GrabbedDecisions is the count the orchestrator successfully
	// dispatched to a download client. Zero when no GrabProcessor is
	// wired or no decisions reached the dispatch path.
	GrabbedDecisions int
	// PendingDecisions is the count the orchestrator deferred (delayed
	// by a TimeDelay spec or short-circuited by a per-protocol client
	// failure). Zero when no GrabProcessor is wired.
	PendingDecisions int
	// StartedAt is the wall-clock time the handler started its work.
	StartedAt time.Time
	// CompletedAt is the wall-clock time the handler returned.
	CompletedAt time.Time
}

// RssSync is the IExecute<RssSyncCommand> port. fetcher MUST be
// non-nil — running the command without a fetcher has no meaningful
// behaviour. decider, grabber, bus, and log are optional; nil decider
// skips the decision pipeline (compat with the slice 4.Q wiring),
// nil grabber skips the dispatch step (compat with the slice 4.U
// wiring), nil bus skips the event publish, nil log defaults to
// slog.Default.
type RssSync struct {
	fetcher RssFetcher
	decider RssDecider
	grabber GrabProcessor
	bus     *messaging.Bus
	log     *slog.Logger
}

// NewRssSync builds the handler. fetcher MUST be non-nil; decider +
// grabber are optional. Slice 4.U added the decider dep; slice 6.A6d
// added the grabber dep (the production *download.ProcessDownloadDecisions
// binding). nil grabber preserves the slice-4.U behaviour — decisions
// are produced but never dispatched.
func NewRssSync(fetcher RssFetcher, decider RssDecider, grabber GrabProcessor, bus *messaging.Bus, log *slog.Logger) (*RssSync, error) {
	if fetcher == nil {
		return nil, errors.New("handlers: NewRssSync: nil fetcher")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RssSync{fetcher: fetcher, decider: decider, grabber: grabber, bus: bus, log: log}, nil
}

// Execute runs the RSS sync. Body is ignored — RssSyncCommand carries
// no payload at HEAD. Mirrors the C# Execute method at
// `RssSyncService.cs:60-65` — the C# version delegates to a private
// async Sync(); the Go port inlines the FetchRSS call since we don't
// ship the rest of the pipeline yet.
//
// Per-indexer errors are already logged inside indexersearch.Service.
// FetchRSS via the indexerstatus.Recorder integration; this handler
// surfaces only the aggregate failure (when FetchRSS itself returns
// an error — typically an IndexerSource list failure).
func (h *RssSync) Execute(ctx context.Context, _ string) error {
	started := time.Now().UTC()
	h.log.Info("RSS sync starting")

	rs, err := h.fetcher.FetchRSS(ctx)
	if err != nil {
		h.log.Error("RSS sync failed", slog.Any("err", err))
		return err
	}

	// Slice 4.U (queue [11] sub-arc 11c): run the decision pipeline.
	// Slice 6.A6d (queue [15]): feed the resulting decisions into the
	// orchestrator so approved candidates dispatch to the configured
	// download clients.
	var approved, rejected, grabbed, pending int
	if h.decider != nil && len(rs) > 0 {
		decisions := h.decider.GetRssDecision(ctx, rs, false)
		for _, d := range decisions {
			if len(d.Rejections) == 0 {
				approved++
			} else {
				rejected++
			}
		}
		if h.grabber != nil && len(decisions) > 0 {
			processed := h.grabber.ProcessDecisions(ctx, decisions)
			if processed != nil {
				grabbed = len(processed.Grabbed)
				pending = len(processed.Pending)
			}
		}
	}

	completed := time.Now().UTC()
	h.log.Info("RSS sync completed",
		slog.Int("reportsFound", len(rs)),
		slog.Int("approved", approved),
		slog.Int("rejected", rejected),
		slog.Int("grabbed", grabbed),
		slog.Int("pending", pending),
		slog.Duration("elapsed", completed.Sub(started)),
	)

	if h.bus != nil {
		// messaging.Publish errors propagate via the bus's recursion
		// guard / per-handler-error channel; the RssSync handler
		// itself doesn't fail on a publish error since the work is
		// already done.
		if pubErr := messaging.Publish(ctx, h.bus, RssSyncCompleteEvent{
			ReportsFound:      len(rs),
			ApprovedDecisions: approved,
			RejectedDecisions: rejected,
			GrabbedDecisions:  grabbed,
			PendingDecisions:  pending,
			StartedAt:         started,
			CompletedAt:       completed,
		}); pubErr != nil {
			h.log.Warn("RSS sync: publish RssSyncCompleteEvent failed",
				slog.Any("err", pubErr))
		}
	}
	return nil
}

// rssSyncDefaultTask returns the DefaultScheduledTasks entry for
// RssSync. Hoisted into a function so the constants stay co-located
// with the rest of this file.
func rssSyncDefaultTask() *scheduledtasks.Task {
	return &scheduledtasks.Task{
		TypeName: RssSyncName,
		Interval: RssSyncIntervalDefault,
	}
}
