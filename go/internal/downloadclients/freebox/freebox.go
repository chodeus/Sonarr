package freebox

import (
	"context"
	"encoding/base64"
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

// TorrentFreeboxDownload is the [downloadclients.Provider]
// implementation for the C# `TorrentFreeboxDownload` class at
// `src/NzbDrone.Core/Download/Clients/FreeboxDownload/TorrentFreeboxDownload.cs:1-232`.
//
// Functionally complete after the 5.J79 → 5.J88 arc: Test runs
// the HMAC-SHA1 session-token probe; Download dispatches via POST
// /downloads/add (URL form-encoded for magnets, multipart for
// .torrent bytes) + SetTorrentSettings PUT for AddPaused/queue
// position/seed ratio; GetItems walks /downloads/ with the BT type
// filter + DestinationDirectory/Category path gates + 6-arm status
// table + ETA + SeedRatio composition; RemoveItem fires DELETE
// /downloads/<id> with the optional /erase suffix; GetStatus
// populates OutputRootFolders via GetDownloadConfiguration +
// RemotePathMapper translation.
type TorrentFreeboxDownload struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy

	// rpm is the optional remote-path mapper consulted by GetItems
	// to translate the Freebox-side DecodedDownloadDirectory into
	// Sonarr-side paths. Nil → pass-through.
	rpm downloadclients.RemotePathMapper
}

// New constructs a TorrentFreeboxDownload Provider. proxy=nil binds
// a default [Proxy]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *TorrentFreeboxDownload {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &TorrentFreeboxDownload{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `TorrentFreeboxDownload.cs`
// returns `"Freebox Download"`.
func (f *TorrentFreeboxDownload) Name() string { return "Freebox Download" }

// Protocol returns Torrent.
func (f *TorrentFreeboxDownload) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (f *TorrentFreeboxDownload) Definition() *downloadclients.DownloadClient { return f.def }

// Settings returns the bound Settings.
func (f *TorrentFreeboxDownload) Settings() Settings { return f.settings }

// Download ports `TorrentFreeboxDownload.AddFromMagnetLink` +
// `AddFromTorrentFile` at C# `TorrentFreeboxDownload.cs:130-149`.
// Dispatches:
//
//   - TorrentBody non-empty → [Proxy.AddTaskFromFile] (multipart
//     POST with the .torrent bytes as a `download_file` form-part).
//   - MagnetURL non-empty → [Proxy.AddTaskFromURL] (form-encoded
//     POST with the URL as a `download_url` form parameter).
//
// Both arms post a base64-encoded `download_dir` derived from
// [resolveDownloadDirectory] (Settings.DestinationDirectory wins;
// else GetDownloadConfiguration.DecodedDownloadDirectory +
// `/<Category>` when Settings.Category is non-empty). The
// post-add SetTorrentSettings PUT applies AddPaused + the per-grab
// queue-first selection + the seed-ratio (from req.SeedConfiguration).
//
// Returns the new task ID. Empty add response surfaces an error.
func (f *TorrentFreeboxDownload) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("freebox: nil GrabRequest")
	}
	if f.proxy == nil {
		return "", errors.New("freebox: nil Proxy")
	}

	dir, err := f.resolveDownloadDirectory(ctx)
	if err != nil {
		return "", err
	}
	dirEncoded := ""
	if dir != "" {
		dirEncoded = base64.StdEncoding.EncodeToString([]byte(dir))
	}
	addFirst := f.shouldQueueFirst(req)
	ratio := computeSeedRatio(req)

	switch {
	case len(req.TorrentBody) > 0:
		title := strings.TrimSpace(req.ReleaseTitle)
		if title == "" {
			return "", errors.New("freebox: GrabRequest.ReleaseTitle is empty (required for the torrent-file filename)")
		}
		filename := title + ".torrent"
		id, err := f.proxy.AddTaskFromFile(ctx, f.settings, filename, req.TorrentBody, dirEncoded, f.settings.AddPaused, addFirst, ratio)
		if err != nil {
			return "", fmt.Errorf("freebox: AddTaskFromFile(%q): %w", filename, err)
		}
		return id, nil

	case strings.TrimSpace(req.MagnetURL) != "":
		id, err := f.proxy.AddTaskFromURL(ctx, f.settings, req.MagnetURL, dirEncoded, f.settings.AddPaused, addFirst, ratio)
		if err != nil {
			return "", fmt.Errorf("freebox: AddTaskFromURL: %w", err)
		}
		return id, nil

	default:
		return "", errors.New("freebox: GrabRequest has neither TorrentBody nor MagnetURL")
	}
}

