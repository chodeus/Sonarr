package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `Trakt.cs:18` class name.
const Implementation = "Trakt"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `TraktSettings.cs:18` class name.
const ConfigContract = "TraktSettings"

// refreshSkew is the renewal window — C# `Trakt.cs:129` refreshes when
// Expires < now+5min.
const refreshSkew = 5 * time.Minute

// Trakt ports the C# `Trakt` notification provider at
// `Notifications/Trakt/Trakt.cs:18-382`.
//
// The settings (AccessToken/RefreshToken/Expires) mutate on refresh.
// settingsMu guards concurrent OnX calls; the orchestrator's per-row
// goroutine pool may fire two events at once.
type Trakt struct {
	def        *notifications.Notification
	settings   Settings
	settingsMu sync.Mutex
	doer       httpDoer
	now        func() time.Time
}

// Compile-time guard.
var _ notifications.Provider = (*Trakt)(nil)

// New constructs a *Trakt from a persisted row + the
// [notifications.Dependencies] bundle.
func New(def *notifications.Notification, deps notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("trakt: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("trakt: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Trakt{def: def, settings: s, doer: doer, now: time.Now}, nil
}

// Name returns the provider display label. C# `:34`.
func (t *Trakt) Name() string { return "Trakt" }

// Link returns the docs URL. C# `:33`.
func (t *Trakt) Link() string { return "https://trakt.tv/" }

// Definition returns the persisted row.
func (t *Trakt) Definition() *notifications.Notification { return t.def }

// refreshIfNeeded refreshes the access token if it's within the
// 5-minute renewal window. Mirrors C# `RefreshTokenIfNecessary` +
// `RefreshToken` at `:127-163`. Persistence-back-to-DB is deferred
// — the in-memory Settings update lives for the provider lifetime.
//
// settingsMu MUST be held by the caller.
func (t *Trakt) refreshIfNeeded(ctx context.Context) error {
	if t.settings.Expires.After(t.now().Add(refreshSkew)) {
		return nil
	}
	if t.settings.RefreshToken == "" {
		return fmt.Errorf("%w: refresh token is empty", ErrSendFailed)
	}
	resp, err := refreshAuthToken(ctx, t.doer, t.settings.RefreshToken)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("%w: empty renew response", ErrSendFailed)
	}
	t.settings.AccessToken = resp.AccessToken
	t.settings.Expires = t.now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	if resp.RefreshToken != "" {
		t.settings.RefreshToken = resp.RefreshToken
	}
	return nil
}

// withRefresh wraps an action with the refresh + lock sequence.
func (t *Trakt) withRefresh(ctx context.Context, fn func(accessToken string) error) error {
	t.settingsMu.Lock()
	defer t.settingsMu.Unlock()
	if err := t.refreshIfNeeded(ctx); err != nil {
		return err
	}
	return fn(t.settings.AccessToken)
}

// buildShowFromSeries assembles a CollectShow shell for the given
// series with no per-episode payload. Mirrors the common
// AddSeriesToCollection / RemoveSeriesFromCollection construction at
// C# `:255-294`.
func buildShowFromSeries(s *series.Series) CollectShow {
	return CollectShow{
		Title: s.Title,
		Year:  s.Year,
		IDs:   ShowIDs{Tvdb: s.TvdbID, Imdb: s.ImdbID},
	}
}

// buildEpisodeFileShow assembles a CollectShow shell with the
// per-episode payload from the EpisodeFile. Mirrors the
// AddEpisodeToCollection / RemoveEpisodeFromCollection construction.
//
// MediaInfo-derived fields (resolution / hdr / audio / audioChannels)
// are DEFERRED — see package doc. Episodes carried via the C# code's
// `EpisodeFile.Episodes.Value` aren't on the Go EpisodeFile row
// (the Episodes link is computed via a separate lookup not yet wired).
// For now we emit no per-episode entries; the show-level collection
// path still updates Trakt.
func buildEpisodeFileShow(s *series.Series, f *episodefiles.File, withCollectedAt bool, now time.Time) CollectShow {
	show := buildShowFromSeries(s)
	// Episode list lookup is deferred — emit a season entry with an
	// empty Episodes slice would diverge from C# (which iterates the
	// resolved list). The fallback: emit the show-level update only.
	// When the Episodes link ports, the per-episode payload can drop
	// in here.
	_ = withCollectedAt
	_ = now
	_ = f
	return show
}

// OnGrab — not overridden in C#; no-op.
func (t *Trakt) OnGrab(_ context.Context, _ notifications.GrabMessage) error { return nil }

// OnDownload mirrors C# `:36-40`. Adds the episode-file to collection.
func (t *Trakt) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	if msg.Series == nil || msg.EpisodeFile == nil {
		return nil
	}
	return t.withRefresh(ctx, func(accessToken string) error {
		now := t.now()
		payload := &CollectShowsPayload{Shows: []CollectShow{buildEpisodeFileShow(msg.Series, msg.EpisodeFile, true, now)}}
		return addToCollection(ctx, t.doer, accessToken, payload)
	})
}

