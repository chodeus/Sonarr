package download

import (
	"context"
	"encoding/json"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// PendingWriter is the narrow persistence surface
// [ProcessDownloadDecisions] hands the per-batch pending-release queue
// to. `*pendingreleases.Service.AddMany` satisfies it directly via
// Go's structural typing. Optional dep — see
// [ProcessDownloadDecisions.SetPendingWriter].
//
// Mirrors the C# `IPendingReleaseService.AddMany` call the
// `ProcessDownloadDecisions` orchestrator makes at
// `src/NzbDrone.Core/Download/ProcessDownloadDecisions.cs:119-121`.
type PendingWriter interface {
	AddMany(ctx context.Context, items []pendingreleases.AddItem) error
}

// preparePending ports C# `ProcessDownloadDecisions.PreparePending`
// at `ProcessDownloadDecisions.cs:178-194`. It records the decision in
// out.Pending (the in-memory batch result) AND queues a persistence
// [pendingreleases.AddItem], downgrading the reason to
// [pendingreleases.ReasonFallback] when the decision's episodes were
// already grabbed or already pending earlier in the same batch (the
// C# `IsEpisodeProcessed(grabbed, report) || IsEpisodeProcessed(pending,
// report)` guard at `:186-190` — the first claimant keeps the real
// reason; subsequent same-episode releases are stored as a fallback so
// they grab next time only if the first one falls through).
//
// The fallback check MUST run before out.Pending is appended so the
// candidate isn't compared against itself.
func (p *ProcessDownloadDecisions) preparePending(queue *[]pendingreleases.AddItem, out *ProcessedDecisions, decision decisionengine.Decision, reason pendingreleases.Reason) {
	if p.episodeProcessed(out.Grabbed, &decision) || p.episodeProcessed(out.Pending, &decision) {
		reason = pendingreleases.ReasonFallback
	}
	if item, ok := buildPendingItem(&decision, reason); ok {
		*queue = append(*queue, item)
	}
	out.Pending = append(out.Pending, decision)
}

// persistPending flushes the per-batch pending-release queue through
// the wired [PendingWriter]. Mirrors C# `:119-121`
// (`if (pendingAddQueue.Any()) _pendingReleaseService.AddMany(...)`).
// No-op when no writer is wired (unit tests / harnesses that opt out)
// or the queue is empty. A write failure is logged, not propagated:
// the grabs that already landed in this batch must not be unwound by a
// pending-table write error (matches the C# posture — AddMany throwing
// would abort the whole ProcessDecisions task, but the already-issued
// DownloadReport grabs are irreversible).
func (p *ProcessDownloadDecisions) persistPending(ctx context.Context, queue []pendingreleases.AddItem) {
	if p.pending == nil || len(queue) == 0 {
		return
	}
	if err := p.pending.AddMany(ctx, queue); err != nil {
		p.log.Warn("download: persisting pending releases failed",
			"err", err, "count", len(queue))
	}
}

// buildPendingItem serialises a Subject into the narrow
// [pendingreleases.Input] the persistence layer consumes. Returns
// (zero, false) when the Subject is malformed, has no matched Series
// (the C# code reads `decision.RemoteEpisode.Series.Id` — a nil Series
// would NPE there; the Go port drops the item), or carries no
// resolvable ReleaseInfo.
//
// The serialisation shape MUST round-trip through the read side:
//   - ReleaseInfoJSON ← json.Marshal(Subject.Release) — the queue/grab
//     re-hydration (api/v3/queue_grab.go:138-141) unmarshals it back
//     into parser.ReleaseInfo, and the dedup predicate
//     (pendingreleases/service.go matchingRows) reads {title,
//     publishDate, indexer} off it.
//   - ParsedEpisodeInfoJSON ← json.Marshal(Subject.ParsedEpisodeInfo)
//     — queue_grab.go:142-151 + the remove paths
//     (pendingreleases/remove.go parsedInfoFor) read {seasonNumber,
//     episodeNumbers, quality} off it.
//
// Marshaling the concrete Release (which may be *parser.TorrentInfo, a
// superset of ReleaseInfo) is fine: the read side decodes only the
// embedded ReleaseInfo fields and ignores the torrent extras.
func buildPendingItem(decision *decisionengine.Decision, reason pendingreleases.Reason) (pendingreleases.AddItem, bool) {
	sub, ok := decision.Subject.(*decisionengine.Subject)
	if !ok || sub == nil || sub.Series == nil {
		return pendingreleases.AddItem{}, false
	}
	ri := releaseInfoOf(sub.Release)
	if ri == nil {
		return pendingreleases.AddItem{}, false
	}
	releaseJSON, err := json.Marshal(sub.Release)
	if err != nil {
		return pendingreleases.AddItem{}, false
	}
	var parsedJSON string
	if sub.ParsedEpisodeInfo != nil {
		if b, mErr := json.Marshal(sub.ParsedEpisodeInfo); mErr == nil {
			parsedJSON = string(b)
		}
	}
	in := pendingreleases.Input{
		SeriesID:              sub.Series.ID,
		MatchTitle:            ri.Title,
		MatchPubDate:          ri.PublishDate,
		MatchIndexer:          ri.Indexer,
		ParsedEpisodeInfoJSON: parsedJSON,
		ReleaseInfoJSON:       string(releaseJSON),
	}
	return pendingreleases.AddItem{Input: in, Reason: reason}, true
}

// RejectedInputFrom extracts the (SeriesID, Title, PublishDate,
// Indexer) match-tuple a pending-release REMOVAL keys on, from a
// PERMANENTLY-rejected decision's Subject. Returns (zero, false) when
// the Subject is malformed / has no matched Series or resolvable
// ReleaseInfo.
//
// Mirrors the tuple C# `RemoveRejected` reads off each rejected
// `DownloadDecision.RemoteEpisode.Release` via MatchingReleasePredicate
// (`src/NzbDrone.Core/Download/Pending/PendingReleaseService.cs:597-612`).
// Shares [releaseInfoOf] with [buildPendingItem] so the add-side and
// remove-side match tuples can never drift apart.
func RejectedInputFrom(decision decisionengine.Decision) (pendingreleases.RejectedInput, bool) {
	sub, ok := decision.Subject.(*decisionengine.Subject)
	if !ok || sub == nil || sub.Series == nil {
		return pendingreleases.RejectedInput{}, false
	}
	ri := releaseInfoOf(sub.Release)
	if ri == nil {
		return pendingreleases.RejectedInput{}, false
	}
	return pendingreleases.RejectedInput{
		SeriesID:     sub.Series.ID,
		MatchTitle:   ri.Title,
		MatchPubDate: ri.PublishDate,
		MatchIndexer: ri.Indexer,
	}, true
}

// releaseInfoOf resolves the base [parser.ReleaseInfo] out of the
// Subject's `any`-typed Release (which is either *parser.ReleaseInfo
// or *parser.TorrentInfo). Returns nil for any other shape.
func releaseInfoOf(release any) *parser.ReleaseInfo {
	switch r := release.(type) {
	case *parser.ReleaseInfo:
		return r
	case *parser.TorrentInfo:
		return &r.ReleaseInfo
	}
	return nil
}
