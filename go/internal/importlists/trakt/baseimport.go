package trakt

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// statusRecorder is the narrow surface each variant needs from the
// orchestrator's importliststatus.Service.
type statusRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// settingsPersister is the narrow surface each variant needs from the
// importlists.Repo. *importlists.Repo satisfies it via its embedded
// repo.Repository[ImportList].Update method.
type settingsPersister interface {
	Update(ctx context.Context, def *importlists.ImportList) error
}

// refreshSkew is the renewal window — C# `TraktImportBase.Fetch:43`
// refreshes when Expires < UtcNow.AddMinutes(5).
const refreshSkew = 5 * time.Minute

// baseImport holds the fields + helpers common to all three Trakt
// import variants. Each variant embeds *baseImport + adds its own
// settings struct + endpoint composer.
type baseImport struct {
	def        *importlists.ImportList
	doer       httpDoer
	status     statusRecorder
	persister  settingsPersister
	logger     *slog.Logger
	now        func() time.Time
	settingsMu sync.Mutex
}

// newBaseImport constructs the common scaffolding from a definition +
// dependency bundle.
func newBaseImport(def *importlists.ImportList, deps importlists.Dependencies) *baseImport {
	doer, _ := deps.HTTPClient.(httpDoer)
	status, _ := deps.StatusService.(statusRecorder)
	persister, _ := deps.ImportListsRepo.(settingsPersister)
	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}
	return &baseImport{
		def:       def,
		doer:      doer,
		status:    status,
		persister: persister,
		logger:    logger,
		now:       time.Now,
	}
}

// refreshIfNeeded refreshes the access token when Expires is within
// the 5-minute renewal window. Mirrors C# `TraktImportBase.Fetch`
// refresh branch + `RefreshToken` body. After a successful refresh
// the new triple is round-tripped back to the DB via the persister
// (Dependencies.ImportListsRepo) using marshalSettings — mirrors C#
// `_importListRepository.UpdateSettings(...)` at
// `TraktImportBase.cs:143`.
//
// marshalSettings is supplied by the variant since the wrapping
// settings type carries variant-specific fields the base doesn't
// know about (UserSettings.Username, PopularSettings.Rating, ...).
// A nil persister, row-id == 0, or marshal failure all short-circuit
// the persistence step (mirrors C# `Definition.Id > 0` guard).
//
// Caller MUST hold settingsMu. settings is a pointer to the
// variant's embedded SettingsBase to allow in-place updates.
func (b *baseImport) refreshIfNeeded(ctx context.Context, settings *SettingsBase, marshalSettings func() ([]byte, error)) error {
	if settings.RefreshToken == "" {
		return nil
	}
	if settings.Expires.After(b.now().Add(refreshSkew)) {
		return nil
	}
	tok, err := refreshAuthToken(ctx, b.doer, settings.RefreshToken)
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	settings.AccessToken = tok.AccessToken
	settings.Expires = b.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.RefreshToken != "" {
		settings.RefreshToken = tok.RefreshToken
	}
	b.persistSettingsLocked(ctx, marshalSettings)
	return nil
}

// persistSettingsLocked round-trips the variant's typed Settings back
// to the DB via the persister. Marshal failures + persister errors
// are intentionally swallowed (mirrors C# fire-and-forget
// UpdateSettings) — the next refresh attempt will retry.
//
// Caller MUST hold settingsMu.
func (b *baseImport) persistSettingsLocked(ctx context.Context, marshalSettings func() ([]byte, error)) {
	if b.persister == nil || b.def == nil || b.def.ID == 0 || marshalSettings == nil {
		return
	}
	raw, err := marshalSettings()
	if err != nil {
		return
	}
	s := string(raw)
	b.def.Settings = &s
	_ = b.persister.Update(ctx, b.def)
}

// recordSuccess fires the status recorder when wired.
func (b *baseImport) recordSuccess(ctx context.Context) {
	if b.status == nil || b.def == nil || b.def.ID == 0 {
		return
	}
	_ = b.status.RecordSuccess(ctx, b.def.ID)
}

// recordFailure fires the status recorder with zero back-off.
func (b *baseImport) recordFailure(ctx context.Context) {
	if b.status == nil || b.def == nil || b.def.ID == 0 {
		return
	}
	_ = b.status.RecordFailure(ctx, b.def.ID, 0)
}

// definitionID returns def.ID safely with nil-tolerance.
func (b *baseImport) definitionID() int {
	if b.def == nil {
		return 0
	}
	return b.def.ID
}

// definitionName returns def.Name safely with nil-tolerance.
func (b *baseImport) definitionName() string {
	if b.def == nil {
		return ""
	}
	return b.def.Name
}

// itemFromShow converts a Trakt seriesResource to an
// [importlists.ItemInfo], stamping ImportListID/ImportList from the
// definition.
func (b *baseImport) itemFromShow(show *seriesResource) importlists.ItemInfo {
	info := importlists.ItemInfo{
		ImportListID: b.definitionID(),
		ImportList:   b.definitionName(),
		Title:        show.Title,
		ImdbID:       show.IDs.Imdb,
	}
	if show.IDs.Tvdb != nil {
		info.TvdbID = *show.IDs.Tvdb
	}
	if show.IDs.Tmdb != nil {
		info.TmdbID = *show.IDs.Tmdb
	}
	if show.Year != nil {
		info.Year = *show.Year
	}
	return info
}
