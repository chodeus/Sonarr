package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/namingconfig"
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
