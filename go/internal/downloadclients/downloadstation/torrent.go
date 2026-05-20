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
// Functionally complete at 5.J78 against the V1 task surface:
// Download dispatches torrent-file (multipart upload via
// [Proxy.AddTaskFromData]) vs magnet (via [Proxy.AddTaskFromURL]);
// GetItems walks the BT-filtered task list with the path gate +
// 5-arm status table + SeedRatio + per-item cleanup gate; Test
// runs the 4-arm chain; RemoveItem delegates to [Proxy.RemoveTask].
// GetStatus is IsLocalhost-only — the SharedFolderResolver arm
// that populates OutputRootFolders is the only deferred piece (plus
// the V1/V2 selector for DSM 7+ which Phase 6 will layer on top).
type TorrentDownloadStation struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy

	// rpm is the optional remote-path mapper consulted by
	// GetItems to translate the DSM-side `destination` path into
	// Sonarr-side paths. Nil → pass-through.
	rpm downloadclients.RemotePathMapper

	// resolver translates DSM SharedFolder-prefixed paths
	// (`/data/tv/Show`) to physical paths (`/volume1/data/tv/Show`)
	// via [SharedFolderResolver]. Auto-bound by New when a proxy is
	// available; tests may override via [SetSharedFolderResolver].
	// Slice 5.J122 wire-up.
	resolver *SharedFolderResolver
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
	t := &TorrentDownloadStation{settings: settings, def: def, log: log, proxy: proxy}
	// Auto-bind the SharedFolderResolver when a real proxy is
	// wired. Tests that pin a stubbed Doer get the resolver "for
	// free" — the resolver only fires its proxy round-trips when
	// Resolve is called, which itself only happens when the
	// GetStatus arm finds a non-empty TvDirectory. Tests that need
	// to override the resolver pin call [SetSharedFolderResolver].
	if proxy != nil {
		t.resolver = NewSharedFolderResolver(proxy)
	}
	return t
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

// Download ports C# `TorrentDownloadStation.cs:166-204` end-to-end.
// Dispatches on the GrabRequest body:
//
//   - TorrentBody non-empty → [Proxy.AddTaskFromData] (multipart
//     POST of the .torrent bytes; mirrors C#
//     `AddFromTorrentFile` at `:185-204`). Post-add lookup matches
//     `Additional.Detail["uri"]` against the ReleaseTitle (the
//     extension-less form matching C# `Path.GetFileNameWithoutExtension(filename)`
//     at `:191`).
//   - MagnetURL non-empty → [Proxy.AddTaskFromURL] (mirrors C#
//     `AddFromMagnetLink` at `:166-183`). Post-add lookup matches
//     against the magnet URL verbatim (C# `:172`).
//
// Returns the resolved Task.ID. The SerialNumberProvider-prefixed
// DownloadID composition at C# `:435-438` (`<serial>:<id>`) is
// deferred — the bare task ID still works as a queue join key
// since GetItems applies the same composition.
func (t *TorrentDownloadStation) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("downloadstation: nil GrabRequest")
	}
	if t.proxy == nil {
		return "", errors.New("downloadstation: nil Proxy")
	}
	dest := downloadDirectory(t.settings)

	switch {
	case len(req.TorrentBody) > 0:
		title := strings.TrimSpace(req.ReleaseTitle)
		if title == "" {
			return "", errors.New("downloadstation: GrabRequest.ReleaseTitle is empty (required for the torrent-file upload)")
		}
		filename := title + ".torrent"
		if err := t.proxy.AddTaskFromData(ctx, t.settings, req.TorrentBody, filename, dest); err != nil {
			return "", fmt.Errorf("downloadstation: AddTaskFromData(%q): %w", filename, err)
		}
		return t.findAddedTask(ctx, title) // matches C# `:191` extension-less lookup

	case strings.TrimSpace(req.MagnetURL) != "":
		magnet := req.MagnetURL
		if err := t.proxy.AddTaskFromURL(ctx, t.settings, magnet, dest); err != nil {
			return "", fmt.Errorf("downloadstation: AddTaskFromURL: %w", err)
		}
		return t.findAddedTask(ctx, magnet)

	default:
		return "", errors.New("downloadstation: GrabRequest has neither TorrentBody nor MagnetURL")
	}
}

