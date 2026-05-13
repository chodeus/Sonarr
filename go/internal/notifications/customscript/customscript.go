package customscript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// Implementation is the persisted-row identifier for this provider.
// Mirrors C# `CustomScript.cs:24` class name.
const Implementation = "CustomScript"

// ConfigContract is the persisted-row settings type identifier.
// Mirrors C# `CustomScriptSettings.cs:26` class name.
const ConfigContract = "CustomScriptSettings"

// CustomScript ports the C# `CustomScript` notification provider at
// `Notifications/CustomScript/CustomScript.cs:24-415`.
type CustomScript struct {
	def      *notifications.Notification
	settings Settings
}

// Compile-time guard.
var _ notifications.Provider = (*CustomScript)(nil)

// New constructs a *CustomScript from a persisted row + the
// [notifications.Dependencies] bundle. The script body uses no
// network deps so Dependencies is ignored.
func New(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
	if def == nil {
		return nil, errors.New("customscript: nil definition")
	}
	var s Settings
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &s); err != nil {
			return nil, fmt.Errorf("customscript: unmarshal settings: %w", err)
		}
	}
	return &CustomScript{def: def, settings: s}, nil
}

// Name returns the provider display label. C# `:51`.
func (c *CustomScript) Name() string { return "Custom Script" }

// Link returns the docs URL. C# `:53`.
func (c *CustomScript) Link() string { return "https://wiki.servarr.com/sonarr/settings#connections" }

// Definition returns the persisted row.
func (c *CustomScript) Definition() *notifications.Notification { return c.def }

// instanceVars seeds the three "instance" env vars every event sets.
// Mirrors C# `AddInstanceVariables` at `CustomScript.cs:390-395`.
//
// Sonarr_InstanceName and Sonarr_ApplicationUrl are DEFERRED — the
// orchestrator can plumb a configfile.Provider via Dependencies in a
// later slice. For now both emit empty strings so the script side
// sees the variable defined.
func instanceVars(eventType string) map[string]string {
	return map[string]string{
		"Sonarr_EventType":      eventType,
		"Sonarr_InstanceName":   "",
		"Sonarr_ApplicationUrl": "",
	}
}

// seriesVars appends the series-shape env vars. Mirrors C#
// `AddSeriesVariables` at `:397-413`. Genre + OriginalLanguage +
// Tags lookups DEFERRED — the available scalar fields cover ~80% of
// real-world script usage.
func seriesVars(env map[string]string, s *series.Series) {
	if s == nil {
		return
	}
	env["Sonarr_Series_Id"] = strconv.Itoa(s.ID)
	env["Sonarr_Series_Title"] = s.Title
	env["Sonarr_Series_TitleSlug"] = s.TitleSlug
	env["Sonarr_Series_Path"] = s.Path
	env["Sonarr_Series_TvdbId"] = strconv.Itoa(s.TvdbID)
	env["Sonarr_Series_TvMazeId"] = strconv.Itoa(s.TvMazeID)
	env["Sonarr_Series_TmdbId"] = strconv.Itoa(s.TmdbID)
	env["Sonarr_Series_ImdbId"] = s.ImdbID
	env["Sonarr_Series_Type"] = strconv.Itoa(s.SeriesType)
	env["Sonarr_Series_Year"] = strconv.Itoa(s.Year)
	env["Sonarr_Series_OriginalCountry"] = s.OriginalCountry
	env["Sonarr_Series_Genres"] = s.Genres
	// OriginalLanguage / Tags lookup deferred — see package doc.
	env["Sonarr_Series_OriginalLanguage"] = ""
	env["Sonarr_Series_Tags"] = ""
}

// OnGrab mirrors C# `:57-91`. The richer Release/Quality/
// CustomFormat env vars require parser.RemoteEpisode + Release
// types; those are deferred and emit empty strings.
func (c *CustomScript) OnGrab(ctx context.Context, msg notifications.GrabMessage) error {
	env := instanceVars("Grab")
	seriesVars(env, msg.Series)
	env["Sonarr_Download_Client"] = msg.DownloadClientName
	env["Sonarr_Download_Client_Type"] = msg.DownloadClientType
	env["Sonarr_Download_Id"] = msg.DownloadID
	if msg.Quality != nil {
		env["Sonarr_Release_Quality"] = msg.Quality.Quality.Name
		env["Sonarr_Release_QualityVersion"] = strconv.Itoa(msg.Quality.Revision.Version)
	}
	return execScript(ctx, &c.settings, env)
}

