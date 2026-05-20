package downloadstation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// TorrentDownloadStation is the [downloadclients.Provider]
// implementation for the C# `TorrentDownloadStation` class at
// `src/NzbDrone.Core/Download/Clients/DownloadStation/TorrentDownloadStation.cs:1-466`.
//
// Skeleton-only at 5.J75: Test (auth probe via SYNO.API.Auth
// login) + GetStatus (IsLocalhost-only) + identity methods are
// real; Download / GetItems / RemoveItem stub out to
// [ErrNotImplemented] until the V1/V2 task-proxy follow-ups land.
type TorrentDownloadStation struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy

	// rpm is the optional remote-path mapper consulted by
	// GetItems to translate the DSM-side `destination` path into
	// Sonarr-side paths. Nil → pass-through.
	rpm downloadclients.RemotePathMapper
}

// New constructs a TorrentDownloadStation Provider. proxy=nil
// binds a default [Proxy]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *TorrentDownloadStation {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log, nil)
	}
	return &TorrentDownloadStation{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `:53`.
func (t *TorrentDownloadStation) Name() string { return "Download Station" }

// Protocol returns Torrent. The Usenet half (UsenetDownloadStation)
// lives in a separate Provider type with the same proxy surface.
func (t *TorrentDownloadStation) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (t *TorrentDownloadStation) Definition() *downloadclients.DownloadClient { return t.def }

// Settings returns the bound Settings.
func (t *TorrentDownloadStation) Settings() Settings { return t.settings }

// Download ports the magnet arm of C# `Flood.cs:166-183`. Wait,
// that was the wrong reference — `TorrentDownloadStation.cs:166-204`.
// Adds a task to Download Station via [Proxy.AddTaskFromURL] for
// the magnet path; the torrent-file multipart-upload arm
// ([Proxy.AddTaskFromData], C# `:185-204`) is deferred — multipart
// support against the SYNO.DownloadStation.Task API needs a
// follow-up slice.
//
// After AddTaskFromURL the C# code re-queries the task list
// looking for a task whose `Additional.Detail["uri"]` matches the
// magnet URL and returns its composed DownloadID. The Go port does
// the same lookup; nil-match surfaces an error.
//
// DownloadID composition uses the task ID directly (the
// SerialNumberProvider arm at C# `:435-438` that prefixes with
// `<serial>:` is deferred until the SerialNumberProvider port lands —
// the queue join key still works without it since GetItems applies
// the same composition).
func (t *TorrentDownloadStation) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("downloadstation: nil GrabRequest")
	}
	if t.proxy == nil {
		return "", errors.New("downloadstation: nil Proxy")
	}
	if len(req.TorrentBody) > 0 {
		return "", fmt.Errorf("%w: AddTaskFromData (torrent-file upload) — magnet-only at this commit", ErrNotImplemented)
	}
	magnet := strings.TrimSpace(req.MagnetURL)
	if magnet == "" {
		return "", errors.New("downloadstation: GrabRequest has neither TorrentBody nor MagnetURL")
	}
	dest := downloadDirectory(t.settings)
	if err := t.proxy.AddTaskFromURL(ctx, t.settings, magnet, dest); err != nil {
		return "", fmt.Errorf("downloadstation: AddTaskFromURL: %w", err)
	}
	tasks, err := t.proxy.GetTasks(ctx, t.settings)
	if err != nil {
		return "", fmt.Errorf("downloadstation: post-add GetTasks: %w", err)
	}
	for _, task := range tasks {
		if task == nil || !strings.EqualFold(task.Type, "BT") {
			continue
		}
		if task.Additional.Detail["uri"] == magnet {
			return task.ID, nil
		}
	}
	return "", fmt.Errorf("downloadstation: AddTaskFromURL succeeded but no matching task found for magnet %q", magnet)
}

