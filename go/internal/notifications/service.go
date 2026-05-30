package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// Service ports the C# `NotificationService` IHandle<*Event>
// fan-out orchestrator at `Notifications/NotificationService.cs:19-560`.
//
// Slice 4.AH (sub-arc N4b of queue [N4]). Sub-arc N4a (slice 4.AG)
// shipped the [Factory] that returns the per-event provider list.
// This slice subscribes the dispatchers to the event bus and walks
// each provider list invoking the matching OnX hook + recording
// success/failure through the status recorder.
//
// Per-provider failure handling: every OnX call is guarded — a
// returned error is logged + recorded as a RecordFailure on the
// status service, but the loop continues onto the next provider.
// Mirrors the C# try/catch at NotificationService.cs:155-160 (the
// per-provider catch that prevents one broken provider from
// breaking the fan-out chain).
//
// Mapping note: the C# Service handles 16 events (12 IHandle + 4
// IHandleAsync). The Go-port event surface only exposes a subset
// today; this slice wires up the events that ARE ported and
// flags the rest as follow-up. See the Subscribe doc-comment for
// the mapping table.
type Service struct {
	factory  *Factory
	recorder serviceRecorder
	logger   *slog.Logger
}

// serviceRecorder is the narrow recorder interface the service
// calls after each provider invocation. The real
// [notificationstatus.Service] satisfies this surface via its
// RecordSuccess + RecordFailure methods. Tests swap in a stub
// recorder. Nil-tolerant: when the service is constructed with
// a nil recorder, success/failure recording is skipped — useful
// for migrations and unit tests that don't need to exercise the
// back-off side-effect.
type serviceRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// NewService binds a Factory + recorder + logger into a Service.
// recorder may be nil (skip recording). logger may be nil (use
// [slog.Default]).
func NewService(factory *Factory, recorder serviceRecorder, logger *slog.Logger) (*Service, error) {
	if factory == nil {
		return nil, fmt.Errorf("notifications: NewService: nil factory")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		factory:  factory,
		recorder: recorder,
		logger:   logger,
	}, nil
}

// Subscribe registers the Service's IHandle dispatchers on the
// given event bus. Mirrors the C# IHandle<TEvent> interface
// declarations on NotificationService — the DI container scans
// for those interfaces and wires the subscriptions automatically;
// the Go port wires them explicitly via [messaging.Subscribe].
//
// Mapping table (C# event → Go event):
//
//   - EpisodeGrabbedEvent → decisionengine.EpisodeGrabbedEvent
//   - EpisodeImportedEvent → episodeimport.EpisodeImportedEvent
//     (slice 4.BU / C-cleanup [C6]). Fires OnDownload per imported
//     file. OnImportComplete batch dispatch waits on the
//     DownloadCompletedEvent upstream port.
//   - SeriesAddCompletedEvent → series.SeriesAddedEvent
//   - SeriesDeletedEvent → series.SeriesDeletedEvent
//   - EpisodeFileDeletedEvent → episodefiles.EpisodeFileDeletedEvent
//   - HealthCheckFailedEvent → healthcheck.FailedEvent
//   - HealthCheckRestoredEvent → healthcheck.RestoredEvent
//   - HealthCheckCompleteEvent (async) → healthcheck.CompleteEvent
//     (drives ProcessQueue drain)
//
// Deferred until upstream events port (slice 4.BU C-cleanup [C6]
// closure note — the Go tree has no equivalent event type yet, so
// these wires are tracked here for re-evaluation when the upstream
// event lands):
//
//   - UpdateInstalledEvent — OnApplicationUpdate dispatch. Waits on
//     the update.Service port (no Go-tree equivalent yet).
//   - ManualInteractionRequiredEvent — OnManualInteractionRequired
//     dispatch. Waits on the trackeddownloads manual-interaction
//     emit point.
//   - DeleteCompletedEvent / DownloadsProcessedEvent /
//     RenameCompletedEvent — drain ProcessQueue.
//     HealthCheckCompleteEvent already covers the
//     ProcessQueue-drain pattern; the additional drains land when
//     their upstream events port.
//
// Subscribe is idempotent only insofar as messaging.Subscribe is —
// calling it twice on the same bus produces two subscriptions.
func (s *Service) Subscribe(bus *messaging.Bus) {
	if bus == nil {
		return
	}
	messaging.Subscribe(bus, "notifications.HandleEpisodeGrabbedEvent", s.HandleEpisodeGrabbedEvent)
	messaging.Subscribe(bus, "notifications.HandleEpisodeImportedEvent", s.HandleEpisodeImportedEvent)
	messaging.Subscribe(bus, "notifications.HandleSeriesAddedEvent", s.HandleSeriesAddedEvent)
	messaging.Subscribe(bus, "notifications.HandleSeriesDeletedEvent", s.HandleSeriesDeletedEvent)
	messaging.Subscribe(bus, "notifications.HandleEpisodeFileDeletedEvent", s.HandleEpisodeFileDeletedEvent)
	messaging.Subscribe(bus, "notifications.HandleSeriesRenamedEvent", s.HandleSeriesRenamedEvent)
	messaging.Subscribe(bus, "notifications.HandleDownloadCompletedEvent", s.HandleDownloadCompletedEvent)
	messaging.Subscribe(bus, "notifications.HandleUntrackedDownloadCompletedEvent", s.HandleUntrackedDownloadCompletedEvent)
	messaging.Subscribe(bus, "notifications.HandleHealthCheckFailedEvent", s.HandleHealthCheckFailedEvent)
	messaging.Subscribe(bus, "notifications.HandleHealthCheckRestoredEvent", s.HandleHealthCheckRestoredEvent)
	messaging.SubscribeAsync(bus, "notifications.HandleHealthCheckCompleteEvent", s.HandleHealthCheckCompleteEvent)
}

