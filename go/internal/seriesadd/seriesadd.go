// Package seriesadd ports the AddOptions-set branch of C#
// `Tv/SeriesScannedHandler.cs:36-74` — the post-add-actions arm that
// runs when a freshly-added series completes its first disk scan.
// Slice 5.J216.
//
// The C# SeriesScannedHandler is a single class with TWO IHandle
// implementations (SeriesScannedEvent + SeriesScanSkippedEvent). Both
// route to a shared HandleScanEvents helper that branches on
// `series.AddOptions == null`:
//
//   - AddOptions == null → call EpisodeRefreshedService.Search.
//     Already ported and wired by J211 (the rescan→re-search dispatch
//     chain).
//   - AddOptions != null → run the 4-step post-add sequence this
//     package handles.
//
// The 4-step sequence (C# `:46-73`):
//
//  1. EpisodeMonitoredService.SetEpisodeMonitoredStatus(series, opts)
//     — apply the user's per-add Monitor choice to every episode.
//     Cascade re-stamps Seasons[].Monitored to match.
//  2. PublishEvent(SeriesAddCompletedEvent(series)) — notify
//     subscribers (notification providers, telemetry).
//  3. Push search commands based on AddOptions search flags:
//     - Both Missing + CutoffUnmet → SeriesSearchCommand
//     (J215 ported this command).
//     - Only Missing → MissingEpisodeSearchCommand (J39).
//     - Only CutoffUnmet → CutoffUnmetEpisodeSearchCommand
//     (NOT YET PORTED — see deferral note below).
//  4. series.AddOptions = null + RemoveAddOptions(series) — clear the
//     post-add gate so the NEXT scan event for this series routes
//     through the J211 episoderefreshed.Search branch instead.
//
// Deferral: the CutoffUnmetEpisodeSearchCommand handler isn't yet
// ported in the Go-port. When AddOptions has SearchForCutoffUnmetEpisodes=
// true AND NOT SearchForMissingEpisodes (the "only cutoff" branch at
// C# `:66-69`), the Go-port warn-logs and skips that push. The other
// three branches (both flags / only missing / neither flag) fire
// correctly. The Steps 1, 2, 4 always run regardless of the search-
// flag branches.
//
// All errors are absorbed via warn-log + continue — one bad step
// shouldn't abort the remaining steps. Specifically: a cascade
// failure still publishes SeriesAddCompletedEvent (Step 2) +
// triggers the search-command push (Step 3) + clears AddOptions
// (Step 4). The post-add lifecycle should complete as far as
// possible to avoid leaving the series in a half-initialised state.
package seriesadd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/episodemonitored"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// MonitorCascader is the narrow seam for Step 1 of the post-add
// sequence — the episode-monitored cascade. `*episodemonitored.Service`
// satisfies via SetEpisodeMonitoredStatus.
type MonitorCascader interface {
	SetEpisodeMonitoredStatus(ctx context.Context, ser *series.Series, opts *episodemonitored.MonitoringOptions) error
}

// CommandPusher is the narrow seam for Step 3 — pushing the per-add
// search command. `*commands.Service` satisfies via Push.
type CommandPusher interface {
	Push(ctx context.Context, name, body string, priority commands.Priority, trigger commands.Trigger) (*commands.Command, error)
}

// AddOptionsClearer is the narrow seam for Step 4 — clearing the
// AddOptions blob from the persisted series row. `*series.Service`
// satisfies via RemoveAddOptions.
type AddOptionsClearer interface {
	RemoveAddOptions(ctx context.Context, seriesID int) error
}

// Service runs the post-add-actions sequence as a SeriesScanned[Skipped]
// subscriber. All four deps required (nil any returns an error from
// NewService); the bus is optional — when nil the
// SeriesAddCompletedEvent publish step is silently skipped.
type Service struct {
	cascader MonitorCascader
	pusher   CommandPusher
	clearer  AddOptionsClearer
	bus      *messaging.Bus
	log      *slog.Logger
}