// GetItems ports `TorrentDownloadStation.GetItems` at C# `:64-116`.
// Walks the task list, filters to BT tasks, applies the TvDirectory
// / TvCategory path gate, and projects each into a
// [downloadclients.Item] via the status table.
//
// The C# SharedFolderResolver translation in OutputPath at `:155-163`
// is deferred — the Go port surfaces OutputPath as `/<destination>`
// from `task.Additional.Detail["destination"]` (the raw DSM-side
// path with a leading slash to match the C# convention at `:73`).
// The RemotePathMapper hook is wired so an operator-configured
// mapping still flows through.
//
// Status table priority order ports C# `:243-261`:
//
//   - Status==Unknown / Waiting / FilehostingWaiting → Queued when
//     Size==0 OR RemainingSize > 0; otherwise Completed.
//   - Status==Paused → Paused.
//   - Status==Finished / Seeding → Completed.
//   - Status==Error → Failed.
//   - default → Downloading.
func (t *TorrentDownloadStation) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if t.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	tasks, err := t.proxy.GetTasks(ctx, t.settings)
	if err != nil {
		return nil, fmt.Errorf("downloadstation: GetTasks: %w", err)
	}
	info := t.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(tasks))
	for _, task := range tasks {
		if task == nil || !strings.EqualFold(task.Type, "BT") {
			continue
		}
		if !passesPathGate(task, t.settings) {
			continue
		}
		out = append(out, t.toItem(ctx, info, task))
	}
	return out, nil
}

// toItem projects one [Task] into a [downloadclients.Item].
func (t *TorrentDownloadStation) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, task *Task) *downloadclients.Item {
	dest := task.Additional.Detail["destination"]
	rawPath := "/" + strings.TrimLeft(dest, "/")
	output := ospath.New(rawPath)
	if t.rpm != nil && strings.TrimSpace(dest) != "" {
		if mapped, err := t.rpm.RemapRemoteToLocal(ctx, t.settings.Host, output); err == nil {
			output = mapped
		} else {
			t.log.Debug("downloadstation: RemapRemoteToLocal failed", "destination", rawPath, "err", err)
		}
	}

	remaining := computeRemainingSize(task)
	status := computeStatus(task, remaining)
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		Category:           t.settings.TvCategory,
		DownloadID:         task.ID,
		Title:              task.Title,
		TotalSize:          task.Size,
		RemainingSize:      remaining,
		RemainingTime:      computeRemainingTime(task, remaining),
		SeedRatio:          computeSeedRatio(task),
		Status:             status,
		Message:            computeMessage(task),
	}
	finished := task.Status == TaskStatusFinished
	if info != nil && info.RemoveCompletedDownloads && finished {
		item.CanMoveFiles = true
		item.CanBeRemoved = true
	}
	if status == downloadclients.DownloadItemStatusCompleted || status == downloadclients.DownloadItemStatusFailed {
		// C# `:107-110` composes the per-task OutputPath as
		// remapped(destination) + title. The Go port joins to
		// the task title so the manual-import flow can target
		// the per-download folder/file.
		if strings.TrimSpace(task.Title) != "" {
			item.OutputPath = ospath.New(strings.TrimRight(output.FullPath(), "/") + "/" + task.Title)
		} else {
			item.OutputPath = output
		}
	}
	return item
}

// passesPathGate enforces the TvDirectory / TvCategory filter from
// C# `:75-89`. Empty settings → accept. TvDirectory wins when set
// (the task's destination must be under TvDirectory); otherwise
// TvCategory must appear as one of the destination's path
// segments.
func passesPathGate(task *Task, s Settings) bool {
	dest := task.Additional.Detail["destination"]
	if dest == "" {
		return true
	}
	if strings.TrimSpace(s.TvDirectory) != "" {
		wanted := strings.TrimLeft(strings.TrimSpace(s.TvDirectory), "/")
		got := strings.TrimLeft(dest, "/")
		// Either exact or wanted is an ancestor of got.
		return got == wanted || strings.HasPrefix(got, wanted+"/")
	}
	if strings.TrimSpace(s.TvCategory) != "" {
		for _, seg := range strings.FieldsFunc(dest, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == s.TvCategory {
				return true
			}
		}
		return false
	}
	return true
}