// HandleEpisodeGrabbedEvent ports the C# `Handle(EpisodeGrabbedEvent)`
// at `NotificationService.cs:130-161`. Builds a [GrabMessage] from
// the EpisodeGrabbedEvent and fans out to every OnGrabEnabled
// provider, gated by ShouldHandleSeries.
func (s *Service) HandleEpisodeGrabbedEvent(ctx context.Context, ev decisionengine.EpisodeGrabbedEvent) error {
	if ev.Episode == nil || ev.Episode.Series == nil {
		return nil
	}
	providers, err := s.factory.OnGrabEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnGrab providers", slog.Any("err", err))
		return nil
	}
	episodesCopy := materialiseEpisodes(ev.Episode.Episodes)
	var qual *quality.Model
	if pe := ev.Episode.ParsedEpisodeInfo; pe != nil {
		q := pe.Quality
		qual = &q
	}
	msg := GrabMessage{
		Message:            getMessage(ev.Episode.Series, episodesCopy, qual),
		Series:             ev.Episode.Series,
		Episode:            ev.Episode,
		Quality:            qual,
		DownloadClientType: ev.DownloadClient,
		DownloadClientName: ev.DownloadClientName,
		DownloadID:         ev.DownloadID,
	}
	s.fanOut(ctx, providers, ev.Episode.Series.Tags, "OnGrab", func(p Provider) error {
		return p.OnGrab(ctx, msg)
	})
	return nil
}

