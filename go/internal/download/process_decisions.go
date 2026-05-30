package download

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// ClientLister is the narrow surface ProcessDownloadDecisions
// consumes for "which download clients can grab this protocol?".
// Mirrors the slice of `IProvideDownloadClient` consumed by C#
// `DownloadService.cs:36,69-78`.
//
// Returns the enabled+available providers for the supplied protocol
// in caller-defined priority order (highest-priority first). Empty
// slice (or nil) means no client is configured for the protocol —
// the orchestrator treats every grab against that protocol as
// "pending: client unavailable" matching C# `:75-78`.
type ClientLister interface {
	ListByProtocol(ctx context.Context, protocol indexers.DownloadProtocol) ([]downloadclients.Provider, error)
}

// DecisionPrioritizer is the narrow surface
// [ProcessDownloadDecisions] consumes for the per-batch ordering
// step. Mirrors C# `IPrioritizeDownloadDecision.PrioritizeDecisions`
// at `DownloadDecisionPriorizationService.cs:8-11`. Production binds
// `*decisionengine.DecisionComparer` via Go's structural typing.
//
// Implementations MUST NOT mutate the input slice; ProcessDecisions
// expects a fresh ordered copy back so the input can be safely
// retained for diagnostics / error paths.
type DecisionPrioritizer interface {
	PrioritizeDecisions(decisions []decisionengine.Decision) []decisionengine.Decision
}

// ProcessDownloadDecisions is the per-batch grab orchestrator.
// Ports C# `ProcessDownloadDecisions` at
// `src/NzbDrone.Core/Download/ProcessDownloadDecisions.cs:14-227`,
// minus the PendingReleaseService persistence. Slice 4.BP (C-cleanup
// [C8]) wires the [DecisionPrioritizer] dependency that ports the
// C# `IPrioritizeDownloadDecision` sort.
//
// Per-decision processing (line-aligned with C# `:51-117`):
//
//  1. Skip decisions whose episodes were already grabbed by a
//     higher-priority decision in the same batch (`IsEpisodeProcessed`
//     at `:167-176`).
//  2. TemporarilyRejected decisions → Pending bucket (delayed grab).
//  3. Per-protocol short-circuit: once a Usenet client returned
//     Failed, every remaining Usenet decision goes to Pending.
//     Same for Torrent. Mirrors C# `:67-72,96-110`.
//  4. Otherwise: dispatch to the highest-priority matching Provider
//     via Provider.Download. On success → Grabbed; on
//     ReleaseUnavailable → Rejected; on transport/auth failure →
//     Pending + trip the protocol short-circuit; on any other
//     error → Skipped (logged + dropped).
type ProcessDownloadDecisions struct {
	clients     ClientLister
	prioritizer DecisionPrioritizer
	bus         *messaging.Bus
	recorder    providerstatus.Recorder
	fetcher     BodyFetcher
	pending     PendingWriter
	log         *slog.Logger
}