// findAddedTask re-fetches the task list after an add call and
// returns the BT task whose `Additional.Detail["uri"]` matches the
// supplied identifier. The C# code uses `SingleOrDefault` —
// duplicate matches surface a "failed to add" error here to mirror
// that strictness.
func (t *TorrentDownloadStation) findAddedTask(ctx context.Context, uri string) (string, error) {
	tasks, err := t.proxy.GetTasks(ctx, t.settings)
	if err != nil {
		return "", fmt.Errorf("downloadstation: post-add GetTasks: %w", err)
	}
	var match *Task
	for _, task := range tasks {
		if task == nil || !strings.EqualFold(task.Type, "BT") {
			continue
		}
		if task.Additional.Detail["uri"] == uri {
			if match != nil {
				return "", fmt.Errorf("downloadstation: multiple BT tasks match uri %q", uri)
			}
			match = task
		}
	}
	if match == nil {
		return "", fmt.Errorf("downloadstation: add succeeded but no matching BT task found for uri %q", uri)
	}
	return match.ID, nil
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

// SetSharedFolderResolver overrides the SharedFolderResolver that
// GetStatus (+ future TestOutputPath + deleteData disk arm) consult
// to translate DSM SharedFolder-prefixed paths to physical paths.
// nil disables the resolver — GetStatus falls back to IsLocalhost-only.
// Slice 5.J122.
func (t *TorrentDownloadStation) SetSharedFolderResolver(r *SharedFolderResolver) {
	t.resolver = r
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

// GetStatus ports C# `TorrentDownloadStation.GetStatus` at `:118-142`.
//
// IsLocalhost matches C# `:132` — 127.0.0.1 / ::1 / localhost.
// OutputRootFolders wires the J121 SharedFolderResolver: when
// Settings.TvDirectory is non-empty, the configured path is fed
// through [SharedFolderResolver.Resolve] to translate the DSM
// SharedFolder prefix to a physical mount path; the result is then
// routed through [RemotePathMapper.RemapRemoteToLocal] (when wired)
// to surface a Sonarr-side path.
//
// The C# tree at `:122` also resolves a hashed serial number for
// the resolver cache key, but the Go port keys on Host (J121
// divergence note) so the serialNumber dance isn't needed.
//
// Failures from the resolver are NON-fatal — GetStatus returns
// IsLocalhost-only when the proxy fails to fetch the SharedFolder
// mapping. Same posture as the other DCs' GetStatus arms.
//
// The TvCategory-only path (TvDirectory empty + TvCategory set)
// is still deferred: it requires DSMInfoProxy.GetDefaultDestination
// to resolve the DSM-side default folder, which hasn't ported.
// Documented; the GetStatus path stays IsLocalhost-only when only
// TvCategory is configured.
func (t *TorrentDownloadStation) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	info := &downloadclients.ClientInfo{IsLocalhost: isLocalHost(t.settings.Host)}
	if t.resolver == nil {
		return info, nil
	}
	dir := strings.TrimSpace(t.settings.TvDirectory)
	if dir == "" {
		return info, nil
	}
	// C# at `:124-126` prefixes a leading `/` because Download
	// Station returns paths without it but expects it on the way
	// in. The Go port mirrors so the resolver's IndexOf-after-1
	// shared-folder extraction works the same way.
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	resolved, err := t.resolver.Resolve(ctx, t.settings, dir)
	if err != nil {
		t.log.Debug("downloadstation: GetStatus resolver failed; degrading to IsLocalhost-only",
			slog.String("dir", dir),
			slog.Any("err", err))
		return info, nil
	}
	if t.rpm != nil {
		if mapped, err := t.rpm.RemapRemoteToLocal(ctx, t.settings.Host, resolved); err == nil {
			resolved = mapped
		} else {
			t.log.Debug("downloadstation: GetStatus RemapRemoteToLocal failed",
				slog.String("dir", resolved.FullPath()),
				slog.Any("err", err))
		}
	}
	info.OutputRootFolders = []ospath.OsPath{resolved}
	return info, nil
}

// MarkItemAsImported is the C# base default.
func (t *TorrentDownloadStation) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (t *TorrentDownloadStation) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports the connection + auth + version + task-list arms of
// C# `Test(failures)` at `:206-216` PLUS (slice 5.J123) the
// TestOutputPath arm at C# `:309-357` validating the configured
// TvDirectory against the SYNO.FileStation API.
//
// Algorithm: Settings.Validate first; then TestConnection
// (auth-probe); then ValidateVersion (API >= 2); then
// TestGetTorrents (live task-list probe); then TestOutputPath
// (folder-validity probe). Each arm short-circuits on failure
// matching the C# `HasErrors` guard between probes.
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
	// TestOutputPath arm (slice 5.J123, ports C# :309-357). Only
	// fires when TvDirectory is configured — the C#
	// `GetDownloadDirectory == null` arm at :313-321 (NoDefaultDestination)
	// requires DSMInfoProxy.GetDefaultDestination which isn't ported.
	if f := t.testOutputPath(ctx); f != nil {
		return []downloadclients.TestFailure{*f}, nil
	}
	return nil, nil
}

