// Boot-time adapters for the slice-3.AN
// *episodeimport.LocalEpisodeFormatCalculator wiring. Three narrow
// interfaces — LocalEpisodeFileNameBuilder / LocalEpisodeFormatParser /
// LocalEpisodeFormatScorer — each get a thin adapter wrapping the
// already-constructed production services
// (*namingconfig.Service / *customformats.Service / *qualityprofiles.Service).
//
// Slice 3.AN closes queue [7] sub-arc 7a — the calculator was already
// ported (slice 3.62 era) but its `formats` slot in
// [episodeimport.NewImportDecisionMaker] was nil. This file's
// adapters fill that slot.
//
// Same posture as `decisionengine_wiring.go` /
// `downloadedimport_wiring.go`: an adapter where the real service
// exists but its surface differs from what the consumer needs.

package main

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// localEpisodeFileNameBuilderAdapter wraps *namingconfig.Service +
// the package-level [organizer.BuildFileName] free fn to satisfy
// [episodeimport.LocalEpisodeFileNameBuilder]. Mirrors C#
// `IBuildFileNames.BuildFileName(List<Episode>, Series, EpisodeFile)`
// at `LocalEpisodeCustomFormatCalculationService.cs:28,34`.
//
// The adapter loads the active NamingConfig row per call (cached by
// the slice-3.A2 namingconfig.Service ConfigSavedEvent invalidation
// pattern; per-call cost is the cache lookup, not a DB hit) and
// translates the writer-relevant fields into a [organizer.BuildOptions]
// before forwarding. The pattern mirrors the slice-3.96
// nameBuilderAdapter.
//
// Errors from GetConfig surface as a logged-warning + a typed error
// return — the calculator's UpdateEpisodeCustomFormats arm logs and
// falls through to filepath.Base for the original-name pass, so a
// transient namingconfig DB outage doesn't drop the whole CustomFormat
// resolution flow.
type localEpisodeFileNameBuilderAdapter struct {
	svc *namingconfig.Service
	log *slog.Logger
}

// BuildEpisodeFileName forwards through *LocalEpisode.ToEpisodeFile()
// + organizer.BuildFileName. nil le returns an error rather than ""
// so the calculator's defensive arm fires.
func (a localEpisodeFileNameBuilderAdapter) BuildEpisodeFileName(le *episodeimport.LocalEpisode) (string, error) {
	if le == nil {
		return "", errors.New("localEpisodeFileNameBuilderAdapter: nil LocalEpisode")
	}
	cfg, err := a.svc.GetConfig(context.Background())
	if err != nil {
		a.log.Warn("localEpisodeFileNameBuilder: GetConfig failed", "err", err)
		return "", err
	}
	if cfg == nil {
		return "", errors.New("localEpisodeFileNameBuilderAdapter: nil NamingConfig")
	}
	opts := organizer.BuildOptions{
		RenameEpisodes:               cfg.RenameEpisodes,
		StandardEpisodeFormat:        cfg.StandardEpisodeFormat,
		DailyEpisodeFormat:           cfg.DailyEpisodeFormat,
		AnimeEpisodeFormat:           cfg.AnimeEpisodeFormat,
		SeriesFolderFormat:           cfg.SeriesFolderFormat,
		SeasonFolderFormat:           cfg.SeasonFolderFormat,
		SpecialsFolderFormat:         cfg.SpecialsFolderFormat,
		MultiEpisodeStyle:            organizer.MultiEpisodeStyle(cfg.MultiEpisodeStyle),
		ColonReplacementFormat:       organizer.ColonReplacementFormat(cfg.ColonReplacementFormat),
		ReplaceIllegalCharacters:     cfg.ReplaceIllegalCharacters,
		CustomColonReplacementFormat: cfg.CustomColonReplacementFormat,
	}
	return organizer.BuildFileName(le.Episodes, le.Series, le.ToEpisodeFile(), "", opts)
}

// localEpisodeFormatParserAdapter wraps *customformats.Service to
// satisfy [episodeimport.LocalEpisodeFormatParser]. Mirrors C#
// `ICustomFormatCalculationService.ParseCustomFormat(LocalEpisode, fileName)`
// at `CustomFormatCalculationService.cs:117-140`.
//
// Algorithm:
//
//  1. Build a [parser.ParsedEpisodeInfo] from le's already-aggregated
//     fields (SceneName-or-fileName for ReleaseTitle).
//  2. Bundle into a [customformats.CustomFormatInput].
//  3. Walk every persisted Format via Service.All; the per-Service
//     cache means the All() walk is goroutine-safe + cheap.
//  4. customformats.Calculate runs the matching loop; matches are
//     converted back to the persisted-row form for the
//     LocalEpisode.CustomFormats slice.
//
// Errors from Service.All / Format decode surface as logged warnings +
// nil return — matches the C# tree's overall posture (the calculator
// is best-effort enrichment; a transient DB blip shouldn't reject
// the candidate).
type localEpisodeFormatParserAdapter struct {
	svc *customformats.Service
	log *slog.Logger
}

