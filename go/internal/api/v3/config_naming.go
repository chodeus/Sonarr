package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/validation"
)

// NamingConfigResource mirrors
// `src/Sonarr.Api.V3/Config/NamingConfigResource.cs`. Twelve-field
// passthrough — the persisted namingconfig.Config carries the same
// shape with camelCase JSON tags ready for wire emission. The
// resource type exists so future controller-only fields (e.g.
// computed sample-output strings) can land without touching the
// persistence struct.
//
// Field-source mapping (C# → Go): every field is a direct
// passthrough from namingconfig.Config.{ID,MultiEpisodeStyle,...}.
type NamingConfigResource struct {
	ID                           int    `json:"id"`
	MultiEpisodeStyle            int    `json:"multiEpisodeStyle"`
	RenameEpisodes               bool   `json:"renameEpisodes"`
	StandardEpisodeFormat        string `json:"standardEpisodeFormat"`
	DailyEpisodeFormat           string `json:"dailyEpisodeFormat"`
	SeasonFolderFormat           string `json:"seasonFolderFormat"`
	SeriesFolderFormat           string `json:"seriesFolderFormat"`
	AnimeEpisodeFormat           string `json:"animeEpisodeFormat"`
	ReplaceIllegalCharacters     bool   `json:"replaceIllegalCharacters"`
	SpecialsFolderFormat         string `json:"specialsFolderFormat"`
	ColonReplacementFormat       int    `json:"colonReplacementFormat"`
	CustomColonReplacementFormat string `json:"customColonReplacementFormat"`
}

// NamingConfigGetter is the read side the handler depends on.
// *namingconfig.Service.GetConfig satisfies it.
type NamingConfigGetter interface {
	GetConfig(ctx context.Context) (*namingconfig.Config, error)
}

// NamingConfigHandler returns the http.Handler for
// GET /api/v3/config/naming. Mirrors
// `NamingConfigController.GetNamingConfig` —
// the C# RestController<NamingConfigResource> base resolves the
// singleton row via `GetResourceById(1)`, which forwards to the
// service's Get. The Go port collapses the GetResourceById layer
// since this is the only call site.
func NamingConfigHandler(getter NamingConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := getter.GetConfig(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("namingconfig: %w", err), nil)
			return
		}
		res := NamingConfigResource{
			ID:                           cfg.ID,
			MultiEpisodeStyle:            cfg.MultiEpisodeStyle,
			RenameEpisodes:               cfg.RenameEpisodes,
			StandardEpisodeFormat:        cfg.StandardEpisodeFormat,
			DailyEpisodeFormat:           cfg.DailyEpisodeFormat,
			SeasonFolderFormat:           cfg.SeasonFolderFormat,
			SeriesFolderFormat:           cfg.SeriesFolderFormat,
			AnimeEpisodeFormat:           cfg.AnimeEpisodeFormat,
			ReplaceIllegalCharacters:     cfg.ReplaceIllegalCharacters,
			SpecialsFolderFormat:         cfg.SpecialsFolderFormat,
			ColonReplacementFormat:       cfg.ColonReplacementFormat,
			CustomColonReplacementFormat: cfg.CustomColonReplacementFormat,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ---------- 5.B12: PUT + /examples ----------

// NamingExampleResource is the JSON shape produced by
// GET /api/v3/config/naming/examples. Mirrors C# at
// `src/Sonarr.Api.V3/Config/NamingExampleResource.cs`. Every field
// is a string OR null on the wire (null when the corresponding
// format fails validation or is empty); modelled here as plain
// strings with `omitempty` so empty values drop the key the same
// way STJson.WhenWritingNull does.
type NamingExampleResource struct {
	SingleEpisodeExample     string `json:"singleEpisodeExample,omitempty"`
	MultiEpisodeExample      string `json:"multiEpisodeExample,omitempty"`
	DailyEpisodeExample      string `json:"dailyEpisodeExample,omitempty"`
	AnimeEpisodeExample      string `json:"animeEpisodeExample,omitempty"`
	AnimeMultiEpisodeExample string `json:"animeMultiEpisodeExample,omitempty"`
	SeriesFolderExample      string `json:"seriesFolderExample,omitempty"`
	SeasonFolderExample      string `json:"seasonFolderExample,omitempty"`
	SpecialsFolderExample    string `json:"specialsFolderExample,omitempty"`
}

// NamingConfigWriter is the write surface — Save is the singleton
// upsert pattern (the namingconfig table holds one row). *namingconfig.
// Service satisfies it via GetConfig + Save.
type NamingConfigWriter interface {
	NamingConfigGetter
	Save(ctx context.Context, config *namingconfig.Config) error
}

// NamingConfigUpdateHandler returns PUT /api/v3/config/naming/{id}.
// Mirrors `NamingConfigController.UpdateNamingConfig` at
// `src/Sonarr.Api.V3/Config/NamingConfigController.cs:56-65`. The
// path id is ignored (the table has one row); the body's Id wins —
// matches C# `Save(resource.ToModel())` which doesn't consult the
// URL id either. Returns 202 + the persisted body.
//
// Validation chain (subset of C# SharedValidator at :32-39):
//   - MultiEpisodeStyle in [0, 5]
//   - ColonReplacementFormat in [0, 4] (the enum range; 4 = Custom)
//   - When ColonReplacementFormat == 4 (Custom), CustomColonReplacementFormat
//     must be non-empty
//
// DEFERRED: the per-format token validators (ValidEpisodeFormat,
// ValidDailyEpisodeFormat, ValidAnimeEpisodeFormat,
// ValidSeriesFolderFormat, ValidSeasonFolderFormat,
// ValidSpecialsFolderFormat). Each runs the C# filename builder
// against sample data and checks for required token presence + no
// invalid path chars. Porting those needs the IFilenameSampleService
// equivalent which the organizer package hasn't built; follow-up
// slice. Service.Save will still accept invalid formats today; the
// gap surfaces only when the format actually fails to render.
func NamingConfigUpdateHandler(svc NamingConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeNamingConfigBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("namingconfig: malformed body: %v", err))
			return
		}
		if errs := validateNamingConfig(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := &namingconfig.Config{
			ID:                           body.ID,
			MultiEpisodeStyle:            body.MultiEpisodeStyle,
			RenameEpisodes:               body.RenameEpisodes,
			StandardEpisodeFormat:        body.StandardEpisodeFormat,
			DailyEpisodeFormat:           body.DailyEpisodeFormat,
			SeasonFolderFormat:           body.SeasonFolderFormat,
			SeriesFolderFormat:           body.SeriesFolderFormat,
			AnimeEpisodeFormat:           body.AnimeEpisodeFormat,
			ReplaceIllegalCharacters:     body.ReplaceIllegalCharacters,
			SpecialsFolderFormat:         body.SpecialsFolderFormat,
			ColonReplacementFormat:       body.ColonReplacementFormat,
			CustomColonReplacementFormat: body.CustomColonReplacementFormat,
		}
		if err := svc.Save(r.Context(), model); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("namingconfig: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(toNamingConfigResource(model))
		_, _ = w.Write(buf)
	})
}