// resolveDownloadDirectory mirrors C# `GetDownloadDirectory` at
// `:189-204`. DestinationDirectory wins (trailing `/` trimmed);
// else [Proxy.GetDownloadConfiguration].DecodedDownloadDirectory
// is fetched; when Category is non-empty, `<base>/<Category>` is
// composed. The Provider returns the UTF-8 plaintext; the Download
// dispatch base64-encodes before posting.
func (f *TorrentFreeboxDownload) resolveDownloadDirectory(ctx context.Context) (string, error) {
	if d := strings.TrimSpace(f.settings.DestinationDirectory); d != "" {
		return strings.TrimRight(d, "/"), nil
	}
	cfg, err := f.proxy.GetDownloadConfiguration(ctx, f.settings)
	if err != nil {
		return "", fmt.Errorf("freebox: resolve download directory: %w", err)
	}
	base := strings.TrimRight(cfg.DecodedDownloadDirectory(), "/")
	if strings.TrimSpace(f.settings.Category) == "" {
		return base, nil
	}
	if base == "" {
		return f.settings.Category, nil
	}
	return base + "/" + f.settings.Category, nil
}

// shouldQueueFirst ports C# `ToBeQueuedFirst` at `:211-220` —
// IsRecent maps to RecentPriority, !IsRecent to OlderPriority.
// Both compare against [PriorityFirst].
func (f *TorrentFreeboxDownload) shouldQueueFirst(req *downloadclients.GrabRequest) bool {
	if req.IsRecent && f.settings.RecentPriority == PriorityFirst {
		return true
	}
	if !req.IsRecent && f.settings.OlderPriority == PriorityFirst {
		return true
	}
	return false
}

// computeSeedRatio ports C# `GetSeedRatio` at `:222-230`. Returns
// nil when req.SeedConfiguration is nil OR SeedConfiguration.Ratio
// is nil/zero. Otherwise returns ratio * 100 (matches C# `:229`'s
// `Ratio.Value * 100` since Freebox expresses stop_ratio as a
// percentage rather than the fractional form the C# typed
// SeedConfiguration uses).
func computeSeedRatio(req *downloadclients.GrabRequest) *float64 {
	if req.SeedConfiguration == nil || req.SeedConfiguration.Ratio == nil {
		return nil
	}
	v := *req.SeedConfiguration.Ratio * 100
	return &v
}

// GetItems ports `TorrentFreeboxDownload.GetItems` at C# `:44-128`.
// Walks the task list via [Proxy.GetTasks], filters to BT-type
// tasks, applies the DestinationDirectory / Category path gates,
// and projects each task into a [downloadclients.Item] using the
// 6-arm status table from C# `:85-120`.
//
// Path gates (priority order — first miss skips the row):
//
//   - When Settings.DestinationDirectory is non-empty: skip
//     torrents whose DecodedDownloadDirectory is not at-or-under
//     DestinationDirectory (matches C# `:54-60`).
//   - When Settings.Category is non-empty: skip torrents whose
//     decoded-directory path segments don't contain the Category
//     name (matches C# `:62-70`).
//
// Status table (matches C# `:85-120`):
//
//   - Stopped / Stopping → Paused.
//   - Queued → Queued.
//   - Starting / Downloading / Retry / Checking → Downloading.
//   - Error → Warning + Message = ErrorDescription (decoded via
//     the C# error-code table).
//   - Done / Seeding → Completed.
//   - Unknown / default → Downloading + "Unknown download state"
//     Message (matches C# `:115-118`).
//
// CanMoveFiles + CanBeRemoved both flip true when
// RemoveCompletedDownloads AND Status==Done (matches C# `:122`).
func (f *TorrentFreeboxDownload) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if f.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	tasks, err := f.proxy.GetTasks(ctx, f.settings)
	if err != nil {
		return nil, fmt.Errorf("freebox: GetTasks: %w", err)
	}
	info := f.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(tasks))
	for _, task := range tasks {
		if task == nil || !strings.EqualFold(task.Type, "bt") {
			continue
		}
		if !passesPathGate(task, f.settings) {
			continue
		}
		out = append(out, f.toItem(ctx, info, task))
	}
	return out, nil
}