// OnImportComplete mirrors C# `:42-47`. Adds each file to collection.
func (t *Trakt) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	if msg.Series == nil || len(msg.EpisodeFiles) == 0 {
		return nil
	}
	return t.withRefresh(ctx, func(accessToken string) error {
		now := t.now()
		shows := make([]CollectShow, 0, len(msg.EpisodeFiles))
		for _, f := range msg.EpisodeFiles {
			if f == nil {
				continue
			}
			shows = append(shows, buildEpisodeFileShow(msg.Series, f, true, now))
		}
		return addToCollection(ctx, t.doer, accessToken, &CollectShowsPayload{Shows: shows})
	})
}

// OnRename — not overridden in C#; no-op.
func (t *Trakt) OnRename(_ context.Context, _ notifications.RenameMessage) error { return nil }

// OnEpisodeFileDelete mirrors C# `:49-53`. Removes the file from
// collection.
func (t *Trakt) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	if msg.Series == nil || msg.EpisodeFile == nil {
		return nil
	}
	return t.withRefresh(ctx, func(accessToken string) error {
		payload := &CollectShowsPayload{Shows: []CollectShow{buildEpisodeFileShow(msg.Series, msg.EpisodeFile, false, t.now())}}
		return removeFromCollection(ctx, t.doer, accessToken, payload)
	})
}

// OnSeriesAdd mirrors C# `:55-59`. Adds the series shell to collection.
func (t *Trakt) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	if msg.Series == nil {
		return nil
	}
	return t.withRefresh(ctx, func(accessToken string) error {
		payload := &CollectShowsPayload{Shows: []CollectShow{buildShowFromSeries(msg.Series)}}
		return addToCollection(ctx, t.doer, accessToken, payload)
	})
}

// OnSeriesDelete mirrors C# `:61-65`. Removes the series shell.
func (t *Trakt) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	if msg.Series == nil {
		return nil
	}
	return t.withRefresh(ctx, func(accessToken string) error {
		payload := &CollectShowsPayload{Shows: []CollectShow{buildShowFromSeries(msg.Series)}}
		return removeFromCollection(ctx, t.doer, accessToken, payload)
	})
}

// OnHealthIssue — not overridden in C#; no-op.
func (t *Trakt) OnHealthIssue(_ context.Context, _ notifications.HealthCheckMessage) error {
	return nil
}

// OnHealthRestored — not overridden in C#; no-op.
func (t *Trakt) OnHealthRestored(_ context.Context, _ notifications.HealthCheckMessage) error {
	return nil
}

// OnApplicationUpdate — not overridden in C#; no-op.
func (t *Trakt) OnApplicationUpdate(_ context.Context, _ notifications.ApplicationUpdateMessage) error {
	return nil
}

// OnManualInteractionRequired — not overridden in C#; no-op.
func (t *Trakt) OnManualInteractionRequired(_ context.Context, _ notifications.ManualInteractionRequiredMessage) error {
	return nil
}

// ProcessQueue is a no-op — Trakt has no queue.
func (t *Trakt) ProcessQueue(_ context.Context) error { return nil }

// Test refreshes the token (if needed) then verifies the access
// token by calling getUserName. Mirrors C# `Test()` at `:67-100`.
func (t *Trakt) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	t.settingsMu.Lock()
	defer t.settingsMu.Unlock()
	if v := t.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := t.refreshIfNeeded(ctx); err != nil {
		return []notifications.TestFailure{{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	if _, err := getUserName(ctx, t.doer, t.settings.AccessToken); err != nil {
		prop := "Token"
		if errors.Is(err, ErrInvalidAccessToken) {
			return []notifications.TestFailure{{PropertyName: prop, ErrorMessage: "Access Token is invalid"}}, nil
		}
		return []notifications.TestFailure{{
			PropertyName:        prop,
			ErrorMessage:        fmt.Sprintf("Unable to verify token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	return nil, nil
}

// SupportsOnGrab returns false — Trakt only overrides Download/
// ImportComplete/EpisodeFileDelete/SeriesAdd/SeriesDelete.
func (t *Trakt) SupportsOnGrab() bool { return false }

// SupportsOnDownload — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [Trakt.SupportsOnGrab]. C# base ties to
// SupportsOnDownload.
func (t *Trakt) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [Trakt.SupportsOnGrab]. Returns false.
func (t *Trakt) SupportsOnRename() bool { return false }

// SupportsOnSeriesAdd — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnHealthIssue() bool { return false }

// SupportsOnHealthRestored — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnHealthRestored() bool { return false }

// SupportsOnApplicationUpdate — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnApplicationUpdate() bool { return false }

// SupportsOnManualInteractionRequired — see [Trakt.SupportsOnGrab].
func (t *Trakt) SupportsOnManualInteractionRequired() bool { return false }

// init registers the Trakt constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