// NamingConfigExamplesHandler returns GET /api/v3/config/naming/examples.
// Mirrors `NamingConfigController.GetExamples` at `:67-117`. C# binds
// the format strings from the query (`[FromQuery]NamingConfigResource`)
// and renders sample filenames via IFilenameSampleService; when the
// bound `id == 0` it falls back to the persisted config entirely.
//
// The Go port reproduces the IFilenameSampleService fixtures (the
// static Standard/Daily/Anime series + episodes + files from
// `FileNameSampleService.cs:44-208`) and renders each example through
// the ported organizer.BuildFileName / GetSeriesFolder / GetSeasonFolder.
// Folder examples are omitted (null) when their format is blank,
// matching C# `IsNullOrWhiteSpace ? null : ...`.
//
// Two documented divergences from C# (both minor, render best-effort
// rather than null):
//   - The FileNameValidationService gate (which nulls an example whose
//     rendered name fails to round-trip through the episode parser) is
//     not ported — a malformed format renders a best-effort string
//     instead of null. The empty-on-NamingFormatException case is still
//     matched (BuildFileName's error → "").
//   - MediaInfo tokens render empty (BuildOptions.MediaInfo left nil):
//     the default formats carry no {MediaInfo ...} tokens, so this only
//     affects a custom format that references them.
func NamingConfigExamplesHandler(svc NamingConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := parseNamingExampleQuery(r)
		// C#: when the bound resource's Id is 0, replace it wholesale
		// with the persisted config (the query format strings are
		// ignored in that case).
		if res.ID == 0 {
			cfg, err := svc.GetConfig(r.Context())
			if err != nil || cfg == nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("namingconfig: load persisted: %w", err), nil)
				return
			}
			res = toNamingConfigResource(cfg)
		}
		out := renderNamingExamples(namingExampleBuildOptions(&res))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// parseNamingExampleQuery binds the NamingConfigResource fields the