// HandleEpisodeImportedEvent ports the C# `Handle(EpisodeImportedEvent)`
// at `NotificationService.cs:163-202`. Builds a [DownloadMessage] from
// the imported file + LocalEpisode + DownloadClientItem and fans out
// to every OnDownloadEnabled provider, gated by ShouldHandleSeries +
// the per-provider OnUpgrade flag.
//
// Bail-outs:
//
//   - NewDownload == false (matches C# `:165-168`). Manual-rescan
//     re-emissions don't fire OnDownload.
//   - nil LocalEpisode / nil LocalEpisode.Series (defensive).
//
// OnUpgrade gate (C# `:189`): when OldFiles is non-empty AND the
// provider's OnUpgrade flag is false, suppress. Empty OldFiles AND
// any OnUpgrade value → fire. The C# `OnUpgrade` is a typed bool
// (defaults false on row creation); the Go port's *bool nil is
// treated as false to match.
//
// Slice 4.BU (C-cleanup [C6]).
func (s *Service) HandleEpisodeImportedEvent(ctx context.Context, ev episodeimport.EpisodeImportedEvent) error {
	if !ev.NewDownload {
		return nil
	}
	if ev.LocalEpisode == nil || ev.LocalEpisode.Series == nil {
		return nil
	}

	providers, err := s.factory.OnDownloadEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnDownload providers", slog.Any("err", err))
		return nil
	}

	le := ev.LocalEpisode
	episodesCopy := materialiseEpisodes(le.Episodes)
	qual := le.Quality
	msg := DownloadMessage{
		Message:     getMessage(le.Series, episodesCopy, &qual),
		Series:      le.Series,
		EpisodeInfo: le,
		EpisodeFile: ev.EpisodeFile,
		OldFiles:    materialiseDeletedFiles(ev.OldFiles),
		SourcePath:  le.Path,
		Release:     le.Release,
	}
	if ev.DownloadClientItem != nil {
		msg.DownloadClientInfo = ev.DownloadClientItem.DownloadClientInfo
		msg.DownloadID = ev.DownloadClientItem.DownloadID
	}

	upgrade := len(msg.OldFiles) > 0
	tags := le.Series.Tags
	for _, p := range providers {
		def := p.Definition()
		if def == nil {
			continue
		}
		if !ShouldHandleSeries(def.Tags, tags) {
			s.logger.Debug("notifications: tags mismatch",
				slog.Int("provider_id", def.ID),
				slog.String("provider_name", def.Name),
				slog.String("hook", "OnDownload"))
			continue
		}
		// OnUpgrade gate. C# `:189`: when OldFiles is non-empty, only
		// fire if the provider's OnUpgrade is true. Empty OldFiles →
		// always fire.
		if upgrade {
			if def.OnUpgrade == nil || !*def.OnUpgrade {
				s.logger.Debug("notifications: OnDownload suppressed by OnUpgrade=false on upgrade path",
					slog.Int("provider_id", def.ID))
				continue
			}
		}
		if err := p.OnDownload(ctx, msg); err != nil {
			s.recordFailure(ctx, def, "OnDownload", err)
			continue
		}
		s.recordSuccess(ctx, def)
	}
	return nil
}

// HandleDownloadCompletedEvent ports C# `NotificationService.Handle(DownloadCompletedEvent)`
// (`NotificationService.cs:204-245`). Builds an ImportCompleteMessage
// from the tracked download's RemoteEpisode (series/episodes/parsed-info)
// + the imported EpisodeFiles, and fans out OnImportComplete to every
// enabled + tag-matched provider. No custom-format dependency — the C#
// handler reads only the RemoteEpisode the event already carries.
func (s *Service) HandleDownloadCompletedEvent(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
	td := ev.TrackedDownload
	if td == nil || td.RemoteEpisode == nil || td.RemoteEpisode.Series == nil || td.RemoteEpisode.ParsedEpisodeInfo == nil {
		return nil
	}
	re := td.RemoteEpisode
	seriesRow := re.Series
	eps := materialiseEpisodes(re.Episodes)
	files := materialiseFiles(ev.EpisodeFiles)
	pei := re.ParsedEpisodeInfo
	q := pei.Quality

	providers, err := s.factory.OnImportCompleteEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnImportComplete providers", slog.Any("err", err))
		return nil
	}

	msg := ImportCompleteMessage{
		Message:         importCompleteMessageText(seriesRow, eps, pei, &q),
		Series:          seriesRow,
		Episodes:        eps,
		EpisodeFiles:    files,
		Release:         ev.Release,
		DestinationPath: destinationPath(seriesRow, files),
		ReleaseGroup:    pei.ReleaseGroup,
		ReleaseQuality:  &q,
	}
	if td.DownloadItem != nil {
		msg.DownloadClientInfo = td.DownloadItem.DownloadClientInfo
		msg.DownloadID = td.DownloadItem.DownloadID
		msg.SourcePath = td.DownloadItem.OutputPath.FullPath()
	}
	s.fanOut(ctx, providers, seriesRow.Tags, "OnImportComplete", func(p Provider) error {
		return p.OnImportComplete(ctx, msg)
	})
	return nil
}

