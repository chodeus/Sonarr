package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/quality"
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

// settingsPersister is the narrow surface the provider needs from the
// notifications.Repo. *notifications.Repo satisfies it via its
// embedded repo.Repository[Notification].Update method.
type settingsPersister interface {
	Update(ctx context.Context, def *notifications.Notification) error
}

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
	persister  settingsPersister
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
	persister, _ := deps.NotificationsRepo.(settingsPersister)
	return &Trakt{def: def, settings: s, doer: doer, persister: persister, now: time.Now}, nil
}

// Name returns the provider display label. C# `:34`.
func (t *Trakt) Name() string { return "Trakt" }

// Link returns the docs URL. C# `:33`.
func (t *Trakt) Link() string { return "https://trakt.tv/" }

// Definition returns the persisted row.
func (t *Trakt) Definition() *notifications.Notification { return t.def }

// refreshIfNeeded refreshes the access token if it's within the
// 5-minute renewal window. Mirrors C# `RefreshTokenIfNecessary` +
// `RefreshToken` at `:127-163`. After a successful refresh the new
// AccessToken/Expires/RefreshToken triple is round-tripped back to
// the DB via the persister (Dependencies.NotificationsRepo) — mirrors
// C# `_notificationRepository.UpdateSettings(...)` at `:155`. A nil
// persister or row-id == 0 skips the persistence step (matches the
// C# `Definition.Id > 0` guard at `:153`).
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
	t.persistSettingsLocked(ctx)
	return nil
}