// renderer consults from the query string, mirroring ASP.NET's
// `[FromQuery]NamingConfigResource` binding. Parsing is lenient —
// empty/invalid numeric or bool params default to their zero value
// rather than 400, since the examples endpoint is a best-effort
// preview.
func parseNamingExampleQuery(r *http.Request) NamingConfigResource {
	q := r.URL.Query()
	atoi := func(key string) int { n, _ := strconv.Atoi(q.Get(key)); return n }
	abool := func(key string) bool { b, _ := strconv.ParseBool(q.Get(key)); return b }
	return NamingConfigResource{
		ID:                           atoi("id"),
		MultiEpisodeStyle:            atoi("multiEpisodeStyle"),
		RenameEpisodes:               abool("renameEpisodes"),
		StandardEpisodeFormat:        q.Get("standardEpisodeFormat"),
		DailyEpisodeFormat:           q.Get("dailyEpisodeFormat"),
		SeasonFolderFormat:           q.Get("seasonFolderFormat"),
		SeriesFolderFormat:           q.Get("seriesFolderFormat"),
		AnimeEpisodeFormat:           q.Get("animeEpisodeFormat"),
		ReplaceIllegalCharacters:     abool("replaceIllegalCharacters"),
		SpecialsFolderFormat:         q.Get("specialsFolderFormat"),
		ColonReplacementFormat:       atoi("colonReplacementFormat"),
		CustomColonReplacementFormat: q.Get("customColonReplacementFormat"),
	}
}

// namingExampleBuildOptions maps a NamingConfigResource to the
// organizer.BuildOptions the renderer feeds to BuildFileName — the
// same mapping the production nameBuilderAdapter.buildOptions uses
// (cmd/sonarr/downloadedimport_wiring.go). MediaInfo / QualityResolver
// / CustomFormats are left nil (see handler doc).
func namingExampleBuildOptions(res *NamingConfigResource) organizer.BuildOptions {
	return organizer.BuildOptions{
		RenameEpisodes:               res.RenameEpisodes,
		StandardEpisodeFormat:        res.StandardEpisodeFormat,
		DailyEpisodeFormat:           res.DailyEpisodeFormat,
		AnimeEpisodeFormat:           res.AnimeEpisodeFormat,
		SeriesFolderFormat:           res.SeriesFolderFormat,
		SeasonFolderFormat:           res.SeasonFolderFormat,
		SpecialsFolderFormat:         res.SpecialsFolderFormat,
		MultiEpisodeStyle:            organizer.MultiEpisodeStyle(res.MultiEpisodeStyle),
		ReplaceIllegalCharacters:     res.ReplaceIllegalCharacters,
		ColonReplacementFormat:       organizer.ColonReplacementFormat(res.ColonReplacementFormat),
		CustomColonReplacementFormat: res.CustomColonReplacementFormat,
	}
}

// renderNamingExamples reproduces the eight IFilenameSampleService
// outputs against the static fixtures. Each episode example renders
// via organizer.BuildFileName with extension "" (matching C#
// BuildSample); folder examples use GetSeriesFolder / GetSeasonFolder
// and are emitted only when their format is non-blank.
func renderNamingExamples(opts organizer.BuildOptions) NamingExampleResource {
	std := sampleSeries(series.SeriesTypeStandard)
	daily := sampleSeries(series.SeriesTypeDaily)
	anime := sampleSeries(series.SeriesTypeAnime)
	single := sampleSingleEpisode()
	multi := sampleMultiEpisodes()

	var res NamingExampleResource
	res.SingleEpisodeExample = renderEpisodeExample(single, std, sampleFile("The.Series.Title's!.S01E01.1080p.WEBDL.x264-EVOLVE"), opts)
	res.MultiEpisodeExample = renderEpisodeExample(multi, std, sampleFile("The.Series.Title's!.S01E01-E03.1080p.WEBDL.x264-EVOLVE"), opts)
	res.DailyEpisodeExample = renderEpisodeExample(single, daily, sampleFile("The.Series.Title's!.2013.10.30.1080p.WEBDL.x264-EVOLVE"), opts)
	res.AnimeEpisodeExample = renderEpisodeExample(single, anime, sampleFile("[RlsGroup] The Series Title's! - 001 [1080P]"), opts)
	res.AnimeMultiEpisodeExample = renderEpisodeExample(multi, anime, sampleFile("[RlsGroup] The Series Title's! - 001 - 103 [1080p]"), opts)

	folderOpts := opts.FolderOptions()
	if strings.TrimSpace(opts.SeriesFolderFormat) != "" {
		res.SeriesFolderExample = organizer.GetSeriesFolder(std, folderOpts)
	}
	if strings.TrimSpace(opts.SeasonFolderFormat) != "" {
		res.SeasonFolderExample = organizer.GetSeasonFolder(std, 1, folderOpts)
	}
	if strings.TrimSpace(opts.SpecialsFolderFormat) != "" {
		res.SpecialsFolderExample = organizer.GetSeasonFolder(std, 0, folderOpts)
	}
	return res
}