// testOutputPath ports the body of C# `TorrentDownloadStation.TestOutputPath`
// at `:309-357`. Returns nil when no TvDirectory is configured
// (Go-port narrowing — the C# also handles TvCategory-only via
// GetDefaultDir but that requires DSMInfoProxy which isn't ported).
//
// Probes [Proxy.GetFileInfo] on `/{TvDirectory}` to verify:
//   - The path resolves (FileStation returns non-empty Additional).
//   - The path is a directory (IsDir==true).
//
// Property-name selection: C# `:324` picks "TvDirectory" when set,
// "TvCategory" otherwise. Since the Go port only probes when
// TvDirectory IS set, all failures pin to TvDirectory.
//
// FileStation transport failures degrade to nil so a transiently
// unreachable Synology box during Test doesn't double-surface a
// failure already captured by the TestGetTorrents arm above
// (matches C# `:346-356` catch-arms which surface as empty-field
// failures rather than rethrows — the Go port simplifies).
func (t *TorrentDownloadStation) testOutputPath(ctx context.Context) *downloadclients.TestFailure {
	tv := strings.TrimSpace(t.settings.TvDirectory)
	if tv == "" {
		return nil
	}
	if !strings.HasPrefix(tv, "/") {
		tv = "/" + tv
	}
	info, err := t.proxy.GetFileInfo(ctx, t.settings, tv)
	if err != nil {
		t.log.Debug("downloadstation: TestOutputPath GetFileInfo failed; degrading to silent",
			slog.String("path", tv),
			slog.Any("err", err))
		return nil
	}
	if len(info.Additional) == 0 {
		return &downloadclients.TestFailure{
			PropertyName:        "TvDirectory",
			ErrorMessage:        fmt.Sprintf("Shared folder %q not found on the Diskstation", firstPathComponent(tv)),
			DetailedDescription: fmt.Sprintf("Sonarr couldn't find the shared folder %q on the Diskstation. Open Synology Control Panel > Shared Folder and verify it exists.", firstPathComponent(tv)),
		}
	}
	if !info.IsDir {
		return &downloadclients.TestFailure{
			PropertyName:        "TvDirectory",
			ErrorMessage:        fmt.Sprintf("Folder %q does not exist on the Diskstation", tv),
			DetailedDescription: fmt.Sprintf("The configured TvDirectory %q resolves to a file or non-existent path under shared folder %q.", tv, firstPathComponent(tv)),
		}
	}
	return nil
}

// firstPathComponent returns the first path component (the
// SharedFolder name) of the supplied absolute path. Matches the C#
// `:323` `downloadDir.Split('\\', '/')[0]` heuristic with a leading-
// slash strip first.
func firstPathComponent(path string) string {
	trimmed := strings.TrimLeft(path, "/")
	if i := strings.IndexAny(trimmed, "/\\"); i != -1 {
		return trimmed[:i]
	}
	return trimmed
}

// isLocalHost ports the C# `Settings.Host == "127.0.0.1" || ==
// "::1" || == "localhost"` check.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

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
