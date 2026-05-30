// Package pendingqueue projects persisted pending-release rows into
// queue.Queue items so delayed / download-client-unavailable releases
// surface in the Activity → Queue view alongside the in-progress
// tracked downloads.
//
// Mirrors the C# merge `QueueController.GetQueue` performs at
// `src/Sonarr.Api.V3/Queue/QueueController.cs:173-187`
// (`_queueService.GetQueue().Concat(_pendingReleaseService.GetPendingQueue…())`)
// plus the per-row projection `PendingReleaseService.GetQueueItem`
// (`src/NzbDrone.Core/Download/Pending/PendingReleaseService.cs`).
//
// The Go queue.Queue models the MODERN (Episodes-plural) shape — it
// deliberately omits the deprecated singular Episode field — so this
// projector follows the modern `GetPendingQueue` (one row per release,
// Episodes plural) rather than the obsolete per-episode `GetPendingQueueObsolete`.
//
// DIVERGENCE (documented): the C# `GetPendingQueue` also dedups to the
// best-quality release per episode-id group. The Go port emits every
// non-Fallback pending row — in practice AddMany dedups by
// (Title, PublishDate, Indexer) so the table holds one row per release,
// and two distinct releases pending for the same episode set are rare;
// the best-quality grouping is deferred (it would need the per-series
// quality-profile comparer). The download-client name (C# resolves it
// via indexer.DownloadClientId) is also left empty for now — display
// metadata only, frequently null in C# too.
package pendingqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/series"
)

// RssSyncTaskTypeName is the scheduledtasks TypeName key for the RSS
// sync command — the C# `typeof(RssSyncCommand)` next-execution lookup
// the ect clamp uses.
const RssSyncTaskTypeName = "NzbDrone.Core.Indexers.RssSyncCommand"

// PendingRowLister returns every persisted pending-release row.
// `*pendingreleases.Repo.All` satisfies it.
type PendingRowLister interface {
	All(ctx context.Context) ([]pendingreleases.Release, error)
}

// SeriesLookup resolves a series row by id. `*series.Service.GetSeries`
// satisfies it.
type SeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// EpisodeFinder resolves a single episode by (series, season, episode
// number). `*episodes.Service.FindEpisode` satisfies it.
type EpisodeFinder interface {
	FindEpisode(ctx context.Context, seriesID, seasonNumber, episodeNumber int) (*episodes.Episode, error)
}

// DelayProfileResolver resolves the per-protocol delay (minutes) of the
// best delay profile for a series' tag set. found=false means no
// applicable profile (contributes a zero protocol delay). The cmd/sonarr
// adapter wraps delayprofiles.Service.BestForTags + the C#
// GetProtocolDelay (Torrent→TorrentDelay, else UsenetDelay) switch,
// keeping this package off the delayprofiles import path.
type DelayProfileResolver interface {
	ProtocolDelay(ctx context.Context, tagIDs []int, protocol indexers.DownloadProtocol) (minutes int, found bool, err error)
}

// ConfigReader exposes the two config values the ect computation reads:
// the global minimum-age floor and the RSS sync interval (minutes).
// `*config.Provider` satisfies it.
type ConfigReader interface {
	MinimumAge() int
	RssSyncInterval() int
}

// NextRssSyncReader returns the next scheduled RSS-sync execution time.
// `*scheduledtasks.Service.GetNextExecution` satisfies it. Optional —
// when nil (or it returns an error), the ect clamp-to-next-sync branch
// is skipped.
type NextRssSyncReader interface {
	GetNextExecution(ctx context.Context, typeName string) (time.Time, error)
}

// Projector composes the read-side services to build pending queue rows.
type Projector struct {
	pending  PendingRowLister
	series   SeriesLookup
	episodes EpisodeFinder
	delay    DelayProfileResolver
	cfg      ConfigReader
	nextRss  NextRssSyncReader
	now      func() time.Time
	log      *slog.Logger
}

