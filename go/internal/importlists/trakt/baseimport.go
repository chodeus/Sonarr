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
	logger     *slog.Logger
	now        func() time.Time
	settingsMu sync.Mutex
}

// newBaseImport constructs the common scaffolding from a definition +
// dependency bundle.
func newBaseImport(def *importlists.ImportList, deps importlists.Dependencies) *baseImport {
	doer, _ := deps.HTTPClient.(httpDoer)
	status, _ := deps.StatusService.(statusRecorder)
	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}
	return &baseImport{
		def:    def,
		doer:   doer,
		status: status,
		logger: logger,
		now:    time.Now,
	}
}

// refreshIfNeeded refreshes the access token when Expires is within
// the 5-minute renewal window. Mirrors C# `TraktImportBase.Fetch`
// refresh branch + `RefreshToken` body. Caller MUST hold settingsMu.
//
// settings is a pointer to the variant's embedded SettingsBase to
// allow in-place updates of AccessToken/Expires/RefreshToken.
func (b *baseImport) refreshIfNeeded(ctx context.Context, settings *SettingsBase) error {
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
	return nil
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
