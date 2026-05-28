// Boot-time adapters for the J354 episodeimport.UpgradeSpecification
// production wiring. The spec was ported + unit-tested in
// `internal/episodeimport/specs.go` (slice-5 era) but its `formats` /
// profile / file deps were declared as narrow interfaces whose real
// production bindings hadn't been composed; this file's three adapters
// fill those slots so the spec can join the production
// `importDecisionSpecs` list.
//
// Same posture as `decisionengine_wiring.go` / `format_calculator_wiring.go`:
// an adapter where the real service exists but its surface differs from
// what the consumer's narrow interface needs (series-keyed profile
// resolution, ErrNotFound→nil translation, internal series resolution
// for the by-file CustomFormat dispatch).

package main

import (
	"context"
	"errors"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// upgradeProfileViewBySeriesAdapter wraps *series.Service +
// *qualityprofiles.Service to satisfy
// [episodeimport.UpgradeQualityProfileResolver]. Mirrors the C#
// `Episode.Series.QualityProfile.Value` lazy-load at
// `Specifications/UpgradeSpecification.cs:30` — the Go-port Series
// carries only QualityProfileID, so the adapter resolves the series
// row, then the profile row, then parses it into the
// [decisionengine.ProfileView] the spec compares against.
//
// Errors propagate verbatim — the UpgradeSpecification translates a
// non-nil error into a defensive Accept + Warn-log (so a transient DB
// outage doesn't bounce every candidate file in a batch).
type upgradeProfileViewBySeriesAdapter struct {
	series *series.Service
	qp     *qualityprofiles.Service
}

// GetProfileViewBySeriesID resolves seriesID → QualityProfileID →
// parsed ProfileView.
func (a upgradeProfileViewBySeriesAdapter) GetProfileViewBySeriesID(ctx context.Context, seriesID int) (*decisionengine.ProfileView, error) {
	ser, err := a.series.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if ser == nil {
		return nil, errors.New("upgradeProfileViewBySeriesAdapter: nil series")
	}
	profile, err := a.qp.Get(ctx, ser.QualityProfileID)
	if err != nil {
		return nil, err
	}
	return decisionengine.NewProfileView(profile)
}

// upgradeEpisodeFileResolverAdapter wraps *episodefiles.Service to
// satisfy [episodeimport.EpisodeFileResolver]. Mirrors the C#
// `Episode.EpisodeFile.Value` lazy-load at
// `Specifications/UpgradeSpecification.cs:32`.
//
// The spec's narrow interface contract is "nil result + nil error means
// row-not-found" (the `episodeFile == null` continue-arm at C# `:34-38`),
// so this adapter translates [episodefiles.ErrNotFound] into `(nil, nil)`.
// Any other error propagates and the spec accepts defensively.
type upgradeEpisodeFileResolverAdapter struct {
	svc *episodefiles.Service
}

// GetByID forwards to Service.GetFile, mapping the not-found sentinel
// to the nil-row contract the spec expects.
func (a upgradeEpisodeFileResolverAdapter) GetByID(ctx context.Context, id int) (*episodefiles.File, error) {
	f, err := a.svc.GetFile(ctx, id)
	if err != nil {
		if errors.Is(err, episodefiles.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// upgradeFormatCalculatorAdapter wraps *series.Service +
// *customformats.Service to satisfy
// [episodeimport.CustomFormatCalculator] — the by-EpisodeFile
// CustomFormat dispatch the spec calls at C#
// `Specifications/UpgradeSpecification.cs:62`
// (`_formatService.ParseCustomFormat(episodeFile, series)`).
//
// The episodeimport narrow interface drops the series argument the
// underlying [customformats.ParseCustomFormatForFile] needs, so the
// adapter resolves the series from the file's SeriesID internally. The
// full Format catalogue is fetched per call (via Service.All) so a
// profile edit lands without a restart; the per-Service cache keeps the
// fetch cheap. The spec scores the returned formats against the
// ProfileView itself — this adapter only supplies the matched list.
//
// A series-resolution or catalogue-fetch error propagates; the spec
// accepts defensively on a non-nil error.
type upgradeFormatCalculatorAdapter struct {
	series *series.Service
	cf     *customformats.Service
}

// ParseCustomFormat resolves the file's series, fetches the Format
// catalogue, and returns the matched formats for the file.
func (a upgradeFormatCalculatorAdapter) ParseCustomFormat(ctx context.Context, file *episodefiles.File) ([]customformats.CustomFormat, error) {
	if file == nil {
		return nil, errors.New("upgradeFormatCalculatorAdapter: nil file")
	}
	ser, err := a.series.GetSeries(ctx, file.SeriesID)
	if err != nil {
		return nil, err
	}
	all, err := a.cf.All(ctx)
	if err != nil {
		return nil, err
	}
	return customformats.ParseCustomFormatForFile(file, ser, all), nil
}

// Compile-time guards: each adapter satisfies its narrow interface.
var (
	_ episodeimport.UpgradeQualityProfileResolver = upgradeProfileViewBySeriesAdapter{}
	_ episodeimport.EpisodeFileResolver           = upgradeEpisodeFileResolverAdapter{}
	_ episodeimport.CustomFormatCalculator        = upgradeFormatCalculatorAdapter{}
)