// ParseCustomFormat builds the CustomFormatInput, walks
// customformats.Calculate, and converts matched typed Formats back
// to []customformats.CustomFormat (the persisted row shape).
func (a localEpisodeFormatParserAdapter) ParseCustomFormat(le *episodeimport.LocalEpisode, fileName string) []customformats.CustomFormat {
	if le == nil || le.Series == nil {
		return nil
	}
	formats, err := a.svc.All(context.Background())
	if err != nil {
		a.log.Warn("localEpisodeFormatParser: customformats.All failed; returning empty", "err", err)
		return nil
	}
	releaseTitle := le.SceneName
	if releaseTitle == "" {
		releaseTitle = fileName
	}
	// Note: parser.ParsedEpisodeInfo doesn't carry a ReleaseType field
	// (the C# class does — see ParsedEpisodeInfo.cs:25). The Go port
	// surfaces ReleaseType only on CustomFormatInput / LocalEpisode,
	// matching the per-call shape. The C# `ICustomFormatCalculationService`
	// also writes it onto CustomFormatInput separately, so the Go port
	// is byte-for-byte equivalent at the input the spec actually reads.
	episodeInfo := &parser.ParsedEpisodeInfo{
		SeriesTitle:  le.Series.Title,
		ReleaseTitle: releaseTitle,
		Quality:      le.Quality,
		Languages:    cloneLanguages(le.Languages),
		ReleaseGroup: le.ReleaseGroup,
	}
	input := customformats.CustomFormatInput{
		EpisodeInfo:  episodeInfo,
		Series:       le.Series,
		Size:         le.Size,
		IndexerFlags: le.IndexerFlags,
		Languages:    cloneLanguages(le.Languages),
		Filename:     fileName,
		ReleaseType:  le.ReleaseType,
	}
	matched := customformats.Calculate(input, formats)
	out := make([]customformats.CustomFormat, 0, len(matched))
	for _, f := range matched {
		if f == nil {
			continue
		}
		out = append(out, customformats.CustomFormat{
			ID:                              f.ID,
			Name:                            f.Name,
			IncludeCustomFormatWhenRenaming: f.IncludeCustomFormatWhenRenaming,
		})
	}
	return out
}

// cloneLanguages returns a defensive copy so the CustomFormatInput
// doesn't share the LocalEpisode's slice. The Calculator may run twice
// (renamed-name + original-name pass) — the C# tree creates fresh
// inputs each call; matching that posture avoids accidental mutation.
func cloneLanguages(in []language.Language) []language.Language {
	if len(in) == 0 {
		return nil
	}
	out := make([]language.Language, len(in))
	copy(out, in)
	return out
}

// localEpisodeFormatScorerAdapter wraps *series.Service +
// *qualityprofiles.Service to satisfy
// [episodeimport.LocalEpisodeFormatScorer]. Mirrors C#
// `localEpisode.Series.QualityProfile.Value.CalculateCustomFormatScore(formats) ?? 0`
// at `LocalEpisodeCustomFormatCalculationService.cs:37,40`.
//
// Algorithm:
//
//  1. Series.GetSeries(seriesID) → *series.Series.
//  2. Series.QualityProfileID → *qualityprofiles.Profile via Service.Get.
//  3. Build a presence-set {format.ID: true} from the matched
//     formats slice.
//  4. [qualityprofiles.CalculateCustomFormatScore] sums the per-Item
//     scores for every FormatItem whose ID is in the set.
//
// Any error along the chain returns 0 — matches the C# `?? 0` arm
// when QualityProfile is unloaded / null.
type localEpisodeFormatScorerAdapter struct {
	series *series.Service
	qp     *qualityprofiles.Service
	log    *slog.Logger
}

// CalculateCustomFormatScore returns the score for `formats` against
// the series' QualityProfile, or 0 on any lookup error.
func (a localEpisodeFormatScorerAdapter) CalculateCustomFormatScore(seriesID int, formats []customformats.CustomFormat) int {
	if len(formats) == 0 {
		return 0
	}
	ctx := context.Background()
	ser, err := a.series.GetSeries(ctx, seriesID)
	if err != nil || ser == nil {
		a.log.Debug("localEpisodeFormatScorer: GetSeries failed; returning 0",
			"seriesID", seriesID, "err", err)
		return 0
	}
	if ser.QualityProfileID == 0 {
		// No profile configured — matches C# `?? 0` arm.
		return 0
	}
	profile, err := a.qp.Get(ctx, ser.QualityProfileID)
	if err != nil || profile == nil {
		a.log.Debug("localEpisodeFormatScorer: QualityProfile lookup failed; returning 0",
			"seriesID", seriesID, "profileID", ser.QualityProfileID, "err", err)
		return 0
	}
	items, err := profile.ParsedFormatItems()
	if err != nil {
		a.log.Debug("localEpisodeFormatScorer: ParsedFormatItems failed; returning 0",
			"seriesID", seriesID, "err", err)
		return 0
	}
	present := make(map[int]bool, len(formats))
	for _, f := range formats {
		present[f.ID] = true
	}
	return qualityprofiles.CalculateCustomFormatScore(items, present)
}

// Compile-time guards: each adapter satisfies its narrow interface.
// A signature drift on either side breaks the build at this site.
var (
	_ episodeimport.LocalEpisodeFileNameBuilder = localEpisodeFileNameBuilderAdapter{}
	_ episodeimport.LocalEpisodeFormatParser    = localEpisodeFormatParserAdapter{}
	_ episodeimport.LocalEpisodeFormatScorer    = localEpisodeFormatScorerAdapter{}
)