// HandleUntrackedDownloadCompletedEvent ports C#
// `NotificationService.Handle(UntrackedDownloadCompletedEvent)`
// (`NotificationService.cs:248-287`) — the manual-import (untracked)
// sibling of HandleDownloadCompletedEvent. Same ImportCompleteMessage +
// OnImportComplete fan-out, sourced from the event's own series /
// episodes / parsed-info / files / source path.
func (s *Service) HandleUntrackedDownloadCompletedEvent(ctx context.Context, ev trackeddownloads.UntrackedDownloadCompletedEvent) error {
	if ev.Series == nil || ev.ParsedEpisodeInfo == nil {
		return nil
	}
	eps := materialiseEpisodes(ev.Episodes)
	files := materialiseFiles(ev.EpisodeFiles)
	pei := ev.ParsedEpisodeInfo
	q := pei.Quality

	providers, err := s.factory.OnImportCompleteEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnImportComplete providers", slog.Any("err", err))
		return nil
	}

	msg := ImportCompleteMessage{
		Message:         importCompleteMessageText(ev.Series, eps, pei, &q),
		Series:          ev.Series,
		Episodes:        eps,
		EpisodeFiles:    files,
		SourcePath:      ev.SourcePath,
		SourceTitle:     pei.ReleaseTitle,
		DestinationPath: destinationPath(ev.Series, files),
		ReleaseGroup:    pei.ReleaseGroup,
		ReleaseQuality:  &q,
	}
	s.fanOut(ctx, providers, ev.Series.Tags, "OnImportComplete", func(p Provider) error {
		return p.OnImportComplete(ctx, msg)
	})
	return nil
}

// importCompleteMessageText picks the full-season vs per-episode message
// shape (C# `NotificationService.cs:212-214`): a FullSeason parse with at
// least one episode uses the "Season N" summary; otherwise the standard
// per-episode message.
func importCompleteMessageText(s *series.Series, eps []*episodes.Episode, pei *parser.ParsedEpisodeInfo, q *quality.Model) string {
	if pei != nil && pei.FullSeason && len(eps) > 0 && eps[0] != nil {
		return getFullSeasonMessage(s, eps[0].SeasonNumber, q)
	}
	return getMessage(s, eps, q)
}

// getFullSeasonMessage ports C# `GetFullSeasonMessage` (`NotificationService.cs:71-76`).
func getFullSeasonMessage(s *series.Series, seasonNumber int, q *quality.Model) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%s - Season %d [%s]", s.Title, seasonNumber, getQualityString(s, q))
}

// materialiseFiles converts the event's value-slice of EpisodeFiles into
// the pointer-slice ImportCompleteMessage carries. Returns nil for empty
// input.
func materialiseFiles(fs []episodefiles.File) []*episodefiles.File {
	if len(fs) == 0 {
		return nil
	}
	out := make([]*episodefiles.File, 0, len(fs))
	for i := range fs {
		f := fs[i]
		out = append(out, &f)
	}
	return out
}

// destinationPath mirrors C# `EpisodeFiles.Select(e => Path.Join(series.Path,
// e.RelativePath)).ToList().GetLongestCommonPath()` — the common library
// directory the batch imported into.
func destinationPath(s *series.Series, files []*episodefiles.File) string {
	if s == nil || len(files) == 0 {
		return ""
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if f != nil {
			paths = append(paths, filepath.Join(s.Path, f.RelativePath))
		}
	}
	return longestCommonPath(paths)
}

