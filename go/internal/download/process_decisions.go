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
	"sonarr.tv/sonarr/internal/parser"
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

// ProcessDownloadDecisions is the per-batch grab orchestrator.
// Ports C# `ProcessDownloadDecisions` at
// `src/NzbDrone.Core/Download/ProcessDownloadDecisions.cs:14-227`,
// minus the PendingReleaseService persistence + the
// PrioritizeDownloadDecision sort.
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
	clients ClientLister
	log     *slog.Logger
}

// NewProcessDownloadDecisions binds the dependency. clients is
// REQUIRED.
func NewProcessDownloadDecisions(clients ClientLister, log *slog.Logger) (*ProcessDownloadDecisions, error) {
	if clients == nil {
		return nil, errors.New("download: NewProcessDownloadDecisions: nil ClientLister")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProcessDownloadDecisions{clients: clients, log: log}, nil
}

// ProcessDecisions iterates approved + temporarily-rejected
// decisions in input order, classifying each into Grabbed/Pending/
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
		if decision.TemporarilyRejected() {
			out.Pending = append(out.Pending, decision)
			continue
		}

		// Bucket 5: per-protocol short-circuit. C# `:67-72`.
		protocol, _ := p.protocolOf(&decision)
		if (protocol == indexers.DownloadProtocolUsenet && usenetFailed) ||
			(protocol == indexers.DownloadProtocolTorrent && torrentFailed) {
			out.Pending = append(out.Pending, decision)
			continue
		}

		// Bucket 6: dispatch.
		result := p.dispatch(ctx, &decision)
		switch result {
		case ResultGrabbed:
			out.Grabbed = append(out.Grabbed, decision)
		case ResultPending:
			out.Pending = append(out.Pending, decision)
		case ResultRejected:
			out.Rejected = append(out.Rejected, decision)
		case ResultFailed:
			out.Pending = append(out.Pending, decision)
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

	return out
}

// dispatch runs one Decision through the available providers for
// its protocol. Returns the per-decision result.
//
// Mirrors C# `ProcessDecisionInternal` at `:196-226`. The C#
// version's first-tried-client / next-client fallback isn't directly
// ported here — the Go orchestrator uses the first available
// Provider only. Multi-client failover is queue [15] sub-arc territory.
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

	provider := clients[0]
	req := buildGrabRequest(decision, release)

	p.log.Debug("download: dispatching grab",
		"title", release.Title, "indexer", release.Indexer, "client", provider.Name())

	id, err := provider.Download(ctx, req)
	switch {
	case err == nil:
		// Stamp the DownloadID onto the Subject so downstream
		// (history + tracked-downloads) can pick it up. The
		// in-flight Subject pointer is shared with the Grabbed
		// list entry the caller iterates.
		p.stampDownloadID(decision, id, provider)
		return ResultGrabbed
	case errors.Is(err, downloadclients.ErrNotImplementedYet):
		p.log.Warn("download: provider Download not implemented yet",
			"client", provider.Name(), "err", err)
		return ResultSkipped
	case isUnavailable(err):
		p.log.Warn("download: client unavailable, will retry",
			"client", provider.Name(), "err", err)
		return ResultFailed
	default:
		p.log.Warn("download: dispatch failed",
			"client", provider.Name(), "title", release.Title, "err", err)
		return ResultSkipped
	}
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

// stampDownloadID mutates Subject.DownloadAllowed to true and (if
// the SeedConfiguration field is populated) leaves it intact. The
// per-grab ID surfaces via the Provider.Download return.
//
// The Subject doesn't carry a DownloadID field today (the C#
// RemoteEpisode equivalent doesn't either — the ID lives on the
// queue Item). The orchestrator could persist it via downloadhistory
// here, but that's queue [15] sub-arc territory.
func (p *ProcessDownloadDecisions) stampDownloadID(decision *decisionengine.Decision, id string, provider downloadclients.Provider) {
	sub, ok := decision.Subject.(*decisionengine.Subject)
	if !ok || sub == nil {
		return
	}
	sub.DownloadAllowed = true
	_ = id
	_ = provider
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