// NewProcessDownloadDecisions binds the dependencies. clients is
// REQUIRED; prioritizer is OPTIONAL — when nil, ProcessDecisions
// uses the input order as-is, matching the pre-slice-4.BP behavior.
// Production wiring supplies *decisionengine.DecisionComparer.
func NewProcessDownloadDecisions(clients ClientLister, prioritizer DecisionPrioritizer, log *slog.Logger) (*ProcessDownloadDecisions, error) {
	if clients == nil {
		return nil, errors.New("download: NewProcessDownloadDecisions: nil ClientLister")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProcessDownloadDecisions{clients: clients, prioritizer: prioritizer, log: log}, nil
}

// SetBus wires the *messaging.Bus the orchestrator publishes
// [decisionengine.EpisodeGrabbedEvent] on after every successful
// Provider.Download. The bus dep is OPTIONAL — when unset, successful
// grabs still complete + bucket into Grabbed, but no listeners
// (historyhandlers.HandleEpisodeGrabbed, downloadhistory.Service,
// pendingreleaseshandlers.EpisodeGrabbed, notifications.Service,
// blocklisthandlers.DownloadFailed) fire. Production wiring at
// cmd/sonarr/main.go MUST call SetBus before the first dispatch;
// unit tests may leave it nil. Slice 6.A2 — gap-fill for the
// previously-no-op stampDownloadID stamp.
//
// Returns the receiver for chainable construction.
func (p *ProcessDownloadDecisions) SetBus(bus *messaging.Bus) *ProcessDownloadDecisions {
	p.bus = bus
	return p
}

// SetRecorder wires the [providerstatus.Recorder] (production:
// *downloadclientstatus.Service) the orchestrator records every
// per-client outcome against. Slice 6.A3 — mirrors the C#
// `ProviderStatusServiceBase.RecordSuccess/RecordFailure` calls
// `DownloadService.cs` does inside its per-client try/catch arms at
// `:82-116`. Optional dep — when nil, outcomes are not recorded
// (existing tests + non-status-aware harnesses can opt out).
//
// Per-outcome semantics (mirrors C# escalation contract):
//
//   - ResultGrabbed (Provider.Download returned nil err) →
//     RecordSuccess(ctx, def.ID) decrements EscalationLevel +
//     clears DisabledTill.
//   - Provider.Download returned ErrNotImplementedYet → NO RECORD.
//     A stub provider isn't a real failure; recording would penalize
//     incomplete-port slices that left a Download method unwired.
//   - Provider.Download returned an "unavailable" error (isUnavailable
//     classifier hits) → RecordFailure(ctx, def.ID, 0) ESCALATES.
//     This is the C# DownloadClientUnavailableException arm at `:215-218`.
//   - Provider.Download returned any other error →
//     RecordConnectionFailure(ctx, def.ID) records the touch without
//     escalating. One-off transport errors shouldn't push the client
//     up the back-off ladder beyond what a retry would warrant.
//
// def.ID == 0 (test stubs / in-memory providers) skips the record
// regardless of outcome — a zero ID can't be a real DB row.
//
// Returns the receiver for chainable construction.
func (p *ProcessDownloadDecisions) SetRecorder(r providerstatus.Recorder) *ProcessDownloadDecisions {
	p.recorder = r
	return p
}

// SetBodyFetcher wires the [BodyFetcher] the orchestrator uses to
// pre-fetch the .nzb / .torrent body from the indexer that produced
// each release. Slice 6.A4 — fills the gap documented at
// `downloadclients.GrabRequest.NzbBody/TorrentBody`. Optional dep —
// when nil, GrabRequest.NzbBody + TorrentBody stay empty and
// concrete clients that require pre-fetched bytes will fail (SAB +
// NZBGet); clients that accept DownloadURL directly (qBittorrent
// via URL, Deluge URL-add) or magnet links (Transmission) keep
// working unaffected.
//
// Fetch policy (per protocol, applied at dispatch time):
//
//   - Usenet: fetch GrabRequest.DownloadURL into NzbBody when
//     DownloadURL is non-empty. NzbBody fields already populated
//     (re-entry safety) are left alone.
//   - Torrent: fetch DownloadURL into TorrentBody ONLY when
//     MagnetURL is empty. When MagnetURL is set, the client adds
//     via magnet directly; the .torrent file isn't needed.
//   - Either protocol: a Fetch error buckets the decision to
//     Pending WITHOUT tripping the per-protocol short-circuit (the
//     indexer host is the issue, not the download client).
//
// Returns the receiver for chainable construction.
func (p *ProcessDownloadDecisions) SetBodyFetcher(f BodyFetcher) *ProcessDownloadDecisions {
	p.fetcher = f
	return p
}

// SetPendingWriter wires the [PendingWriter] (production:
// *pendingreleases.Service) the orchestrator persists the per-batch
// pending-release queue to. Ports the C# `IPendingReleaseService`
// dependency `ProcessDownloadDecisions` uses to call `AddMany` at
// `ProcessDownloadDecisions.cs:119-121`. Optional dep — when nil,
// ProcessDecisions still buckets decisions into ProcessedDecisions.Pending
// (the in-memory result) but does NOT persist them to the
// PendingReleases table; the table-backed retry / queue-display /
// remove-on-grab paths stay inert. Production wiring at
// cmd/sonarr/main.go MUST call SetPendingWriter so delayed +
// download-client-unavailable releases survive a restart and surface in
// the queue. Unit tests may leave it nil.
//
// Returns the receiver for chainable construction.
func (p *ProcessDownloadDecisions) SetPendingWriter(w PendingWriter) *ProcessDownloadDecisions {
	p.pending = w
	return p
}

// ProcessDecisions iterates approved + temporarily-rejected
// decisions in priority order (per the wired [DecisionPrioritizer];
// input order if none), classifying each into Grabbed/Pending/
// Rejected. Decisions whose Rejections list contains at least one
// Permanent rejection drop into Rejected without a grab attempt.
//
// The C# version returns Task<ProcessedDecisions>; the Go port
// returns the value directly (synchronous; concrete clients are
// expected to honour ctx cancellation themselves).
func (p *ProcessDownloadDecisions) ProcessDecisions(ctx context.Context, decisions []decisionengine.Decision) *ProcessedDecisions {
	out := NewProcessedDecisions()
	var (
		usenetFailed  bool
		torrentFailed bool
	)
	// pendingAdd accumulates the persistence batch for the wired
	// PendingWriter — flushed once after the loop via persistPending.
	// Mirrors the C# `pendingAddQueue` at `ProcessDownloadDecisions.cs:46`.
	var pendingAdd []pendingreleases.AddItem

	// Slice 4.BP (C-cleanup [C8]): apply the prioritization sort
	// before iteration so higher-ranked candidates for the same
	// series are tried first — the dedup gate at bucket 3 then
	// drops the lower-ranked siblings instead of arbitrarily
	// keeping the input-order first one. Mirrors C# `ProcessDecisions`
	// at `:38-39` which feeds prioritized decisions into the loop.
	if p.prioritizer != nil {
		decisions = p.prioritizer.PrioritizeDecisions(decisions)
	}

	for i := range decisions {
		decision := decisions[i]

		// Bucket 1: permanently rejected decisions go straight to
		// Rejected. Mirrors C# `:44` `rejected = decisions.Where(d => d.Rejected).ToList()`.
		if decision.Rejected() {
			out.Rejected = append(out.Rejected, decision)
			continue
		}

		// Bucket 2: qualification gate. Both Approved AND
		// TemporarilyRejected pass through, but the candidate must
		// have at least one resolved Episode (C# `:161-165`).
		if !p.isQualified(&decision) {
			out.Rejected = append(out.Rejected, decision)
			continue
		}

		// Bucket 3: dedup against earlier-grabbed episodes (C# `:55-58`).
		if p.episodeProcessed(out.Grabbed, &decision) {
			continue
		}

		// Bucket 4: TemporarilyRejected → Pending (delayed). C# `:61-65`.
		// Persisted with reason Delay (the DelayProfile-driven wait).
		if decision.TemporarilyRejected() {
			p.preparePending(&pendingAdd, out, decision, pendingreleases.ReasonDelay)
			continue
		}

		// Bucket 5: per-protocol short-circuit. C# `:67-72`.
		// Persisted with reason DownloadClientUnavailable.
		protocol, _ := p.protocolOf(&decision)
		if (protocol == indexers.DownloadProtocolUsenet && usenetFailed) ||
			(protocol == indexers.DownloadProtocolTorrent && torrentFailed) {
			p.preparePending(&pendingAdd, out, decision, pendingreleases.ReasonDownloadClientUnavailable)
			continue
		}

		// Bucket 6: dispatch.
		result := p.dispatch(ctx, &decision)
		switch result {
		case ResultGrabbed:
			out.Grabbed = append(out.Grabbed, decision)
		case ResultPending:
			// ResultPending only arises from a body pre-fetch failure
			// (the indexer host is transiently unreachable). C# has no
			// pre-fetch step; its `case Pending` maps to Delay (`:84-88`),
			// but we tag DownloadClientUnavailable so the
			// CleanupDownloadClientUnavailablePendingReleases housekeeper
			// can purge a stale row (it preserves Delay rows forever).
			p.preparePending(&pendingAdd, out, decision, pendingreleases.ReasonDownloadClientUnavailable)
		case ResultRejected:
			out.Rejected = append(out.Rejected, decision)
		case ResultFailed:
			// C# `:96-110`: reason DownloadClientUnavailable + trip the
			// per-protocol short-circuit.
			p.preparePending(&pendingAdd, out, decision, pendingreleases.ReasonDownloadClientUnavailable)
			switch protocol {
			case indexers.DownloadProtocolUsenet:
				usenetFailed = true
			case indexers.DownloadProtocolTorrent:
				torrentFailed = true
			}
		case ResultSkipped:
			// drop silently — already logged at dispatch site.
		}
	}

	// C# `:119-122`: flush the accumulated pending-release batch.
	p.persistPending(ctx, pendingAdd)

	return out
}

// dispatch runs one Decision through the available providers for
// its protocol, walking the prioritized client list and returning
// on the first successful grab. Mirrors C# `DownloadService.DownloadReport`
// at `DownloadService.cs:55-119` — the prioritized-clients failover
// loop that tries each client, tracks already-tried Definition.Id
// values (the C# `triedClients` HashSet), and finally throws
// `DownloadClientUnavailableException` when every client errored.
//
// Per-client error semantics (slice 4.BR / C-cleanup [C9]):
//
//   - nil error → ResultGrabbed; stamp DownloadID; return immediately.
//   - [downloadclients.ErrNotImplementedYet] → log + try next. A stub
//     client among other working clients should not stop the loop.
//     If it's the only client, the loop exhausts → ResultFailed.
//   - [isUnavailable](err) (transport/auth failure) → log + try next.
//     This is the C# `DownloadClientException` catch arm at `:100-104`.
//   - any other error → log + try next. The C# `:105-115` arm
//     rethrows `ReleaseDownloadException` (permanent failure) before
//     adding to the tried set; the Go port doesn't have a typed
//     equivalent at the orchestrator layer yet (concrete clients
//     surface release-rejection variants but they're not unified),
//     so we widen to "try next" — refine when typed errors land.
//
// After the loop exhausts: ResultFailed (the C#
// `DownloadClientUnavailableException` outcome — the per-protocol
// short-circuit at the caller will then mark subsequent decisions
// of the same protocol as Pending without trying).
//
// Tried-client dedup: when [Provider.Definition] returns a non-nil
// row with a non-zero ID, repeated entries with that ID are skipped.
// Matches the C# `triedClients` HashSet at `:80,84,103,114`.
// Providers with a nil Definition (test stubs, future in-memory
// providers) always attempt — the dedup is defensive only.
func (p *ProcessDownloadDecisions) dispatch(ctx context.Context, decision *decisionengine.Decision) ProcessedDecisionResult {
	protocol, release := p.protocolOf(decision)
	if release == nil {
		p.log.Debug("download: skipping decision with no resolvable Release shape",
			"subject", subjectTitle(decision))
		return ResultSkipped
	}

	clients, err := p.clients.ListByProtocol(ctx, protocol)
	if err != nil {
		p.log.Warn("download: ListByProtocol failed", "err", err, "protocol", protocol)
		return ResultFailed
	}
	if len(clients) == 0 {
		p.log.Info("download: no client available for protocol", "protocol", protocol,
			"title", release.Title)
		return ResultFailed
	}

	req := buildGrabRequest(decision, release)

	// Slice 6.A4: pre-fetch the .nzb / .torrent body from the
	// indexer that produced the release before the per-client loop.
	// Failure buckets to Pending (not Failed) so we don't trip the
	// per-protocol short-circuit — the indexer is the issue, not
	// the configured download client.
	if res, abort := p.prefetchBody(ctx, req, protocol); abort {
		return res
	}

	tried := make(map[int]struct{}, len(clients))
	var lastErr error
	var lastClient string

	for _, provider := range clients {
		if def := provider.Definition(); def != nil && def.ID != 0 {
			if _, dup := tried[def.ID]; dup {
				continue
			}
			tried[def.ID] = struct{}{}
		}

		p.log.Debug("download: dispatching grab",
			"title", release.Title, "indexer", release.Indexer, "client", provider.Name())

		id, err := provider.Download(ctx, req)
		if err == nil {
			// Stamp the DownloadID onto the Subject so downstream
			// (history + tracked-downloads) can pick it up. The
			// in-flight Subject pointer is shared with the Grabbed
			// list entry the caller iterates.
			p.stampDownloadID(ctx, decision, id, provider)
			p.recordOutcome(ctx, provider, nil)
			return ResultGrabbed
		}

		lastErr = err
		lastClient = provider.Name()

		switch {
		case errors.Is(err, downloadclients.ErrNotImplementedYet):
			p.log.Warn("download: provider Download not implemented yet, trying next client",
				"client", provider.Name(), "err", err)
		case isUnavailable(err):
			p.log.Warn("download: client unavailable, trying next client",
				"client", provider.Name(), "err", err)
		default:
			p.log.Warn("download: dispatch failed on client, trying next client",
				"client", provider.Name(), "title", release.Title, "err", err)
		}
		p.recordOutcome(ctx, provider, err)
	}

	p.log.Warn("download: all clients failed for protocol",
		"protocol", protocol, "title", release.Title,
		"lastClient", lastClient, "lastErr", lastErr)
	return ResultFailed
}

// isQualified ports C# `IsQualifiedReport` at `:161-165` —
// Approved||TemporarilyRejected + at least one resolved Episode.
func (p *ProcessDownloadDecisions) isQualified(decision *decisionengine.Decision) bool {
	if !decision.Approved() && !decision.TemporarilyRejected() {
		return false
	}
	sub, ok := decision.Subject.(*decisionengine.Subject)
	if !ok || sub == nil {
		return false
	}
	return len(sub.Episodes) > 0
}

// episodeProcessed reports whether any episode in candidate has
// already been grabbed by an earlier decision in this batch.
// Mirrors C# `IsEpisodeProcessed` at `:167-176`.
func (p *ProcessDownloadDecisions) episodeProcessed(grabbed []decisionengine.Decision, candidate *decisionengine.Decision) bool {
	sub, ok := candidate.Subject.(*decisionengine.Subject)
	if !ok || sub == nil {
		return false
	}
	candidateIDs := episodeIDSet(sub.Episodes)
	if len(candidateIDs) == 0 {
		return false
	}
	for i := range grabbed {
		gSub, ok := grabbed[i].Subject.(*decisionengine.Subject)
		if !ok || gSub == nil {
			continue
		}
		for _, ep := range gSub.Episodes {
			if _, hit := candidateIDs[ep.ID]; hit {
				return true
			}
		}
	}
	return false
}

// protocolOf resolves the decision's release protocol + the
// underlying ReleaseInfo. Returns (Unknown, nil) when Subject is
// malformed.
func (p *ProcessDownloadDecisions) protocolOf(decision *decisionengine.Decision) (indexers.DownloadProtocol, *parser.ReleaseInfo) {
	sub, ok := decision.Subject.(*decisionengine.Subject)
	if !ok || sub == nil {
		return indexers.DownloadProtocolUnknown, nil
	}
	switch r := sub.Release.(type) {
	case *parser.ReleaseInfo:
		return r.DownloadProtocol, r
	case *parser.TorrentInfo:
		return r.DownloadProtocol, &r.ReleaseInfo
	}
	return indexers.DownloadProtocolUnknown, nil
}

// stampDownloadID flips Subject.DownloadAllowed and publishes a
// [decisionengine.EpisodeGrabbedEvent] on the wired bus so the
// historyhandlers / downloadhistory / pendingreleaseshandlers /
// notifications / blocklisthandlers listeners can persist + fan
// out. Slice 6.A2 — fills in the previously-no-op stamp.
//
// The Subject itself doesn't carry a DownloadID field (matching the
// C# RemoteEpisode shape — the ID lives on the queue Item); the
// event carries it. Subject.DownloadAllowed is set so downstream
// code that re-encounters this Subject (the Grabbed bucket the
// caller iterates) can branch on "was a grab dispatched against
// this candidate".
//
// Bus-nil path: the event isn't published; the grab still counts.
// Production wiring is REQUIRED to call SetBus before the first
// dispatch — the warn-log here surfaces misconfiguration without
// breaking the orchestrator's primary contract.
//
// Mirrors C# `DownloadService.DownloadReport` at
// `src/NzbDrone.Core/Download/DownloadService.cs:120-135` where the
// EpisodeGrabbedEvent is filled out + `_eventAggregator.PublishEvent`
// fires after the client.Download() return.
func (p *ProcessDownloadDecisions) stampDownloadID(ctx context.Context, decision *decisionengine.Decision, id string, provider downloadclients.Provider) {
	sub, ok := decision.Subject.(*decisionengine.Subject)
	if !ok || sub == nil {
		return
	}
	sub.DownloadAllowed = true

	if p.bus == nil {
		p.log.Warn("download: bus not wired; EpisodeGrabbedEvent dropped",
			"downloadID", id, "client", provider.Name())
		return
	}

	ev := decisionengine.EpisodeGrabbedEvent{
		Episode:            sub,
		DownloadID:         id,
		DownloadClientName: provider.Name(),
	}
	if def := provider.Definition(); def != nil {
		ev.DownloadClientID = def.ID
		ev.DownloadClient = def.Implementation
		// Production rows carry both Name and Implementation; the
		// user-configured display name wins for DownloadClientName
		// when the definition has one (Provider.Name may return the
		// implementation-default if the user never renamed). Matches
		// C# `DownloadService.DownloadReport` which reads
		// `downloadClient.Definition.Name` at `:123`.
		if def.Name != "" {
			ev.DownloadClientName = def.Name
		}
	}

	if err := messaging.Publish(ctx, p.bus, ev); err != nil {
		// Bus shutdown / recursion-limit. The grab itself is already
		// dispatched to the remote client — log and move on rather
		// than rolling back. Mirrors the C# bus posture where
		// PublishEvent failures don't unwind the grab.
		p.log.Warn("download: EpisodeGrabbedEvent publish failed",
			"err", err, "downloadID", id, "client", provider.Name())
	}
}

// buildGrabRequest extracts the GrabRequest fields from
// Subject.Release. The orchestrator at queue [15] sub-arc territory
// will additionally fetch the NZB/Torrent body via the indexer's
// download-request builder; for slice 4.Z, the Provider.Download
// receives DownloadURL/MagnetURL/InfoHash only — concrete clients
// that need pre-fetched bytes return ErrNotImplementedYet (or fetch
// inline as a follow-up).
func buildGrabRequest(decision *decisionengine.Decision, release *parser.ReleaseInfo) *downloadclients.GrabRequest {
	req := &downloadclients.GrabRequest{
		ReleaseTitle: release.Title,
		DownloadURL:  release.DownloadURL,
		IndexerID:    release.IndexerID,
		IndexerName:  release.Indexer,
	}
	if sub, ok := decision.Subject.(*decisionengine.Subject); ok && sub != nil {
		req.SeedConfiguration = sub.SeedConfiguration
		req.Subject = sub
		if sub.Series != nil {
			// SeriesTitle threads through for qBittorrent's
			// AddSeriesTags arm (slice 6.I). Concrete clients are
			// free to ignore the field; only qBittorrent currently
			// reads it.
			req.SeriesTitle = sub.Series.Title
		}
		// IsRecent reads the matched episodes via the Subject's
		// IsRecentEpisode helper — but that helper takes a `time.Time`
		// for the clock-pin. The orchestrator hasn't a clock dep at
		// this site; the queue [15] sub-arc that wires the bus +
		// scheduler will substitute a real clock. For now, treat as
		// false (Older-tier priority). Documented behaviour delta.
	}
	if t := parser.AsTorrentInfo(decisionRelease(decision)); t != nil {
		req.MagnetURL = t.MagnetURL
		req.InfoHash = t.InfoHash
	}
	return req
}

// decisionRelease pulls the raw Subject.Release back out so
// buildGrabRequest can type-assert for the torrent-specific fields.
// Hoisted out to keep buildGrabRequest readable.
func decisionRelease(decision *decisionengine.Decision) any {
	if sub, ok := decision.Subject.(*decisionengine.Subject); ok && sub != nil {
		return sub.Release
	}
	return nil
}

// subjectTitle is a tolerant title-extractor for log lines.
func subjectTitle(decision *decisionengine.Decision) string {
	if sub, ok := decision.Subject.(*decisionengine.Subject); ok && sub != nil {
		return sub.String()
	}
	return "<unknown>"
}

// episodeIDSet collects episode IDs into a set for the dedup pass.
func episodeIDSet(eps []episodes.Episode) map[int]struct{} {
	out := make(map[int]struct{}, len(eps))
	for _, e := range eps {
		out[e.ID] = struct{}{}
	}
	return out
}

// prefetchBody runs the indexer-side body fetch for the GrabRequest
// + populates NzbBody (usenet) or TorrentBody (torrent without
// magnet) in place. Slice 6.A4. Returns (result, abort) — when
// abort is true, dispatch returns the result immediately (Pending
// without tripping the short-circuit); when abort is false, the
// per-client dispatch loop proceeds. Skip cases that don't abort:
//
//   - No fetcher wired (production not yet configured for this
//     path). Skips silently to preserve the existing test surface.
//   - Empty req.DownloadURL — no fetch target. SAB/NZBGet will
//     immediately fail without bytes; that's expected.
//   - Torrent with non-empty MagnetURL — magnet adds bypass the
//     .torrent file fetch.
//   - Body already populated (re-entry safety; the orchestrator
//     never re-enters today but the path is cheap).
func (p *ProcessDownloadDecisions) prefetchBody(ctx context.Context, req *downloadclients.GrabRequest, protocol indexers.DownloadProtocol) (ProcessedDecisionResult, bool) {
	if p.fetcher == nil || req == nil || req.DownloadURL == "" {
		return 0, false
	}
	switch protocol {
	case indexers.DownloadProtocolUsenet:
		if len(req.NzbBody) > 0 {
			return 0, false
		}
	case indexers.DownloadProtocolTorrent:
		if req.MagnetURL != "" || len(req.TorrentBody) > 0 {
			return 0, false
		}
	default:
		return 0, false
	}

	body, err := p.fetcher.Fetch(ctx, req.IndexerID, req.DownloadURL)
	if err != nil {
		p.log.Warn("download: body pre-fetch failed; bucketing decision to Pending",
			"title", req.ReleaseTitle, "indexer", req.IndexerName, "indexerID", req.IndexerID,
			"downloadURL", req.DownloadURL, "err", err)
		return ResultPending, true
	}

	switch protocol {
	case indexers.DownloadProtocolUsenet:
		req.NzbBody = body
	case indexers.DownloadProtocolTorrent:
		req.TorrentBody = body
	}
	p.log.Debug("download: body pre-fetched",
		"title", req.ReleaseTitle, "protocol", protocol, "bytes", len(body))
	return 0, false
}

// recordOutcome reports a per-client Download attempt to the wired
// [providerstatus.Recorder], if any. See [ProcessDownloadDecisions.SetRecorder]
// for the per-outcome semantics. No-op when no recorder is wired, when
// the provider has no Definition (test stub), or when Definition.ID
// is zero. Errors from the recorder are deliberately ignored (logged
// at debug) so a status-table write failure can't unwind a successful
// remote-client grab. Slice 6.A3.
func (p *ProcessDownloadDecisions) recordOutcome(ctx context.Context, provider downloadclients.Provider, err error) {
	if p.recorder == nil {
		return
	}
	def := provider.Definition()
	if def == nil || def.ID == 0 {
		return
	}
	if err == nil {
		if recErr := p.recorder.RecordSuccess(ctx, def.ID); recErr != nil {
			p.log.Debug("download: RecordSuccess failed", "client", provider.Name(), "err", recErr)
		}
		return
	}
	// ErrNotImplementedYet is a stub-port marker, not a real failure.
	// Don't penalize the row in the status table.
	if errors.Is(err, downloadclients.ErrNotImplementedYet) {
		return
	}
	if isUnavailable(err) {
		if recErr := p.recorder.RecordFailure(ctx, def.ID, 0); recErr != nil {
			p.log.Debug("download: RecordFailure failed", "client", provider.Name(), "err", recErr)
		}
		return
	}
	if recErr := p.recorder.RecordConnectionFailure(ctx, def.ID); recErr != nil {
		p.log.Debug("download: RecordConnectionFailure failed", "client", provider.Name(), "err", recErr)
	}
}

// isUnavailable classifies a Provider.Download error as
// "transport/auth failure" — the conditions that should trigger the
// per-protocol short-circuit. Matches C# `DownloadClientUnavailable
// Exception` + `DownloadClientAuthenticationException` at
// `:215-218`.
//
// The Go port doesn't have a unified error sentinel for "unavailable";
// it matches against the typed sentinels concrete clients return
// (sabnzbd.ErrUnavailable / transmission.ErrUnavailable +
// transmission.ErrAuthFailed). The check is by error-message
// substring to stay decoupled from the concrete-client packages
// (avoiding an import cycle).
func isUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"unavailable", "authentication", "auth failed", "auth_failed"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// helper: format the count summary for log lines.
func (pd *ProcessedDecisions) String() string {
	return fmt.Sprintf("Grabbed=%d Pending=%d Rejected=%d",
		len(pd.Grabbed), len(pd.Pending), len(pd.Rejected))
}