// longestCommonPath ports C# `PathExtensions.GetLongestCommonPath`
// (`PathExtensions.cs:271-296`): the longest common character prefix of
// the inputs, truncated at the last separator. Empty input, or no common
// separator, → "".
func longestCommonPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	first := paths[0]
	length := len(first)
	for _, p := range paths[1:] {
		if len(p) < length {
			length = len(p)
		}
		for i := 0; i < length; i++ {
			if p[i] != first[i] {
				length = i
				break
			}
		}
	}
	sub := first[:length]
	idx := strings.LastIndexAny(sub, `/\`)
	if idx < 0 {
		return ""
	}
	return sub[:idx]
}

// materialiseDeletedFiles converts the event's value-slice into the
// pointer-slice the DownloadMessage carries. Returns nil for empty
// input so the upgrade gate at the call site works on len() rather
// than a defensive nil-check.
func materialiseDeletedFiles(in []episodefiles.DeletedFile) []*episodefiles.DeletedFile {
	if len(in) == 0 {
		return nil
	}
	out := make([]*episodefiles.DeletedFile, len(in))
	for i := range in {
		df := in[i]
		out[i] = &df
	}
	return out
}

// HandleSeriesAddedEvent ports the C# Handle(SeriesAddCompletedEvent)
// at `NotificationService.cs:443-456` (approx — see the C# source
// for the exact line). The Go-port SeriesAddedEvent is the per-row
// event; the C# tree adds a SeriesAddCompletedEvent batch shape.
// Until that batch event ports, the per-row event is the closest
// match — every Series-add gets one OnSeriesAdd dispatch.
func (s *Service) HandleSeriesAddedEvent(ctx context.Context, ev series.SeriesAddedEvent) error {
	if ev.Series == nil {
		return nil
	}
	providers, err := s.factory.OnSeriesAddEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnSeriesAdd providers", slog.Any("err", err))
		return nil
	}
	msg := SeriesAddMessage{
		Message: ev.Series.Title,
		Series:  ev.Series,
	}
	s.fanOut(ctx, providers, ev.Series.Tags, "OnSeriesAdd", func(p Provider) error {
		return p.OnSeriesAdd(ctx, msg)
	})
	return nil
}

// HandleSeriesDeletedEvent ports the C# Handle(SeriesDeletedEvent).
// The Go-port event carries a slice (bulk-deletion supported); the
// service fans out one OnSeriesDelete dispatch per series in the
// batch.
func (s *Service) HandleSeriesDeletedEvent(ctx context.Context, ev series.SeriesDeletedEvent) error {
	if len(ev.Series) == 0 {
		return nil
	}
	providers, err := s.factory.OnSeriesDeleteEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnSeriesDelete providers", slog.Any("err", err))
		return nil
	}
	for i := range ev.Series {
		seriesRow := ev.Series[i]
		msg := NewSeriesDeleteMessage(&seriesRow, ev.DeleteFiles)
		s.fanOut(ctx, providers, seriesRow.Tags, "OnSeriesDelete", func(p Provider) error {
			return p.OnSeriesDelete(ctx, msg)
		})
	}
	return nil
}

// HandleSeriesRenamedEvent ports the C# `Handle(SeriesRenamedEvent)` at
// `NotificationService.cs:290-308`. Fans out one OnRename dispatch per
// renamed series to every OnRenameEnabled provider, gated by
// ShouldHandleSeries.
//
// VERIFY-FIRST (J503): this was long doc-flagged "defers until the
// batch event lands" — but episodefiles.SeriesRenamedEvent has been
// defined + published since J453 (the extras MoveFilesAfterRename
// consumer), and the provider OnRename method + Factory.OnRenameEnabled
// + the OnRename config flag + ShouldHandleSeries all pre-existed. The
// only missing piece was this wire — so OnRename notifications were
// silently never sent.
func (s *Service) HandleSeriesRenamedEvent(ctx context.Context, ev episodefiles.SeriesRenamedEvent) error {
	if ev.Series == nil {
		return nil
	}
	providers, err := s.factory.OnRenameEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnRename providers", slog.Any("err", err))
		return nil
	}
	// Pack the typed renamed-file records into the []any the
	// RenameMessage carries (mirrors the OnGrab/OnDownload pattern of
	// passing the internal model straight through to the payload
	// builder).
	renamed := make([]any, len(ev.RenamedFiles))
	for i := range ev.RenamedFiles {
		renamed[i] = ev.RenamedFiles[i]
	}
	msg := RenameMessage{
		Series:       ev.Series,
		RenamedFiles: renamed,
	}
	s.fanOut(ctx, providers, ev.Series.Tags, "OnRename", func(p Provider) error {
		return p.OnRename(ctx, msg)
	})
	return nil
}

// HandleEpisodeFileDeletedEvent ports the C# Handle(EpisodeFileDeletedEvent)
// at `NotificationService.cs:384-422`. Skips files with no linked
// episode (matches the C# `Empty()` short-circuit at `:386-391` —
// "metadata was removed", nothing to notify about).
func (s *Service) HandleEpisodeFileDeletedEvent(ctx context.Context, ev episodefiles.EpisodeFileDeletedEvent) error {
	if ev.EpisodeFile == nil || len(ev.LinkedEpisodes) == 0 {
		return nil
	}
	providers, err := s.factory.OnEpisodeFileDeleteEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnEpisodeFileDelete providers", slog.Any("err", err))
		return nil
	}
	episodesCopy := make([]*episodes.Episode, len(ev.LinkedEpisodes))
	for i := range ev.LinkedEpisodes {
		e := ev.LinkedEpisodes[i]
		episodesCopy[i] = &e
	}
	msg := EpisodeDeleteMessage{
		Message:     getMessage(ev.Series, episodesCopy, nil),
		Series:      ev.Series,
		EpisodeFile: ev.EpisodeFile,
		Episodes:    episodesCopy,
		Reason:      ev.Reason,
	}
	var seriesTags []int
	if ev.Series != nil {
		seriesTags = ev.Series.Tags
	}
	s.fanOut(ctx, providers, seriesTags, "OnEpisodeFileDelete", func(p Provider) error {
		return p.OnEpisodeFileDelete(ctx, msg)
	})
	return nil
}

// HandleHealthCheckFailedEvent ports the C# Handle(HealthCheckFailedEvent)
// at `NotificationService.cs:493-518` (approx). The dispatcher
// honours the per-provider IncludeHealthWarnings flag — Warning
// checks only fire when the row's IncludeHealthWarnings is true.
//
// Suppresses notifications during the startup-grace window
// (IsInStartupGracePeriod=true) — matches the C# guard that
// prevents a flurry of post-restart alerts.
func (s *Service) HandleHealthCheckFailedEvent(ctx context.Context, ev healthcheck.FailedEvent) error {
	if ev.IsInStartupGracePeriod {
		return nil
	}
	providers, err := s.factory.OnHealthIssueEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnHealthIssue providers", slog.Any("err", err))
		return nil
	}
	msg := HealthCheckMessage{Check: ev.HealthCheck}
	// HealthIssue uses a custom gating function instead of
	// ShouldHandleSeries — no series context, only the per-provider
	// IncludeHealthWarnings flag matters.
	for _, p := range providers {
		def := p.Definition()
		if def == nil {
			continue
		}
		if !ShouldHandleHealthFailure(ev.HealthCheck.Type, def.IncludeHealthWarnings) {
			continue
		}
		if err := p.OnHealthIssue(ctx, msg); err != nil {
			s.recordFailure(ctx, def, "OnHealthIssue", err)
			continue
		}
		s.recordSuccess(ctx, def)
	}
	return nil
}

// HandleHealthCheckRestoredEvent ports the C# Handle(HealthCheckRestoredEvent)
// at `NotificationService.cs:520-543` (approx). Mirror of
// HandleHealthCheckFailedEvent for the restored side — same
// startup-grace gate, same IncludeHealthWarnings gating (the C#
// code only fires when the originally-failing check would itself
// have fired, which is the same predicate).
func (s *Service) HandleHealthCheckRestoredEvent(ctx context.Context, ev healthcheck.RestoredEvent) error {
	if ev.IsInStartupGracePeriod {
		return nil
	}
	providers, err := s.factory.OnHealthRestoredEnabled(ctx, true)
	if err != nil {
		s.logger.Warn("notifications: load OnHealthRestored providers", slog.Any("err", err))
		return nil
	}
	msg := HealthCheckMessage{Check: ev.PreviousCheck}
	for _, p := range providers {
		def := p.Definition()
		if def == nil {
			continue
		}
		if !ShouldHandleHealthFailure(ev.PreviousCheck.Type, def.IncludeHealthWarnings) {
			continue
		}
		if err := p.OnHealthRestored(ctx, msg); err != nil {
			s.recordFailure(ctx, def, "OnHealthRestored", err)
			continue
		}
		s.recordSuccess(ctx, def)
	}
	return nil
}

// HandleHealthCheckCompleteEvent ports the C# IHandleAsync<HealthCheckCompleteEvent>
// at `NotificationService.cs:451-465` (approx). Drains every
// provider's ProcessQueue — the per-host coalescing buffer the
// Plex/Kodi/Emby providers maintain. Webhook + Discord ProcessQueue
// are no-ops so this is effectively a no-op until those providers
// port.
func (s *Service) HandleHealthCheckCompleteEvent(ctx context.Context, _ healthcheck.CompleteEvent) error {
	providers, err := s.factory.OnHealthIssueEnabled(ctx, false)
	if err != nil {
		s.logger.Warn("notifications: load providers for ProcessQueue", slog.Any("err", err))
		return nil
	}
	for _, p := range providers {
		def := p.Definition()
		if err := p.ProcessQueue(ctx); err != nil && def != nil {
			s.logger.Warn("notifications: ProcessQueue failed",
				slog.Int("provider_id", def.ID),
				slog.String("provider_name", def.Name),
				slog.Any("err", err))
		}
	}
	return nil
}

// fanOut walks providers and invokes the per-provider call after
// applying the [ShouldHandleSeries] tag gate. Records success or
// failure per provider; per-provider failure is logged but does
// not abort the chain.
func (s *Service) fanOut(ctx context.Context, providers []Provider, seriesTags []int, hookName string, call func(Provider) error) {
	for _, p := range providers {
		def := p.Definition()
		if def == nil {
			continue
		}
		if !ShouldHandleSeries(def.Tags, seriesTags) {
			s.logger.Debug("notifications: tags mismatch",
				slog.Int("provider_id", def.ID),
				slog.String("provider_name", def.Name),
				slog.String("hook", hookName))
			continue
		}
		if err := call(p); err != nil {
			s.recordFailure(ctx, def, hookName, err)
			continue
		}
		s.recordSuccess(ctx, def)
	}
}

func (s *Service) recordSuccess(ctx context.Context, def *Notification) {
	if s.recorder == nil || def == nil || def.ID == 0 {
		return
	}
	if err := s.recorder.RecordSuccess(ctx, def.ID); err != nil {
		s.logger.Debug("notifications: recordSuccess",
			slog.Int("provider_id", def.ID),
			slog.Any("err", err))
	}
}

func (s *Service) recordFailure(ctx context.Context, def *Notification, hookName string, hookErr error) {
	s.logger.Warn("notifications: provider error",
		slog.Int("provider_id", def.ID),
		slog.String("provider_name", def.Name),
		slog.String("hook", hookName),
		slog.Any("err", hookErr))
	if s.recorder == nil || def.ID == 0 {
		return
	}
	if err := s.recorder.RecordFailure(ctx, def.ID, 0); err != nil {
		s.logger.Debug("notifications: recordFailure",
			slog.Int("provider_id", def.ID),
			slog.Any("err", err))
	}
}

// getMessage ports the C# private helper at
// `NotificationService.cs:48-69`. Builds the per-event message
// string used as the human-readable description on most payload
// types.
//
// Series.SeriesType determines the format:
//
//   - SeriesTypes.Daily → "Title - AirDate - Episode [Quality]"
//   - Anything else → "Title - {Season}x{EE}{EE}... - {Title + Title} [Quality]"
//
// quality may be nil — the C# call sites that pass nil are the
// EpisodeFileDelete arm (`:394` passes the per-file Quality which
// is always non-nil in C#; the Go port reaches here with a nil
// quality via the EpisodeDeleteMessage path because the per-file
// Quality isn't surfaced through the EpisodeFileDeletedEvent
// today — flagged as a follow-up).
func getMessage(s *series.Series, eps []*episodes.Episode, q *quality.Model) string {
	if s == nil {
		return ""
	}
	qualityString := getQualityString(s, q)
	if len(eps) == 0 {
		return fmt.Sprintf("%s - [%s]", s.Title, qualityString)
	}
	// Daily / standard branches.
	if s.SeriesType == series.SeriesTypeDaily && eps[0] != nil {
		ep := eps[0]
		return fmt.Sprintf("%s - %s - %s [%s]", s.Title, ep.AirDate, ep.Title, qualityString)
	}
	// Standard: "TitlexEE" join + " + " on titles.
	var (
		numbers []string
		titles  []string
	)
	for _, e := range eps {
		if e == nil {
			continue
		}
		numbers = append(numbers, fmt.Sprintf("x%02d", e.EpisodeNumber))
		titles = append(titles, e.Title)
	}
	if len(numbers) == 0 {
		return fmt.Sprintf("%s - [%s]", s.Title, qualityString)
	}
	episodeNumbers := strings.Join(numbers, "")
	episodeTitles := strings.Join(titles, " + ")
	return fmt.Sprintf("%s - %d%s - %s [%s]", s.Title, eps[0].SeasonNumber, episodeNumbers, episodeTitles, qualityString)
}

// getQualityString ports the C# private helper at
// `NotificationService.cs:78-95`. Appends "Proper" / "v{N}"
// suffix per the series type and revision version. Returns an
// empty-Quality fallback when q is nil.
func getQualityString(s *series.Series, q *quality.Model) string {
	if q == nil {
		return ""
	}
	out := q.Quality.Name
	if q.Revision.Version > 1 {
		if s != nil && s.SeriesType == series.SeriesTypeAnime {
			out += fmt.Sprintf(" v%d", q.Revision.Version)
		} else {
			out += " Proper"
		}
	}
	return out
}

// materialiseEpisodes converts a []episodes.Episode (value slice
// — the Subject's Episodes field shape) to []*episodes.Episode
// for use through getMessage.
func materialiseEpisodes(es []episodes.Episode) []*episodes.Episode {
	out := make([]*episodes.Episode, len(es))
	for i := range es {
		e := es[i]
		out[i] = &e
	}
	return out
}