// OnDownload mirrors C# `:93-150`. Many env vars (MediaInfo /
// IndexerFlags / CustomFormatScore / Release.*) are deferred.
func (c *CustomScript) OnDownload(ctx context.Context, msg notifications.DownloadMessage) error {
	env := instanceVars("Download")
	seriesVars(env, msg.Series)
	env["Sonarr_IsUpgrade"] = strconv.FormatBool(len(msg.OldFiles) > 0)
	if msg.EpisodeFile != nil {
		env["Sonarr_EpisodeFile_Id"] = strconv.Itoa(msg.EpisodeFile.ID)
		env["Sonarr_EpisodeFile_RelativePath"] = msg.EpisodeFile.RelativePath
		if msg.Series != nil {
			env["Sonarr_EpisodeFile_Path"] = msg.Series.Path + "/" + msg.EpisodeFile.RelativePath
		}
		env["Sonarr_EpisodeFile_SeasonNumber"] = strconv.Itoa(msg.EpisodeFile.SeasonNumber)
		// Quality decoded from the raw JSON blob is DEFERRED.
		env["Sonarr_EpisodeFile_ReleaseGroup"] = msg.EpisodeFile.ReleaseGroup
		env["Sonarr_EpisodeFile_SceneName"] = msg.EpisodeFile.SceneName
	}
	env["Sonarr_EpisodeFile_SourcePath"] = msg.SourcePath
	env["Sonarr_Download_Id"] = msg.DownloadID
	if len(msg.OldFiles) > 0 {
		paths := make([]string, 0, len(msg.OldFiles))
		for _, old := range msg.OldFiles {
			if old != nil && old.File != nil {
				paths = append(paths, old.File.RelativePath)
			}
		}
		env["Sonarr_DeletedRelativePaths"] = strings.Join(paths, "|")
	}
	return execScript(ctx, &c.settings, env)
}

// OnImportComplete mirrors C# `:152-197`. Heavy MediaInfo/Release
// fields deferred.
func (c *CustomScript) OnImportComplete(ctx context.Context, msg notifications.ImportCompleteMessage) error {
	env := instanceVars("Download")
	seriesVars(env, msg.Series)
	env["Sonarr_EpisodeFile_Count"] = strconv.Itoa(len(msg.EpisodeFiles))
	env["Sonarr_SourcePath"] = msg.SourcePath
	env["Sonarr_DestinationPath"] = msg.DestinationPath
	env["Sonarr_Download_Id"] = msg.DownloadID
	env["Sonarr_Release_Group"] = msg.ReleaseGroup
	if msg.ReleaseQuality != nil {
		env["Sonarr_Release_Quality"] = msg.ReleaseQuality.Quality.Name
		env["Sonarr_Release_QualityVersion"] = strconv.Itoa(msg.ReleaseQuality.Revision.Version)
	}
	return execScript(ctx, &c.settings, env)
}

// OnRename mirrors C# `:199-213`. RenamedFiles are typed `any` —
// the per-file rename details require [MediaFiles.RenamedEpisodeFile]
// to port (deferred). Series is also `any` per the Provider
// signature — assert to *series.Series.
func (c *CustomScript) OnRename(ctx context.Context, msg notifications.RenameMessage) error {
	env := instanceVars("Rename")
	if ser, ok := msg.Series.(*series.Series); ok {
		seriesVars(env, ser)
	}
	return execScript(ctx, &c.settings, env)
}

// OnEpisodeFileDelete mirrors C# `:215-242`.
func (c *CustomScript) OnEpisodeFileDelete(ctx context.Context, msg notifications.EpisodeDeleteMessage) error {
	env := instanceVars("EpisodeFileDelete")
	seriesVars(env, msg.Series)
	if msg.EpisodeFile != nil {
		env["Sonarr_EpisodeFile_Id"] = strconv.Itoa(msg.EpisodeFile.ID)
		env["Sonarr_EpisodeFile_RelativePath"] = msg.EpisodeFile.RelativePath
		if msg.Series != nil {
			env["Sonarr_EpisodeFile_Path"] = msg.Series.Path + "/" + msg.EpisodeFile.RelativePath
		}
		env["Sonarr_EpisodeFile_SeasonNumber"] = strconv.Itoa(msg.EpisodeFile.SeasonNumber)
		// Quality decoded from the raw JSON blob is DEFERRED.
		env["Sonarr_EpisodeFile_ReleaseGroup"] = msg.EpisodeFile.ReleaseGroup
		env["Sonarr_EpisodeFile_SceneName"] = msg.EpisodeFile.SceneName
	}
	env["Sonarr_EpisodeFile_DeleteReason"] = msg.Reason.String()
	return execScript(ctx, &c.settings, env)
}

// OnSeriesAdd mirrors C# `:244-253`.
func (c *CustomScript) OnSeriesAdd(ctx context.Context, msg notifications.SeriesAddMessage) error {
	env := instanceVars("SeriesAdd")
	seriesVars(env, msg.Series)
	return execScript(ctx, &c.settings, env)
}

