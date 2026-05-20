package downloadstation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// UsenetImplementation is the persisted-row identifier for the
// Usenet half. Mirrors C# class name `UsenetDownloadStation`.
const UsenetImplementation = "UsenetDownloadStation"

// UsenetDownloadStation is the [downloadclients.Provider]
// implementation for the C# `UsenetDownloadStation` class at
// `src/NzbDrone.Core/Download/Clients/DownloadStation/UsenetDownloadStation.cs:1-446`.
//
// Shares the [Proxy] surface with [TorrentDownloadStation] — only
// the type-filter ("NZB" vs "BT") + the Download arm (multipart
// .nzb upload via [Proxy.AddTaskFromData]) diverge. Functionally
// complete at 5.J77 except for the SharedFolderResolver-driven
// OutputPath translation + TestOutputPath arm (both deferred to
// the follow-up that ports SharedFolderResolver).
type UsenetDownloadStation struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
	rpm      downloadclients.RemotePathMapper
}

// NewUsenet constructs a UsenetDownloadStation Provider. proxy=nil
// binds a default [Proxy]; log=nil binds [slog.Default].
func NewUsenet(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *UsenetDownloadStation {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log, nil)
	}
	return &UsenetDownloadStation{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. Same as the torrent half —
// the C# tree at `UsenetDownloadStation.cs:51` returns `"Download
// Station"`.
func (u *UsenetDownloadStation) Name() string { return "Download Station" }

// Protocol returns Usenet.
func (u *UsenetDownloadStation) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClient row.
func (u *UsenetDownloadStation) Definition() *downloadclients.DownloadClient { return u.def }

// Settings returns the bound Settings.
func (u *UsenetDownloadStation) Settings() Settings { return u.settings }

// Download ports C# `UsenetDownloadStation.AddFromNzbFile` at
// `:177-196`. Uploads the .nzb bytes via [Proxy.AddTaskFromData]
// (multipart POST), then looks up the resulting task by matching
// `Additional.Detail["uri"]` against the filename string the C#
// post-add lookup uses at `:183`.
//
// Returns the resolved Task.ID. Empty NzbBody surfaces an error;
// post-add no-match surfaces a "failed to add" error matching
// C# `:195`.
func (u *UsenetDownloadStation) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("downloadstation: nil GrabRequest")
	}
	if u.proxy == nil {
		return "", errors.New("downloadstation: nil Proxy")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("downloadstation: GrabRequest.NzbBody is empty")
	}
	title := strings.TrimSpace(req.ReleaseTitle)
	if title == "" {
		return "", errors.New("downloadstation: GrabRequest.ReleaseTitle is empty")
	}
	filename := title + ".nzb"
	dest := downloadDirectory(u.settings)
	if err := u.proxy.AddTaskFromData(ctx, u.settings, req.NzbBody, filename, dest); err != nil {
		return "", fmt.Errorf("downloadstation: AddTaskFromData(%q): %w", filename, err)
	}
	tasks, err := u.proxy.GetTasks(ctx, u.settings)
	if err != nil {
		return "", fmt.Errorf("downloadstation: post-add GetTasks: %w", err)
	}
	for _, task := range tasks {
		if task == nil || !strings.EqualFold(task.Type, "NZB") {
			continue
		}
		if task.Additional.Detail["uri"] == filename {
			return task.ID, nil
		}
	}
	return "", fmt.Errorf("downloadstation: AddTaskFromData succeeded but no matching task found for %q", filename)
}

// GetItems ports `UsenetDownloadStation.GetItems` at C# `:62-127`.
// Walks the task list, filters to NZB tasks, applies the
// TvDirectory / TvCategory path gate, projects each into a
// [downloadclients.Item] via the same status table as the torrent
// half (shared via the package-level `computeStatus` helper).
//
// CanMoveFiles + CanBeRemoved both default to TRUE — the Usenet
// half always allows cleanup regardless of RemoveCompletedDownloads
// (matches C# `:109-110` which unconditionally sets both flags).
//
// The C# global-speed-driven RemainingTime computation at `:73-77`
// (which divides total-remaining by aggregate download-speed) is
// approximated per-task here: each task uses its own
// speed_download for the ETA. Net behavioural difference: ETA on
// a stalled-but-counted task may be different. Documented; the
// global-speed share-out is a queue-UI nicety, not load-bearing.
func (u *UsenetDownloadStation) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if u.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	tasks, err := u.proxy.GetTasks(ctx, u.settings)
	if err != nil {
		return nil, fmt.Errorf("downloadstation: GetTasks: %w", err)
	}
	info := u.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(tasks))
	for _, task := range tasks {
		if task == nil || !strings.EqualFold(task.Type, "NZB") {
			continue
		}
		if !passesPathGate(task, u.settings) {
			continue
		}
		out = append(out, u.toItem(ctx, info, task))
	}
	return out, nil
}