// computeRemainingSize ports C# `:263-274`. Reads
// `Additional.Transfer["size_downloaded"]`, clamps non-numeric to
// 0, returns `Size - max(0, downloaded)`.
func computeRemainingSize(task *Task) int64 {
	downloaded, _ := strconv.ParseInt(task.Additional.Transfer["size_downloaded"], 10, 64)
	if downloaded < 0 {
		downloaded = 0
	}
	return task.Size - downloaded
}

// computeRemainingTime ports C# `:276-294`. Returns nil when
// speed_download is zero / non-numeric; otherwise remainingSize /
// speed seconds.
func computeRemainingTime(task *Task, remaining int64) *time.Duration {
	speed, _ := strconv.ParseInt(task.Additional.Transfer["speed_download"], 10, 64)
	if speed <= 0 {
		return nil
	}
	d := time.Duration(remaining/speed) * time.Second
	return &d
}

// computeSeedRatio ports C# `:296-307`. Returns nil when either
// transfer value is missing/non-numeric; 0 when downloaded <= 0;
// otherwise uploaded/downloaded.
func computeSeedRatio(task *Task) *float64 {
	downloaded, dErr := strconv.ParseInt(task.Additional.Transfer["size_downloaded"], 10, 64)
	uploaded, uErr := strconv.ParseInt(task.Additional.Transfer["size_uploaded"], 10, 64)
	if dErr != nil || uErr != nil {
		return nil
	}
	if downloaded <= 0 {
		zero := 0.0
		return &zero
	}
	r := float64(uploaded) / float64(downloaded)
	return &r
}

// computeStatus ports the C# status table at `:243-261`.
func computeStatus(task *Task, remaining int64) downloadclients.DownloadItemStatus {
	switch task.Status {
	case TaskStatusUnknown, TaskStatusWaiting, TaskStatusFilehostingWaiting:
		if task.Size == 0 || remaining > 0 {
			return downloadclients.DownloadItemStatusQueued
		}
		return downloadclients.DownloadItemStatusCompleted
	case TaskStatusPaused:
		return downloadclients.DownloadItemStatusPaused
	case TaskStatusFinished, TaskStatusSeeding:
		return downloadclients.DownloadItemStatusCompleted
	case TaskStatusError:
		return downloadclients.DownloadItemStatusFailed
	}
	return downloadclients.DownloadItemStatusDownloading
}

// computeMessage ports C# `GetMessage(torrent)` at `:223-241`.
// Returns a non-empty message only when StatusExtra has the
// relevant key for Extracting / Error states.
func computeMessage(task *Task) string {
	if task.StatusExtra == nil {
		return ""
	}
	switch task.Status {
	case TaskStatusExtracting:
		if p := task.StatusExtra["unzip_progress"]; p != "" {
			return "Extracting " + p + "%"
		}
		return "Extracting"
	case TaskStatusError:
		return task.StatusExtra["error_detail"]
	}
	return ""
}

// downloadDirectory ports a trimmed C# `GetDownloadDirectory` at
// `:449-464`. The full C# version also consults `GetDefaultDir`
// (which calls DSMInfoProxy) when only TvCategory is set; the Go
// port defers that arm and returns:
//
//   - Settings.TvDirectory (leading slash stripped) when non-empty
//   - "" otherwise (Download Station will fall back to its own
//     default destination).
//
// Sonarr operators using only Settings.TvCategory will see the
// magnet add land in the DSM default folder — the Test +
// follow-up SharedFolderResolver/DSMInfoProxy work picks up the
// `<default>/<category>` composition.
func downloadDirectory(s Settings) string {
	if strings.TrimSpace(s.TvDirectory) != "" {
		return strings.TrimLeft(s.TvDirectory, "/")
	}
	return ""
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (t *TorrentDownloadStation) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: t.Protocol(),
		Type:     TorrentImplementation,
		Name:     t.Name(),
	}
	if t.def != nil {
		info.ID = t.def.ID
		info.Name = t.def.Name
		info.RemoveCompletedDownloads = t.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional remote-path mapper. Same
// Phase 6.B pattern as the other DCs.
func (t *TorrentDownloadStation) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	t.rpm = rpm
}