// NewProjector binds the dependencies. pending / series / episodes /
// delay / cfg are REQUIRED. nextRss is optional (nil → no clamp). now
// defaults to time.Now; log to slog.Default.
func NewProjector(
	pending PendingRowLister,
	seriesLookup SeriesLookup,
	episodeFinder EpisodeFinder,
	delay DelayProfileResolver,
	cfg ConfigReader,
	nextRss NextRssSyncReader,
	now func() time.Time,
	log *slog.Logger,
) (*Projector, error) {
	if pending == nil {
		return nil, errors.New("pendingqueue: nil PendingRowLister")
	}
	if seriesLookup == nil {
		return nil, errors.New("pendingqueue: nil SeriesLookup")
	}
	if episodeFinder == nil {
		return nil, errors.New("pendingqueue: nil EpisodeFinder")
	}
	if delay == nil {
		return nil, errors.New("pendingqueue: nil DelayProfileResolver")
	}
	if cfg == nil {
		return nil, errors.New("pendingqueue: nil ConfigReader")
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Projector{
		pending:  pending,
		series:   seriesLookup,
		episodes: episodeFinder,
		delay:    delay,
		cfg:      cfg,
		nextRss:  nextRss,
		now:      now,
		log:      log,
	}, nil
}

// Project returns one queue.Queue per non-Fallback pending release.
// Errors listing the rows propagate; per-row rehydration failures are
// logged and the row is skipped (a malformed blob shouldn't blank the
// whole queue).
func (p *Projector) Project(ctx context.Context) ([]queue.Queue, error) {
	rows, err := p.pending.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("pendingqueue: list pending: %w", err)
	}
	now := p.now().UTC()
	nextRss := p.nextRssSync(ctx)

	out := make([]queue.Queue, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		// C# `GetPendingQueue` filters out Fallback-reason rows
		// (PendingReleaseService.cs).
		if row.Reason == int(pendingreleases.ReasonFallback) {
			continue
		}
		item, ok := p.projectRow(ctx, row, now, nextRss)
		if ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// nextRssSync returns the next RSS-sync execution time, or the zero
// time when unavailable (no reader wired / not yet scheduled).
func (p *Projector) nextRssSync(ctx context.Context) time.Time {
	if p.nextRss == nil {
		return time.Time{}
	}
	t, err := p.nextRss.GetNextExecution(ctx, RssSyncTaskTypeName)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func (p *Projector) projectRow(ctx context.Context, row *pendingreleases.Release, now, nextRss time.Time) (queue.Queue, bool) {
	var rel parser.ReleaseInfo
	if err := json.Unmarshal([]byte(row.ReleaseInfo), &rel); err != nil {
		p.log.Warn("pendingqueue: malformed ReleaseInfo; skipping row", "id", row.ID, "err", err)
		return queue.Queue{}, false
	}
	var parsed parser.ParsedEpisodeInfo
	if row.ParsedEpisodeInfo != "" {
		if err := json.Unmarshal([]byte(row.ParsedEpisodeInfo), &parsed); err != nil {
			p.log.Warn("pendingqueue: malformed ParsedEpisodeInfo; using empty", "id", row.ID, "err", err)
		}
	}

	ser := p.lookupSeries(ctx, row.SeriesID)
	eps := p.rehydrateEpisodes(ctx, row.SeriesID, parsed.SeasonNumber, parsed.EpisodeNumbers)

	delay := p.delayMinutes(ctx, ser, rel.DownloadProtocol)
	ect := rel.PublishDate.Add(time.Duration(delay) * time.Minute)
	if !nextRss.IsZero() && ect.Before(nextRss) {
		ect = nextRss
	} else {
		ect = ect.Add(time.Duration(p.cfg.RssSyncInterval()) * time.Minute)
	}
	timeLeft := ect.Sub(now)
	if timeLeft < 0 {
		timeLeft = 0
	}

	season := parsed.SeasonNumber
	added := row.Added
	languages := parsed.Languages
	if len(languages) == 0 {
		languages = []language.Language{language.Unknown}
	}

	q := queue.Queue{
		ID:                      pendingreleases.QueueIDFor(row.ID),
		Series:                  ser,
		SeasonNumber:            &season,
		Episodes:                eps,
		Languages:               languages,
		Quality:                 parsed.Quality,
		Size:                    rel.Size,
		Title:                   row.Title,
		SizeLeft:                rel.Size,
		TimeLeft:                &timeLeft,
		EstimatedCompletionTime: &ect,
		Added:                   &added,
		Status:                  statusForReason(pendingreleases.Reason(row.Reason)),
		StatusMessages:          []string{},
		Protocol:                rel.DownloadProtocol,
		Indexer:                 rel.Indexer,
	}
	if len(eps) == 0 {
		// C# `GetPendingQueue`: a row whose episodes couldn't be matched
		// gets an explanatory error message.
		q.ErrorMessage = "Unable to find matching episode(s)"
	}
	return q, true
}

// lookupSeries resolves the series row, tolerating a not-found (the
// queue row still renders with a nil Series, matching the C# posture
// where a dangling series id yields a row the UI flags).
func (p *Projector) lookupSeries(ctx context.Context, seriesID int) *series.Series {
	if seriesID == 0 {
		return nil
	}
	ser, err := p.series.GetSeries(ctx, seriesID)
	if err != nil {
		if !errors.Is(err, series.ErrNotFound) {
			p.log.Warn("pendingqueue: series lookup failed", "seriesId", seriesID, "err", err)
		}
		return nil
	}
	return ser
}

// rehydrateEpisodes resolves each (season, episodeNumber) into an
// Episode row. Missing episodes are skipped (the C# IncludeRemoteEpisodes
// falls through to an empty list when none match).
func (p *Projector) rehydrateEpisodes(ctx context.Context, seriesID, season int, episodeNumbers []int) []episodes.Episode {
	out := make([]episodes.Episode, 0, len(episodeNumbers))
	for _, en := range episodeNumbers {
		ep, err := p.episodes.FindEpisode(ctx, seriesID, season, en)
		if err != nil {
			if !errors.Is(err, episodes.ErrNotFound) {
				p.log.Warn("pendingqueue: episode lookup failed",
					"seriesId", seriesID, "season", season, "episode", en, "err", err)
			}
			continue
		}
		if ep != nil {
			out = append(out, *ep)
		}
	}
	return out
}

// delayMinutes ports C# `GetDelay` (PendingReleaseService.cs):
// max(profile.GetProtocolDelay(protocol), config.MinimumAge). A
// missing series / delay profile contributes a zero protocol delay, so
// the MinimumAge floor still applies.
func (p *Projector) delayMinutes(ctx context.Context, ser *series.Series, protocol indexers.DownloadProtocol) int {
	protocolDelay := 0
	if ser != nil {
		if d, found, err := p.delay.ProtocolDelay(ctx, ser.Tags, protocol); err == nil && found {
			protocolDelay = d
		}
	}
	minimumAge := p.cfg.MinimumAge()
	if minimumAge > protocolDelay {
		return minimumAge
	}
	return protocolDelay
}

// statusForReason maps a pending-release Reason to the queue status the
// UI renders. Mirrors the C# `Enum.TryParse(Reason.ToString(), out
// QueueStatus)` — the enum names line up (Delay / DownloadClientUnavailable).
// Fallback rows are filtered before projection, so it never reaches here.
func statusForReason(r pendingreleases.Reason) queue.QueueStatus {
	switch r {
	case pendingreleases.ReasonDelay:
		return queue.QueueStatusDelay
	case pendingreleases.ReasonDownloadClientUnavailable:
		return queue.QueueStatusDownloadClientUnavailable
	case pendingreleases.ReasonFallback:
		return queue.QueueStatusFallback
	default:
		return queue.QueueStatusUnknown
	}
}