// toItem projects one [Task] into a [downloadclients.Item].
func (f *TorrentFreeboxDownload) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, task *Task) *downloadclients.Item {
	dir := task.DecodedDownloadDirectory()
	output := ospath.New(dir)
	if f.rpm != nil && strings.TrimSpace(dir) != "" {
		if mapped, err := f.rpm.RemapRemoteToLocal(ctx, f.settings.Host, output); err == nil {
			output = mapped
		} else {
			f.log.Debug("freebox: RemapRemoteToLocal failed", "directory", dir, "err", err)
		}
	}

	// RemainingSize: Size * (1 - ReceivedPrct/10000). ReceivedPrct is
	// per-myriad (basis-points × 100); divide by 10000 to get the
	// fractional progress.
	remaining := int64(float64(task.Size) * (1 - float64(task.ReceivedPrct)/10000))
	if remaining < 0 {
		remaining = 0
	}

	var ratio float64
	if task.StopRatio > 0 {
		ratio = float64(task.StopRatio) / 100
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		Category:           f.settings.Category,
		DownloadID:         task.ID,
		Title:              task.Name,
		TotalSize:          task.Size,
		RemainingSize:      remaining,
		OutputPath:         output,
		SeedRatio:          &ratio,
	}
	if task.Eta > 0 {
		d := time.Duration(task.Eta) * time.Second
		item.RemainingTime = &d
	}

	switch task.Status {
	case TaskStatusStopped, TaskStatusStopping:
		item.Status = downloadclients.DownloadItemStatusPaused
	case TaskStatusQueued:
		item.Status = downloadclients.DownloadItemStatusQueued
	case TaskStatusStarting, TaskStatusDownloading, TaskStatusRetry, TaskStatusChecking:
		item.Status = downloadclients.DownloadItemStatusDownloading
	case TaskStatusError:
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = task.ErrorDescription()
	case TaskStatusDone, TaskStatusSeeding:
		item.Status = downloadclients.DownloadItemStatusCompleted
	default:
		item.Message = "Unknown download state"
		item.Status = downloadclients.DownloadItemStatusDownloading
	}

	if info != nil && info.RemoveCompletedDownloads && task.Status == TaskStatusDone {
		item.CanMoveFiles = true
		item.CanBeRemoved = true
	}
	return item
}