// OnSeriesDelete mirrors C# `:255-264`.
func (c *CustomScript) OnSeriesDelete(ctx context.Context, msg notifications.SeriesDeleteMessage) error {
	env := instanceVars("SeriesDelete")
	seriesVars(env, msg.Series)
	env["Sonarr_Series_DeletedFiles"] = strconv.FormatBool(msg.DeletedFiles)
	return execScript(ctx, &c.settings, env)
}

// OnHealthIssue mirrors C# `:266-278`.
func (c *CustomScript) OnHealthIssue(ctx context.Context, msg notifications.HealthCheckMessage) error {
	env := instanceVars("HealthIssue")
	env["Sonarr_Health_Issue_Level"] = msg.Check.Type.String()
	env["Sonarr_Health_Issue_Message"] = msg.Check.Message
	env["Sonarr_Health_Issue_Type"] = msg.Check.Source
	env["Sonarr_Health_Issue_Wiki"] = msg.Check.WikiURL
	return execScript(ctx, &c.settings, env)
}

// OnHealthRestored mirrors C# `:280-292`.
func (c *CustomScript) OnHealthRestored(ctx context.Context, msg notifications.HealthCheckMessage) error {
	env := instanceVars("HealthRestored")
	env["Sonarr_Health_Restored_Level"] = msg.Check.Type.String()
	env["Sonarr_Health_Restored_Message"] = msg.Check.Message
	env["Sonarr_Health_Restored_Type"] = msg.Check.Source
	env["Sonarr_Health_Restored_Wiki"] = msg.Check.WikiURL
	return execScript(ctx, &c.settings, env)
}

// OnApplicationUpdate mirrors C# `:294-305`.
func (c *CustomScript) OnApplicationUpdate(ctx context.Context, msg notifications.ApplicationUpdateMessage) error {
	env := instanceVars("ApplicationUpdate")
	env["Sonarr_Update_Message"] = msg.Message
	env["Sonarr_Update_NewVersion"] = msg.NewVersion
	env["Sonarr_Update_PreviousVersion"] = msg.PreviousVersion
	return execScript(ctx, &c.settings, env)
}

// OnManualInteractionRequired mirrors C# `:307-323`. TrackedDownload
// fields are typed `any` and deferred.
func (c *CustomScript) OnManualInteractionRequired(ctx context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	env := instanceVars("ManualInteractionRequired")
	seriesVars(env, msg.Series)
	env["Sonarr_Download_Id"] = msg.DownloadID
	return execScript(ctx, &c.settings, env)
}

// ProcessQueue is a no-op — CustomScript has no queue.
func (c *CustomScript) ProcessQueue(_ context.Context) error { return nil }

// Test runs the configured script with Sonarr_EventType=Test +
// the two instance vars. Mirrors C# `Test` at `:324-357`.
func (c *CustomScript) Test(ctx context.Context) ([]notifications.TestFailure, error) {
	var failures []notifications.TestFailure
	if v := c.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	env := instanceVars("Test")
	if err := execScript(ctx, &c.settings, env); err != nil {
		prop := ""
		var ne *ErrNonZeroExit
		if errors.As(err, &ne) {
			failures = append(failures, notifications.TestFailure{PropertyName: prop, ErrorMessage: err.Error()})
		} else {
			failures = append(failures, notifications.TestFailure{
				PropertyName:        "Path",
				ErrorMessage:        fmt.Sprintf("Script execution failed: %v", err),
				DetailedDescription: err.Error(),
			})
		}
	}
	return failures, nil
}

// SupportsOnGrab reports OnGrab capability. C# CustomScript overrides
// every OnX hook — every Supports* flag is true.
func (c *CustomScript) SupportsOnGrab() bool { return true }

// SupportsOnDownload — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnDownload() bool { return true }

// SupportsOnUpgrade — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnUpgrade() bool { return true }

// SupportsOnImportComplete — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnImportComplete() bool { return true }

// SupportsOnRename — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnRename() bool { return true }

// SupportsOnSeriesAdd — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnSeriesAdd() bool { return true }

// SupportsOnSeriesDelete — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnSeriesDelete() bool { return true }

// SupportsOnEpisodeFileDelete — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnEpisodeFileDelete() bool { return true }

// SupportsOnEpisodeFileDeleteForUpgrade — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }

// SupportsOnHealthIssue — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnHealthIssue() bool { return true }

// SupportsOnHealthRestored — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnHealthRestored() bool { return true }

// SupportsOnApplicationUpdate — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnApplicationUpdate() bool { return true }

// SupportsOnManualInteractionRequired — see [CustomScript.SupportsOnGrab].
func (c *CustomScript) SupportsOnManualInteractionRequired() bool { return true }

// RequestAction is a no-op — the C# `CustomScript` does not
// override RequestAction; the base `NotificationBase.cs` default
// returns null. Return an empty struct so v3-UI callers see {}.
func (c *CustomScript) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// init registers the CustomScript constructor.
func init() {
	notifications.Default().MustRegister(Implementation, ConfigContract, New)
}