// toItem projects one [Task] into an Item with the Usenet-half
// conventions (CanMoveFiles/CanBeRemoved=true unconditionally; no
// SeedRatio).
func (u *UsenetDownloadStation) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, task *Task) *downloadclients.Item {
	dest := task.Additional.Detail["destination"]
	rawPath := "/" + strings.TrimLeft(dest, "/")
	output := ospath.New(rawPath)
	if u.rpm != nil && strings.TrimSpace(dest) != "" {
		if mapped, err := u.rpm.RemapRemoteToLocal(ctx, u.settings.Host, output); err == nil {
			output = mapped
		} else {
			u.log.Debug("downloadstation: RemapRemoteToLocal failed", "destination", rawPath, "err", err)
		}
	}

	remaining := computeRemainingSize(task)
	status := computeStatus(task, remaining)
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		Category:           u.settings.TvCategory,
		DownloadID:         task.ID,
		Title:              task.Title,
		TotalSize:          task.Size,
		RemainingSize:      remaining,
		Status:             status,
		Message:            computeMessage(task),
		CanBeRemoved:       true,
		CanMoveFiles:       true,
	}
	// RemainingTime is set only when not Paused (matches C# :113-116).
	if status != downloadclients.DownloadItemStatusPaused {
		item.RemainingTime = computeRemainingTime(task, remaining)
	}
	if status == downloadclients.DownloadItemStatusCompleted || status == downloadclients.DownloadItemStatusFailed {
		if strings.TrimSpace(task.Title) != "" {
			item.OutputPath = ospath.New(strings.TrimRight(output.FullPath(), "/") + "/" + task.Title)
		} else {
			item.OutputPath = output
		}
	}
	// SeedRatio intentionally nil — usenet has no ratio concept.
	_ = time.Duration(0)
	return item
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (u *UsenetDownloadStation) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: u.Protocol(),
		Type:     UsenetImplementation,
		Name:     u.Name(),
	}
	if u.def != nil {
		info.ID = u.def.ID
		info.Name = u.def.Name
		info.RemoveCompletedDownloads = u.def.RemoveCompletedDownloads
	}
	return info
}

// GetImportItem is the C# base passthrough.
func (u *UsenetDownloadStation) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `UsenetDownloadStation.RemoveItem` at C#
// `:166-175`. Delegates to [Proxy.RemoveTask] (same delete v1 +
// force_complete=false as the torrent half). The C#
// deleteData=true branch's DeleteItemData arm is deferred to
// Phase 6.B's disk-cleanup wiring.
func (u *UsenetDownloadStation) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if u.proxy == nil {
		return errors.New("downloadstation: nil Proxy")
	}
	if item == nil {
		return errors.New("downloadstation: nil item")
	}
	id := strings.TrimSpace(item.DownloadID)
	if id == "" {
		return errors.New("downloadstation: item has empty DownloadID")
	}
	if deleteData {
		u.log.Debug("downloadstation: deleteData=true (disk cleanup deferred to Phase 6.B)", "downloadID", id)
	}
	if err := u.proxy.RemoveTask(ctx, u.settings, id); err != nil {
		return fmt.Errorf("downloadstation: RemoveTask(%s): %w", id, err)
	}
	return nil
}

// GetStatus mirrors the torrent half — IsLocalhost-only at this
// commit. The SharedFolderResolver-driven OutputRootFolders arm
// at C# `:140-164` is deferred to the SharedFolderResolver port.
func (u *UsenetDownloadStation) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(u.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (u *UsenetDownloadStation) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (u *UsenetDownloadStation) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `UsenetDownloadStation.Test(failures)` at C# `:198-208`.
// Same 4-arm chain as the torrent half: connection → version →
// task-list (TestOutputPath deferred until SharedFolderResolver
// lands).
func (u *UsenetDownloadStation) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := u.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if _, err := u.proxy.Authenticate(ctx, u.settings, true); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "Username",
				ErrorMessage: fmt.Sprintf("Authentication failed: %v", err),
			}}, nil
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Diskstation: %v", err),
		}}, nil
	}
	api, err := u.proxy.GetAPIInfo(ctx, u.settings, apiDownloadStationName)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to read API info: %v", err),
		}}, nil
	}
	if api.MinVersion > 2 || api.MaxVersion < 2 {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Download Station API version %d..%d does not support the required version 2", api.MinVersion, api.MaxVersion),
		}}, nil
	}
	if _, err := u.proxy.GetTasks(ctx, u.settings); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to list tasks: %v", err),
		}}, nil
	}
	return nil, nil
}

// SetRemotePathMapper binds the optional remote-path mapper. Same
// Phase 6.B pattern as the torrent half.
func (u *UsenetDownloadStation) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	u.rpm = rpm
}

// Compile-time guard.
var _ downloadclients.Provider = (*UsenetDownloadStation)(nil)

// usenetConstructor is the registry factory for the Usenet half.
func usenetConstructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("downloadstation: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("downloadstation: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	u := NewUsenet(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		u.SetRemotePathMapper(rpm)
	}
	return u, nil
}

func init() {
	downloadclients.Default().MustRegister(UsenetImplementation, ConfigContract, usenetConstructor)
}