// passesPathGate enforces the C# DestinationDirectory + Category
// filters at `:54-70`.
func passesPathGate(task *Task, s Settings) bool {
	decoded := task.DecodedDownloadDirectory()
	if dest := strings.TrimSpace(s.DestinationDirectory); dest != "" {
		wanted := strings.TrimRight(dest, "/")
		got := strings.TrimRight(decoded, "/")
		if got != wanted && !strings.HasPrefix(got, wanted+"/") {
			return false
		}
	}
	if cat := strings.TrimSpace(s.Category); cat != "" {
		segs := strings.FieldsFunc(decoded, func(r rune) bool { return r == '/' || r == '\\' })
		found := false
		for _, seg := range segs {
			if seg == cat {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (f *TorrentFreeboxDownload) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: f.Protocol(),
		Type:     Implementation,
		Name:     f.Name(),
	}
	if f.def != nil {
		info.ID = f.def.ID
		info.Name = f.def.Name
		info.RemoveCompletedDownloads = f.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional remote-path mapper. Same
// Phase 6.B pattern as the other DCs.
func (f *TorrentFreeboxDownload) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	f.rpm = rpm
}

// GetImportItem is the C# base passthrough.
func (f *TorrentFreeboxDownload) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `TorrentFreeboxDownload.RemoveItem` at C#
// `:151-154`. Delegates to [Proxy.DeleteTask] — DELETE
// `/downloads/<id>` (deleteData=false) or `/downloads/<id>/erase`
// (deleteData=true; the `/erase` suffix tells Freebox to also
// remove the downloaded files from disk).
func (f *TorrentFreeboxDownload) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if f.proxy == nil {
		return errors.New("freebox: nil Proxy")
	}
	if item == nil {
		return errors.New("freebox: nil item")
	}
	id := strings.TrimSpace(item.DownloadID)
	if id == "" {
		return errors.New("freebox: item has empty DownloadID")
	}
	if err := f.proxy.DeleteTask(ctx, f.settings, id, deleteData); err != nil {
		return fmt.Errorf("freebox: DeleteTask(%s): %w", id, err)
	}
	return nil
}

// GetStatus ports `TorrentFreeboxDownload.GetStatus` at C#
// `:156-165` end-to-end. Resolves the configured destination
// directory via [resolveDownloadDirectory] (Settings.DestinationDirectory
// wins; else GetDownloadConfiguration.DecodedDownloadDirectory +
// optional `/<Category>`) and populates OutputRootFolders with the
// RemotePathMapper-translated result.
//
// Config-probe failures are NON-fatal: GetStatus swallows the error
// and returns the IsLocalhost-only ClientInfo so the queue
// healthcheck still surfaces the configured host. Same posture as
// the Hadouken / DownloadStation GetStatus implementations.
func (f *TorrentFreeboxDownload) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	info := &downloadclients.ClientInfo{IsLocalhost: downloadclients.IsLocalhostHost(f.settings.Host)}
	if f.proxy == nil {
		return info, nil
	}
	dir, err := f.resolveDownloadDirectory(ctx)
	if err != nil {
		f.log.Debug("freebox: GetStatus resolveDownloadDirectory failed", "err", err)
		return info, nil
	}
	if strings.TrimSpace(dir) == "" {
		return info, nil
	}
	remote := ospath.New(dir)
	if f.rpm != nil {
		if mapped, mapErr := f.rpm.RemapRemoteToLocal(ctx, f.settings.Host, remote); mapErr == nil {
			remote = mapped
		} else {
			f.log.Debug("freebox: GetStatus RemapRemoteToLocal failed", "directory", dir, "err", mapErr)
		}
	}
	info.OutputRootFolders = append(info.OutputRootFolders, remote)
	return info, nil
}

// MarkItemAsImported is the C# base default.
func (f *TorrentFreeboxDownload) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (f *TorrentFreeboxDownload) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `TorrentFreeboxDownload.Test(failures)` (C#
// `:170-185`). Runs Settings.Validate, then drives the full
// session-token flow via [Proxy.GetSessionToken] — this exercises
// /login (challenge fetch) AND /login/session (HMAC-SHA1
// exchange), proving the AppId + AppToken combo works end-to-end.
//
// J107 alignment with C# `Test` at `TorrentFreeboxDownload.cs:167-187`:
//   - ErrUnavailable surfaces as BOTH Host AND Port failures (C# :175-176)
//     — the user sees both fields highlighted in the v3 UI.
//   - ErrAuth surfaces as BOTH AppId AND AppToken failures (C# :179-181)
//     — the credentials may be malformed at either field, so both
//     light up.
//   - ErrAPI (404) surfaces as ApiUrl (C# :184-186).
//   - ErrRPC + default surface as ApiUrl-pinned generic-error so
//     the user knows it's the configured-URL family rather than a
//     transport issue (the C# `FreeboxDownloadException` catch is
//     for the application-level error envelope decoded by the
//     proxy).
func (f *TorrentFreeboxDownload) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := f.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if _, err := f.proxy.GetSessionToken(ctx, f.settings, true); err != nil {
		msg := err.Error()
		switch {
		case errors.Is(err, ErrAuth):
			return []downloadclients.TestFailure{
				{PropertyName: "AppId", ErrorMessage: msg},
				{PropertyName: "AppToken", ErrorMessage: msg},
			}, nil
		case errors.Is(err, ErrAPI):
			return []downloadclients.TestFailure{{
				PropertyName: "ApiUrl",
				ErrorMessage: fmt.Sprintf("Freebox API not found at the configured URL: %v", err),
			}}, nil
		case errors.Is(err, ErrUnavailable):
			return []downloadclients.TestFailure{
				{PropertyName: "Host", ErrorMessage: msg},
				{PropertyName: "Port", ErrorMessage: msg},
			}, nil
		default:
			return []downloadclients.TestFailure{{
				PropertyName: "ApiUrl",
				ErrorMessage: fmt.Sprintf("Freebox API error: %v", err),
			}}, nil
		}
	}
	return nil, nil
}

// Compile-time guard.
var _ downloadclients.Provider = (*TorrentFreeboxDownload)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("freebox: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("freebox: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	f := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		f.SetRemotePathMapper(rpm)
	}
	return f, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