// persistSettingsLocked round-trips the in-memory Settings struct
// back to the DB row via the persister. Marshal failures + persister
// errors are intentionally swallowed (mirrors the C# fire-and-forget
// `UpdateSettings` semantics) — the next refresh attempt will retry.
//
// settingsMu MUST be held by the caller.
func (t *Trakt) persistSettingsLocked(ctx context.Context) {
	if t.persister == nil || t.def == nil || t.def.ID == 0 {
		return
	}
	raw, err := json.Marshal(t.settings)
	if err != nil {
		return
	}
	t.def.Settings = string(raw)
	_ = t.persister.Update(ctx, t.def)
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
// Episodes carried via the C# code's `EpisodeFile.Episodes.Value`
// aren't on the Go EpisodeFile row (the Episodes link is computed
// via a separate lookup not yet wired). For now this still emits
// show-level updates only — the per-episode payload + MediaInfo
// decode lands when the episode-list dep gets plumbed at the
// orchestrator level; see [BuildEpisodeResource] for the per-episode
// payload assembly that follow-up wiring can call.
func buildEpisodeFileShow(s *series.Series, f *episodefiles.File, withCollectedAt bool, now time.Time) CollectShow {
	show := buildShowFromSeries(s)
	_ = withCollectedAt
	_ = now
	_ = f
	return show
}

// BuildEpisodeResource assembles a per-episode trakt payload entry
// from an [episodefiles.File] + an episode number. Mirrors the
// inner loop body of C# `AddEpisodeToCollection` at `:165-203`:
// every per-episode entry carries the MediaInfo-derived resolution
// / hdr / media_type / audio / audio_channels fields. Pure function
// — exported so the future episode-list dep can call it per
// resolved episode.
//
// withCollectedAt=true emits a CollectedAt = now for the
// AddEpisodeToCollection flow; false omits it for
// RemoveEpisodeFromCollection (mirrors C# branching on the same
// boolean).
func BuildEpisodeResource(f *episodefiles.File, episodeNumber int, withCollectedAt bool, now time.Time) EpisodeResource {
	er := EpisodeResource{Number: episodeNumber}
	if withCollectedAt {
		t := now
		er.CollectedAt = &t
	}
	if f == nil {
		return er
	}
	// Quality + MediaInfo decode. Quality.Source/Resolution come
	// from the persisted Quality blob; HDR / audio fields come
	// from MediaInfo (nil-safe via the mapper helpers).
	if q := unmarshalQualityModel(f.Quality); q != nil {
		er.Resolution = MapResolution(q.Quality.Resolution, mediaInfoScanType(f))
		er.MediaType = MapMediaType(q.Quality.Source)
	}
	er.Hdr = MapHdr(f)
	audio := MapAudio(f)
	er.Audio = audio
	er.AudioChannels = MapAudioChannels(f, audio)
	return er
}

// mediaInfoScanType returns f.MediaInfo.ScanType safely with
// nil-tolerance. Extracted so [BuildEpisodeResource] can call
// [MapResolution] without an inline nil check.
func mediaInfoScanType(f *episodefiles.File) string {
	if f == nil || f.MediaInfo == nil {
		return ""
	}
	return f.MediaInfo.ScanType
}

// unmarshalQualityModel decodes the Quality JSON blob carried by
// [episodefiles.File.Quality]. Returns nil for empty/zero blobs
// (matches C# behaviour where un-parsed Quality defaults to Unknown).
// Decode failures swallow to nil — payload emission skips the
// per-episode entry's MediaType/Resolution rather than propagating
// the error.
func unmarshalQualityModel(blob string) *quality.Model {
	if strings.TrimSpace(blob) == "" || blob == "{}" {
		return nil
	}
	var m quality.Model
	if err := json.Unmarshal([]byte(blob), &m); err != nil {
		return nil
	}
	return &m
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

// RequestAction implements the C# `Trakt.RequestAction` admin path
// at `:102-125`. Two actions: startOAuth (builds the trakt.tv
// authorize URL pinned to the servarr redirect proxy) and
// getOAuthToken (resolves the redirect-returned tuple to a usable
// settings triple plus the trakt slug via GET /users/settings).
// Unknown actions return `{}` (mirrors C# `:124`).
//
// startOAuth output: `{"OauthUrl": "<trakt.tv/oauth/authorize?...>"}`
// — preserves PascalCase `OauthUrl` key per C# anonymous object at
// `:110`. Query params: client_id (pinned ClientId), response_type=
// "code", redirect_uri (pinned servarr proxy), state (caller-supplied
// callbackUrl for redirect round-trip).
//
// getOAuthToken output: `{accessToken, expires, refreshToken,
// authUser}` — `expires` is now+expires_in seconds, `authUser` is
// the trakt slug fetched via GetUserName. Missing access_token /
// expires_in / refresh_token query params return ErrBadRequest.
// expires_in must be a positive int — non-int values return
// ErrBadRequest. A failed GetUserName call is swallowed and
// authUser falls back to "" (mirrors C# leaving the field
// unpopulated when the call errors).
func (t *Trakt) RequestAction(ctx context.Context, action string, query map[string]string) (any, error) {
	switch action {
	case "startOAuth":
		callbackURL := query["callbackUrl"]
		if callbackURL == "" {
			return nil, fmt.Errorf("%w: callbackUrl invalid", ErrBadRequest)
		}
		u := buildOAuthAuthorizeURL(callbackURL)
		return map[string]any{"OauthUrl": u}, nil
	case "getOAuthToken":
		accessToken := query["access_token"]
		expiresIn := query["expires_in"]
		refreshToken := query["refresh_token"]
		if accessToken == "" {
			return nil, fmt.Errorf("%w: access_token invalid", ErrBadRequest)
		}
		if expiresIn == "" {
			return nil, fmt.Errorf("%w: expires_in invalid", ErrBadRequest)
		}
		if refreshToken == "" {
			return nil, fmt.Errorf("%w: refresh_token invalid", ErrBadRequest)
		}
		secs, err := strconv.Atoi(expiresIn)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("%w: expires_in not a positive int", ErrBadRequest)
		}
		expires := t.now().Add(time.Duration(secs) * time.Second)
		authUser, _ := getUserName(ctx, t.doer, accessToken)
		return map[string]any{
			"accessToken":  accessToken,
			"expires":      expires,
			"refreshToken": refreshToken,
			"authUser":     authUser,
		}, nil
	default:
		return map[string]any{}, nil
	}
}

// init registers the Trakt constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