// NewService binds the four deps. Returns a typed error if any
// required dep is nil. log defaults to slog.Default when nil.
func NewService(cascader MonitorCascader, pusher CommandPusher, clearer AddOptionsClearer, bus *messaging.Bus, log *slog.Logger) (*Service, error) {
	if cascader == nil {
		return nil, errors.New("seriesadd: nil cascader")
	}
	if pusher == nil {
		return nil, errors.New("seriesadd: nil pusher")
	}
	if clearer == nil {
		return nil, errors.New("seriesadd: nil clearer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{cascader: cascader, pusher: pusher, clearer: clearer, bus: bus, log: log}, nil
}

// HandleSeriesScanned routes a SeriesScannedEvent through the
// post-add-actions sequence. Subscribes to BOTH SeriesScannedEvent
// and SeriesScanSkippedEvent via [Service.HandleSeriesScanSkipped]
// (which just delegates here) — C# `SeriesScannedHandler` implements
// both IHandle interfaces dispatching to the same HandleScanEvents
// helper.
//
// Returns nil on the AddOptions==nil branch (the J211 episoderefreshed
// path handles that case; this subscriber gates the opposite way to
// avoid double-handling). Returns nil after the 4-step sequence
// completes — step errors are absorbed inside via warn-log.
func (s *Service) HandleSeriesScanned(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
	if s == nil || ev.Series == nil {
		return nil
	}
	return s.handleScanEvents(ctx, ev.Series)
}

// HandleSeriesScanSkipped routes a SeriesScanSkippedEvent through the
// same logic. C# `SeriesScannedHandler:82-84` does identical dispatch
// for both event types.
func (s *Service) HandleSeriesScanSkipped(ctx context.Context, ev mediafiles.SeriesScanSkippedEvent) error {
	if s == nil || ev.Series == nil {
		return nil
	}
	return s.handleScanEvents(ctx, ev.Series)
}

// handleScanEvents is the shared 4-step sequence. Ports C# `:36-74`
// HandleScanEvents with the AddOptions != null gate.
func (s *Service) handleScanEvents(ctx context.Context, ser *series.Series) error {
	opts, err := series.DecodeAddOptions(ser.AddOptions)
	if err != nil {
		// Decode failure → can't reason about flags. Log + skip; the
		// J211 episoderefreshed.Search subscriber for the same event
		// will still fire (each subscriber is independent).
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"seriesadd: AddOptions decode failed — skipping post-add actions",
			slog.Int("seriesId", ser.ID),
			slog.String("title", ser.Title),
			slog.Any("err", err))
		return nil
	}
	if opts == nil {
		// AddOptions absent → not the post-add path. The J211
		// episoderefreshed.Search subscriber handles this case
		// (independent subscriber on the same event).
		return nil
	}

	s.log.LogAttrs(ctx, slog.LevelInfo,
		"seriesadd: post-add actions",
		slog.Int("seriesId", ser.ID),
		slog.String("title", ser.Title))

	// Step 1 — Episode monitored cascade (C# :47).
	cascadeOpts := &episodemonitored.MonitoringOptions{Monitor: opts.Monitor}
	if err := s.cascader.SetEpisodeMonitoredStatus(ctx, ser, cascadeOpts); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"seriesadd: SetEpisodeMonitoredStatus failed — continuing post-add sequence",
			slog.Int("seriesId", ser.ID),
			slog.Any("err", err))
	}

	// Step 2 — Publish SeriesAddCompletedEvent (C# :49).
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, series.SeriesAddCompletedEvent{Series: ser})
	}

	// Step 3 — Push search commands based on the AddOptions flags (C# :55-69).
	s.pushSearchCommands(ctx, ser, opts)

	// Step 4 — Clear AddOptions so the next scan event routes through
	// the J211 path (C# :72-73).
	if err := s.clearer.RemoveAddOptions(ctx, ser.ID); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"seriesadd: RemoveAddOptions failed — series stays in post-add mode for the next scan",
			slog.Int("seriesId", ser.ID),
			slog.Any("err", err))
	}

	return nil
}

// pushSearchCommands ports C# `:55-69` — the 3-branch dispatch based on
// AddOptions.SearchForMissingEpisodes + .SearchForCutoffUnmetEpisodes.
//
// Branches:
//
//   - Both flags set → SeriesSearchCommand (J215). Logged efficient
//     because the season-grouped search avoids redundant per-episode
//     hits when both Missing + CutoffUnmet apply.
//   - Only Missing → MissingEpisodeSearchCommand (J39).
//   - Only CutoffUnmet → DEFERRED. CutoffUnmetEpisodeSearchCommand
//     handler isn't yet ported in the Go-port. Warn-log and skip.
//   - Neither flag → no push.
func (s *Service) pushSearchCommands(ctx context.Context, ser *series.Series, opts *series.AddOptions) {
	body := fmt.Sprintf(`{"seriesId":%d}`, ser.ID)
	const (
		seriesSearchName  = "SeriesSearch"
		missingSearchName = "MissingEpisodeSearch"
	)

	switch {
	case opts.SearchForMissingEpisodes && opts.SearchForCutoffUnmetEpisodes:
		s.pushOne(ctx, seriesSearchName, body, ser.ID)
	case opts.SearchForMissingEpisodes:
		// Missing-only — push the targeted MissingEpisodeSearchCommand
		// with the seriesId scope filter.
		missingBody := mustMissingBody(ser.ID)
		s.pushOne(ctx, missingSearchName, missingBody, ser.ID)
	case opts.SearchForCutoffUnmetEpisodes:
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"seriesadd: CutoffUnmetEpisodeSearchCommand not yet ported — search skipped",
			slog.Int("seriesId", ser.ID))
	}
}

func (s *Service) pushOne(ctx context.Context, name, body string, seriesID int) {
	if _, err := s.pusher.Push(ctx, name, body, commands.PriorityNormal, commands.TriggerUnspecified); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"seriesadd: failed to push search command",
			slog.String("command", name),
			slog.Int("seriesId", seriesID),
			slog.Any("err", err))
	}
}

// missingBody is the JSON shape MissingEpisodeSearchCommand expects
// (see handlers/missingepisodesearch.go MissingEpisodeSearchBody —
// SeriesId is *int; we set it to the series ID; Monitored defaults
// true).
type missingBody struct {
	SeriesID int `json:"seriesId"`
}

func mustMissingBody(seriesID int) string {
	buf, _ := json.Marshal(missingBody{SeriesID: seriesID})
	return string(buf)
}