// GetImportItem is the C# base passthrough.
func (t *TorrentDownloadStation) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `TorrentDownloadStation.RemoveItem` at C#
// `:144-153`. Calls [Proxy.RemoveTask] (the C# `force_complete=false`
// flavour) with the task ID.
//
// The C# `deleteData=true` branch at `:146-149` calls
// `DeleteItemData(item)` which removes the on-disk file via the
// shared DownloadClientBase machinery. The Go-port disk-cleanup
// arm hasn't ported yet (Phase 6.B follow-up); for now deleteData
// is logged but not acted on at the Provider level — the
// orchestrator's downstream cleanup path takes over.
func (t *TorrentDownloadStation) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if t.proxy == nil {
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
		t.log.Debug("downloadstation: deleteData=true (disk cleanup deferred to Phase 6.B)", "downloadID", id)
	}
	if err := t.proxy.RemoveTask(ctx, t.settings, id); err != nil {
		return fmt.Errorf("downloadstation: RemoveTask(%s): %w", id, err)
	}
	return nil
}

// GetStatus ports the IsLocalhost arm of C# `:392-417`. The
// OutputRootFolders arm (which calls DSMInfoProxy.GetSerialNumber +
// SharedFolderResolver) is deferred to the SharedFolderResolver
// follow-up — IsLocalhost suffices for the healthcheck path.
func (t *TorrentDownloadStation) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(t.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (t *TorrentDownloadStation) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (t *TorrentDownloadStation) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports the connection + auth arm of C# `Test(failures)` at
// `:206-216`. Runs Settings.Validate first, then drives the
// auth-probe via [Proxy.Authenticate].
//
// The C# version also runs `TestOutputPath` (validates the
// configured TvDirectory against SharedFolderResolver) and
// `TestGetTorrents` (lists tasks to verify the API surface is
// reachable post-login). Both are deferred: TestOutputPath
// requires the SharedFolderResolver port (5.J79), TestGetTorrents
// requires the V1/V2 task proxy (5.J76).
func (t *TorrentDownloadStation) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := t.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	// TestConnection arm (C# :359-396): drives auth + the
	// ValidateVersion API-info check inline.
	if _, err := t.proxy.Authenticate(ctx, t.settings, true); err != nil {
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
	// ValidateVersion arm (C# :398-415): require >= v2 of
	// SYNO.DownloadStation.Task.
	api, err := t.proxy.GetAPIInfo(ctx, t.settings, apiDownloadStationName)
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
	// TestGetTorrents arm (C# :417-428): drive the task-list call
	// to verify the auth + API combo actually works post-auth.
	// Surface any failure as a generic Host failure.
	if _, err := t.proxy.GetTasks(ctx, t.settings); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to list tasks: %v", err),
		}}, nil
	}
	return nil, nil
}

// isLocalHost ports the C# `Settings.Host == "127.0.0.1" || ==
// "::1" || == "localhost"` check.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ErrNotImplemented signals a 5.J75 skeleton stub.
var ErrNotImplemented = errors.New("downloadstation: not yet implemented in this build")

// Compile-time guard.
var _ downloadclients.Provider = (*TorrentDownloadStation)(nil)

// constructor is the registry factory for the Torrent half.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
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
	t := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		t.SetRemotePathMapper(rpm)
	}
	return t, nil
}

func init() {
	downloadclients.Default().MustRegister(TorrentImplementation, ConfigContract, constructor)
}
