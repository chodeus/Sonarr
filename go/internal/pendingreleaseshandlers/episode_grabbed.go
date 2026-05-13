package pendingreleaseshandlers

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// GrabbedRemover narrows [*pendingreleases.Service] to the
// RemoveGrabbed surface this handler needs. Production binding
// satisfies it via Go's structural typing on the existing
// [pendingreleases.Service.RemoveGrabbed] method.
type GrabbedRemover interface {
	RemoveGrabbed(ctx context.Context, in pendingreleases.GrabbedInput) error
}

// HandleEpisodeGrabbed ports C#
// `PendingReleaseService.Handle(EpisodeGrabbedEvent)` at
// `PendingReleaseService.cs:667-671`. Builds a
// [pendingreleases.GrabbedInput] from the event's [decisionengine.Subject]
// (Series.ID + MappedSeasonNumber + Episodes[*].EpisodeNumber +
// ParsedEpisodeInfo.Quality) and dispatches to
// [GrabbedRemover.RemoveGrabbed].
//
// Defensive arms:
//
//   - nil remover → typed error.
//   - nil event / nil Episode (Subject) → no-op.
//   - nil Episode.Series → no-op (can't key the series-scoped sweep).
//   - empty Episodes slice → no-op (no episode numbers to match).
//   - nil ParsedEpisodeInfo → zero quality.Model is used; the
//     [pendingreleases.Service.RemoveGrabbed] quality compare will
//     treat that as "lowest possible" so it preserves higher-quality
//     pending rows. Defensive — production grab events always carry
//     a non-nil ParsedEpisodeInfo per the slice 3.X decisionengine
//     contract.
//
// Slice 4.BZ (sub-arc C7b-ii).
func HandleEpisodeGrabbed(ctx context.Context, r GrabbedRemover, ev *decisionengine.EpisodeGrabbedEvent) error {
	if r == nil {
		return errors.New("pendingreleaseshandlers: HandleEpisodeGrabbed: nil remover")
	}
	if ev == nil || ev.Episode == nil || ev.Episode.Series == nil || len(ev.Episode.Episodes) == 0 {
		return nil
	}
	sub := ev.Episode
	in := pendingreleases.GrabbedInput{
		SeriesID:       sub.Series.ID,
		SeasonNumber:   sub.MappedSeasonNumber,
		EpisodeNumbers: episodeNumbersFrom(sub),
	}
	if sub.ParsedEpisodeInfo != nil {
		in.Quality = sub.ParsedEpisodeInfo.Quality
	}
	if err := r.RemoveGrabbed(ctx, in); err != nil {
		return fmt.Errorf("pendingreleaseshandlers: HandleEpisodeGrabbed: %w", err)
	}
	return nil
}

// episodeNumbersFrom projects the Subject's Episodes slice into the
// EpisodeNumber list pendingreleases.GrabbedInput consumes. Order is
// preserved (caller iterates in arbitrary order anyway — set
// semantics).
func episodeNumbersFrom(sub *decisionengine.Subject) []int {
	if sub == nil {
		return nil
	}
	out := make([]int, 0, len(sub.Episodes))
	for _, e := range sub.Episodes {
		out = append(out, e.EpisodeNumber)
	}
	return out
}