// renderEpisodeExample renders one episode-filename sample. A
// NamingFormatException-equivalent (BuildFileName error) collapses to
// "" — matching C# BuildSample's catch.
func renderEpisodeExample(eps []episodes.Episode, s *series.Series, file *episodefiles.File, opts organizer.BuildOptions) string {
	name, err := organizer.BuildFileName(eps, s, file, "", opts)
	if err != nil {
		return ""
	}
	return name
}

// sampleSeries reproduces the static FileNameSampleService series
// fixture (`FileNameSampleService.cs:46-77`) for the given type.
func sampleSeries(seriesType int) *series.Series {
	return &series.Series{
		SeriesType: seriesType,
		Title:      "The Series Title's!",
		Year:       2010,
		ImdbID:     "tt12345",
		TvdbID:     12345,
		TvMazeID:   54321,
		TmdbID:     11223,
	}
}

// sampleSingleEpisode / sampleMultiEpisodes reproduce the static
// episode fixtures (`FileNameSampleService.cs:79-108`).
func sampleSingleEpisode() []episodes.Episode {
	return []episodes.Episode{sampleEpisode(1, "Episode Title (1)", "2013-10-30")}
}

func sampleMultiEpisodes() []episodes.Episode {
	return []episodes.Episode{
		sampleEpisode(1, "Episode Title (1)", "2013-10-30"),
		sampleEpisode(2, "Episode Title (2)", ""),
		sampleEpisode(3, "Episode Title (3)", ""),
	}
}

func sampleEpisode(n int, title, airDate string) episodes.Episode {
	abs := n
	return episodes.Episode{
		SeasonNumber:          1,
		EpisodeNumber:         n,
		Title:                 title,
		AirDate:               airDate,
		AbsoluteEpisodeNumber: &abs,
	}
}

// sampleFile reproduces an EpisodeFile fixture with the given scene
// name. Quality is WEBDL-1080p v2; ReleaseGroup "RlsGrp" — matching
// `FileNameSampleService.cs:160-208`. RelativePath = sceneName + .mkv.
func sampleFile(sceneName string) *episodefiles.File {
	return &episodefiles.File{
		Quality:      sampleQualityJSON,
		SceneName:    sceneName,
		ReleaseGroup: "RlsGrp",
		RelativePath: sceneName + ".mkv",
	}
}

// sampleQualityJSON is the persisted-model JSON for WEBDL-1080p v2,
// matching the fixtures' `new QualityModel(Quality.WEBDL1080p, new
// Revision(2))`.
var sampleQualityJSON = func() string {
	b, _ := json.Marshal(quality.NewModelWithRevision(quality.WEBDL1080p, quality.Revision{Version: 2}))
	return string(b)
}()

// ---------- helpers ----------

func decodeNamingConfigBody(r *http.Request) (*NamingConfigResource, error) {
	var t NamingConfigResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateNamingConfig ports the cheap subset of the C#
// SharedValidator chain (range + conditional checks); the per-format
// token validators are deferred (see NamingConfigUpdateHandler).
func validateNamingConfig(res *NamingConfigResource) *validation.Errors {
	errs := validation.New()
	errs.Range("MultiEpisodeStyle", res.MultiEpisodeStyle, 0, 5)
	// ColonReplacementFormat enum values (per C# ColonReplacementFormat):
	// 0 = Delete, 1 = Dash, 2 = SpaceDash, 3 = SpaceDashSpace, 4 = Custom.
	// Anything else is invalid.
	errs.Range("ColonReplacementFormat", res.ColonReplacementFormat, 0, 4)
	if res.ColonReplacementFormat == 4 && res.CustomColonReplacementFormat == "" {
		errs.Add("CustomColonReplacementFormat", "Custom Colon Replacement Format must be provided when ColonReplacementFormat is Custom")
	}
	return errs
}

func toNamingConfigResource(c *namingconfig.Config) NamingConfigResource {
	return NamingConfigResource{
		ID:                           c.ID,
		MultiEpisodeStyle:            c.MultiEpisodeStyle,
		RenameEpisodes:               c.RenameEpisodes,
		StandardEpisodeFormat:        c.StandardEpisodeFormat,
		DailyEpisodeFormat:           c.DailyEpisodeFormat,
		SeasonFolderFormat:           c.SeasonFolderFormat,
		SeriesFolderFormat:           c.SeriesFolderFormat,
		AnimeEpisodeFormat:           c.AnimeEpisodeFormat,
		ReplaceIllegalCharacters:     c.ReplaceIllegalCharacters,
		SpecialsFolderFormat:         c.SpecialsFolderFormat,
		ColonReplacementFormat:       c.ColonReplacementFormat,
		CustomColonReplacementFormat: c.CustomColonReplacementFormat,
	}
}

// errorsNamingConfig — unused right now but reserved; the
// per-format validator slice may want to wrap the underlying
// ValidationException via this helper.
var _ = errors.New
