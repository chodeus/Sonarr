package sabnzbd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the SAB download client. Mirrors the C# class names
// `Sabnzbd` + `SabnzbdSettings` — the v3/v4/v5 React UI posts these
// strings in the DownloadClient row's Implementation +
// ConfigContract columns, and the registry keys on the same pair.
const (
	Implementation = "Sabnzbd"
	ConfigContract = "SabnzbdSettings"
)

// Sabnzbd is the [downloadclients.Provider] implementation for the
// SABnzbd usenet client. Ports C# `Sabnzbd` at
// `src/NzbDrone.Core/Download/Clients/Sabnzbd/Sabnzbd.cs:20-594`.
//
// Sub-arc 14a scope: Name / Protocol / Definition / Download / Test
// are live. GetItems / GetStatus / RemoveItem / GetImportItem /
// MarkItemAsImported return [downloadclients.ErrNotImplementedYet]
// (a typed sentinel that the orchestrator treats as a partial
// implementation — log + skip vs hard failure). Sub-arc 14b will
// finish those methods once the C# behavioural shape is replayed
// against recorded test fixtures.
type Sabnzbd struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	rpm      downloadclients.RemotePathMapper
	disk     downloadclients.DiskProvider
	log      *slog.Logger
}

// New constructs a Sabnzbd Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer. Remote-path mapping is bound via
// [Sabnzbd.SetRemotePathMapper] after construction — keeps the
// signature stable while letting tests opt out (nil mapper degrades
// to "no remap" at every OutputPath site).
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Sabnzbd {
	if log == nil {
		log = slog.Default()
	}
	return &Sabnzbd{settings: settings, proxy: proxy, def: def, log: log}
}

// SetRemotePathMapper binds the [downloadclients.RemotePathMapper]
// the historyItemToItem and GetStatus paths consult to translate
// SAB-side absolute paths back to Sonarr-side paths (for off-host
// SAB instances where the storage directory the SAB user wrote to
// isn't mounted at the same path on the Sonarr host). Optional —
// when nil (test harnesses, single-host setups) the raw SAB-side
// path is emitted unchanged. Mirrors C# `Sabnzbd.cs:160-176,265-275`
// where the C# RemotePathMappingService is consulted at the same
// two sites. Slice 6.B3 (Phase 6.B wire-up arc).
//
// Returns the receiver for chainable construction.
func (s *Sabnzbd) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) *Sabnzbd {
	s.rpm = rpm
	return s
}

// SetDiskProvider binds the optional [downloadclients.DiskProvider]
// the RemoveItem deleteData arm consults to call DeleteFolder
// against item.OutputPath when SAB has moved the item to history
// (matches the C# DeleteItemData step at `Sabnzbd.cs:203-205`,
// which fires BEFORE the SAB-side `del_files=1` cleanup). Nil
// binding leaves the disk arm in its pre-J116 degraded state — the
// SAB-side cleanup via del_files still runs, but Sonarr won't
// remove a folder SAB has lost track of (moved by SAB post-import).
// Slice 5.J116 — same pattern as J111 rTorrent + J112 Aria2 +
// NZBGet 6.C3.
//
// Returns the receiver for chainable construction.
func (s *Sabnzbd) SetDiskProvider(d downloadclients.DiskProvider) *Sabnzbd {
	s.disk = d
	return s
}

// Name returns the user-facing label. Mirrors C# `Sabnzbd.Name`
// at `:184`.
func (s *Sabnzbd) Name() string { return "SABnzbd" }

// Protocol returns the protocol family. Mirrors C#
// `UsenetClientBase.Protocol` at `UsenetClientBase.cs:36`.
func (s *Sabnzbd) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClients row. Mirrors C#
// `DownloadClientBase.Definition` at `:70`.
func (s *Sabnzbd) Definition() *downloadclients.DownloadClient { return s.def }

// Download fires the SAB `addfile` API with the release's NZB
// contents. Mirrors C# `Sabnzbd.AddFromNzbFile` at `:40-53` plus the
// `UsenetClientBase.Download` wrapping at `UsenetClientBase.cs:40-91`.
//
// The Go port skips the C# nzb-validation step (IValidateNzbs.cs)
// — it's a follow-up. The body is whatever the caller hands in via
// req.NzbBody; production callers (queue [15] orchestrator) will
// fetch the URL and pass the bytes here.
//
// Returns the SAB-assigned nzo_id on success, or a wrapped error.
func (s *Sabnzbd) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("sabnzbd: Download: nil request")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("sabnzbd: Download: empty NzbBody — fetcher must populate before invoking provider")
	}
	priority := s.settings.RecentTvPriority
	if !req.IsRecent {
		priority = s.settings.OlderTvPriority
	}
	filename := sanitizeFilename(req.ReleaseTitle) + ".nzb"
	id, err := s.proxy.DownloadNzb(ctx, req.NzbBody, filename, s.settings.TvCategory, priority, s.settings)
	if err != nil {
		return "", fmt.Errorf("sabnzbd: Download(%q): %w", req.ReleaseTitle, err)
	}
	return id, nil
}

// defaultHistoryLimit caps SAB's GetHistory page size. C# pulls this
// from `IConfigService.DownloadClientHistoryLimit` (HostConfig
// keyvalues, default 30); the Go port hard-codes the default until
// HostConfig keyvalues ports.
const defaultHistoryLimit = 30

// GetItems ports C# `Sabnzbd.GetItems` at `Sabnzbd.cs:186-195` — the
// union of queue + history, filtered to items in the configured
// TvCategory (or the SAB "*" wildcard when TvCategory is unset).
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (s *Sabnzbd) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	queue, err := s.proxy.GetQueue(ctx, 0, 0, s.settings)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: GetItems: GetQueue: %w", err)
	}
	history, err := s.proxy.GetHistory(ctx, 0, defaultHistoryLimit, s.settings)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: GetItems: GetHistory: %w", err)
	}

	info := s.itemClientInfo()
	wantCat := strings.TrimSpace(s.settings.TvCategory)
	out := make([]*downloadclients.Item, 0, len(queue.Items)+len(history.Items))

	for _, q := range queue.Items {
		if !sabCategoryMatches(q.Category, wantCat) {
			continue
		}
		if q.Status == SabStatusDeleted {
			continue
		}
		out = append(out, queueItemToItem(q, queue.Paused, info))
	}
	for _, h := range history.Items {
		if !sabCategoryMatches(h.Category, wantCat) {
			continue
		}
		if h.Status == SabStatusDeleted {
			continue
		}
		out = append(out, s.historyItemToItem(ctx, h, info))
	}
	return out, nil
}

// remapOutput translates a SAB-side absolute path back to a
// Sonarr-side path via the wired [downloadclients.RemotePathMapper].
// Returns the raw path unchanged when the mapper is nil, when the
// input is empty, or when the mapper returns an error (the error is
// logged at debug — a remap failure shouldn't fail the calling
// GetItems / GetStatus call; the C# code at
// `RemotePathMappingService.cs:128-157` also returns the raw path on
// no-match without surfacing an error).
func (s *Sabnzbd) remapOutput(ctx context.Context, raw string) ospath.OsPath {
	if raw == "" {
		return ospath.OsPath{}
	}
	remote := ospath.New(raw)
	if s.rpm == nil {
		return remote
	}
	local, err := s.rpm.RemapRemoteToLocal(ctx, s.settings.Host, remote)
	if err != nil {
		s.log.Debug("sabnzbd: RemapRemoteToLocal failed; using raw path",
			slog.String("host", s.settings.Host),
			slog.String("path", raw),
			slog.Any("err", err))
		return remote
	}
	return local
}

// sabCategoryMatches mirrors the C# predicate at `Sabnzbd.cs:190`:
// item.Category == TvCategory OR (item.Category == "*" AND TvCategory is blank).
func sabCategoryMatches(itemCategory, wantCategory string) bool {
	if itemCategory == wantCategory {
		return true
	}
	if itemCategory == "*" && wantCategory == "" {
		return true
	}
	return false
}

// queueItemToItem ports the per-item projection at `Sabnzbd.cs:60-103`.
// IsEncrypted detection (the "ENCRYPTED /" prefix) and the Paused
// vs Queued vs Downloading status arms are preserved.
func queueItemToItem(q SabnzbdQueueItem, queuePaused bool, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	title := q.Title
	isEncrypted := false
	if strings.HasPrefix(title, "ENCRYPTED /") {
		title = title[len("ENCRYPTED /"):]
		isEncrypted = true
	}
	timeLeft := q.Timeleft.Duration()
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         q.ID,
		Category:           q.Category,
		Title:              title,
		TotalSize:          int64(q.SizeMB * 1024 * 1024),
		RemainingSize:      int64(q.SizeLeftMB * 1024 * 1024),
		IsEncrypted:        isEncrypted,
		CanBeRemoved:       true,
		CanMoveFiles:       true,
	}
	// Status. Mirrors the C# branches at `:78-94`.
	switch {
	case (queuePaused && !q.IsPriorityForce()) || q.Status == SabStatusPaused:
		item.Status = downloadclients.DownloadItemStatusPaused
		// Paused → no meaningful ETA.
	case q.Status == SabStatusQueued || q.Status == SabStatusGrabbing || q.Status == SabStatusPropagating:
		item.Status = downloadclients.DownloadItemStatusQueued
		item.RemainingTime = &timeLeft
	default:
		item.Status = downloadclients.DownloadItemStatusDownloading
		item.RemainingTime = &timeLeft
	}
	return item
}

// historyItemToItem ports the per-item projection at `Sabnzbd.cs:108-178`.
// The OutputPath is run through [Sabnzbd.remapOutput] so off-host
// SAB instances surface a Sonarr-side path (slice 6.B3); a nil
// mapper degrades to the raw SAB-side Storage path.
func (s *Sabnzbd) historyItemToItem(ctx context.Context, h SabnzbdHistoryItem, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	zero := time.Duration(0)
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         h.ID,
		Category:           h.Category,
		Title:              h.Title,
		TotalSize:          h.Size,
		RemainingSize:      0,
		RemainingTime:      &zero,
		Message:            h.FailMessage,
		CanBeRemoved:       true,
		CanMoveFiles:       true,
	}
	switch h.Status {
	case SabStatusFailed:
		// C# `:138-148` — the "Unpacking failed, write error or disk
		// is full?" message demotes Failed to Warning.
		if strings.EqualFold(h.FailMessage, "Unpacking failed, write error or disk is full?") {
			item.Status = downloadclients.DownloadItemStatusWarning
		} else {
			item.Status = downloadclients.DownloadItemStatusFailed
		}
	case SabStatusCompleted:
		item.Status = downloadclients.DownloadItemStatusCompleted
	default:
		// Verifying / Moving / Extracting etc.
		item.Status = downloadclients.DownloadItemStatusDownloading
	}
	// Slice 6.B3: route the SAB Storage path through remapOutput so
	// off-host SAB instances surface the Sonarr-side path. The C#
	// parent-walk step at `:163-176` (when the remap returns the same
	// path, walk parents until one resolves) is NOT yet ported — a
	// follow-up slice once the C# behavioural fixture is captured;
	// most deployments hit a configured RemotePathMappings row directly
	// without needing the parent walk.
	if h.Storage != "" {
		item.OutputPath = s.remapOutput(ctx, h.Storage)
	}
	return item
}

// itemClientInfo builds the ItemClientInfo stamp that every emitted
// Item carries. Mirrors C# `DownloadClientItemClientInfo.FromDownloadClient`
// at `DownloadClientItem.cs:33-56`. RemoveCompletedDownloads is the
// per-definition flag, defaulting to false (the HostConfig key isn't
// ported yet — the field on the persisted DownloadClient row would
// drive it once the v3 API exposes it).
func (s *Sabnzbd) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: s.Protocol(),
		Type:     Implementation,
		Name:     s.Name(),
	}
	if s.def != nil {
		info.ID = s.def.ID
		info.Name = s.def.Name
		info.RemoveCompletedDownloads = s.def.RemoveCompletedDownloads
	}
	return info
}

// GetImportItem mirrors C# `Sabnzbd` override at
// `Sabnzbd.cs:447-465`. Sub-arc 14b will port the re-fetch retry
// logic; the C# base default is "return input unchanged".
func (s *Sabnzbd) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	// Default fall-through matches C# `DownloadClientBase.GetImportItem`
	// at `:102-105`. The SAB-specific retry logic is sub-arc 14b.
	return item, nil
}

// RemoveItem ports C# `Sabnzbd.RemoveItem` at `Sabnzbd.cs:197-214`.
// Routes to queue/delete when the item is still in SAB's active
// queue, otherwise history/delete. deleteData controls whether SAB
// also deletes the partial files on disk.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (s *Sabnzbd) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if item == nil {
		return errors.New("sabnzbd: RemoveItem: nil item")
	}
	queue, err := s.proxy.GetQueue(ctx, 0, 0, s.settings)
	if err != nil {
		return fmt.Errorf("sabnzbd: RemoveItem: GetQueue: %w", err)
	}
	inQueue := false
	for _, q := range queue.Items {
		if q.ID == item.DownloadID {
			inQueue = true
			break
		}
	}
	if inQueue {
		if err := s.proxy.RemoveFromQueue(ctx, item.DownloadID, deleteData, s.settings); err != nil {
			return fmt.Errorf("sabnzbd: RemoveItem: RemoveFromQueue: %w", err)
		}
		return nil
	}
	// Not in queue → history. C# `:203-205`: when deleteData==true AND
	// status==Completed, fire DiskProvider.DeleteFolder against
	// item.OutputPath BEFORE the SAB history-delete (the C# ordering
	// — Sonarr handles the post-import-move folder that SAB has
	// lost track of, then SAB's own del_files arm cleans up whatever
	// SAB still knows about). Slice 5.J116 wire-up mirrors the
	// J111 rTorrent + J112 Aria2 disk-arm pattern.
	if deleteData && item.Status == downloadclients.DownloadItemStatusCompleted {
		switch {
		case s.disk == nil:
			s.log.Warn("sabnzbd: RemoveItem: deleteData=true requested on Completed item but no DiskProvider is wired; SAB's del_files arm will still run but Sonarr won't remove the post-import folder",
				slog.String("downloadID", item.DownloadID))
		case item.OutputPath.IsEmpty():
			s.log.Debug("sabnzbd: RemoveItem: deleteData=true but item.OutputPath is empty; skipping Sonarr-side delete",
				slog.String("downloadID", item.DownloadID))
		default:
			if err := s.disk.DeleteFolder(item.OutputPath.FullPath(), true); err != nil {
				s.log.Warn("sabnzbd: RemoveItem: DiskProvider.DeleteFolder failed; the history-delete will still run",
					slog.String("downloadID", item.DownloadID),
					slog.String("path", item.OutputPath.FullPath()),
					slog.Any("err", err))
			}
		}
	}
	deletePermanently := item.Status == downloadclients.DownloadItemStatusFailed
	if err := s.proxy.RemoveFromHistory(ctx, item.DownloadID, deleteData, deletePermanently, s.settings); err != nil {
		return fmt.Errorf("sabnzbd: RemoveItem: RemoveFromHistory: %w", err)
	}
	return nil
}

// GetStatus ports C# `Sabnzbd.GetStatus` at `:246-284`. Resolves the
// per-instance ClientInfo: IsLocalhost, SortingMode (TV/Movie/Date
// when the user's TvCategory falls under SAB's sorter rules), and
// OutputRootFolders (the absolute path SAB writes completed
// downloads into).
//
// Slice 6.B3 routed OutputRootFolders through [Sabnzbd.remapOutput]
// so the resolved SAB-side complete_dir surfaces as the Sonarr-side
// path when a RemotePathMappings row matches; nil mapper / no match
// → raw SAB-side path.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (s *Sabnzbd) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	cfg, err := s.proxy.GetConfig(ctx, s.settings)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: GetStatus: GetConfig: %w", err)
	}
	categories, err := s.resolveCategories(ctx, cfg)
	if err != nil {
		return nil, err
	}

	info := &downloadclients.ClientInfo{
		IsLocalhost: s.settings.Host == "127.0.0.1" || strings.EqualFold(s.settings.Host, "localhost"),
	}

	wantCat := s.settings.TvCategory
	var matched *SabnzbdCategory
	for i := range categories {
		if categories[i].Name == wantCat {
			matched = &categories[i]
			break
		}
	}
	if matched == nil {
		for i := range categories {
			if categories[i].Name == "*" {
				matched = &categories[i]
				break
			}
		}
	}

	if matched != nil {
		// SortingMode resolution: TV trumps Movie trumps Date — same
		// branch order the C# code uses at `:265-275`.
		switch {
		case (cfg.Misc.EnableTvSorting && containsCategory(cfg.Misc.TvCategories, wantCat)) ||
			anyActiveSorterCovers(cfg.Sorters, wantCat):
			info.SortingMode = "TV"
		case cfg.Misc.EnableMovieSorting && containsCategory(cfg.Misc.MovieCategories, wantCat):
			info.SortingMode = "Movie"
		case cfg.Misc.EnableDateSorting && containsCategory([]string(cfg.Misc.DateCategories), wantCat):
			info.SortingMode = "Date"
		}

		if matched.FullPath != "" {
			info.OutputRootFolders = []ospath.OsPath{s.remapOutput(ctx, matched.FullPath)}
		}
	}
	return info, nil
}

// resolveCategories ports C# `Sabnzbd.GetCategories` at `:216-244`.
// Resolves each category's FullPath against SAB's complete_dir,
// fetching SabnzbdFullStatus when the configured complete_dir is
// relative (SAB >= 2.0).
func (s *Sabnzbd) resolveCategories(ctx context.Context, cfg *SabnzbdConfig) ([]SabnzbdCategory, error) {
	completeDir := cfg.Misc.CompleteDir
	if !isAbsolutePath(completeDir) {
		// Fetch FullStatus to resolve the absolute complete_dir
		// (SAB >= 2.0 path). C# `:222-226`.
		status, err := s.proxy.GetFullStatus(ctx, s.settings)
		if err != nil {
			return nil, fmt.Errorf("sabnzbd: GetStatus: GetFullStatus: %w", err)
		}
		completeDir = status.CompleteDir
	}

	out := make([]SabnzbdCategory, len(cfg.Categories))
	for i, c := range cfg.Categories {
		c.FullPath = joinPath(completeDir, strings.TrimRight(c.Dir, "*"))
		out[i] = c
	}
	return out, nil
}

// MarkItemAsImported reports the SAB-doesn't-override case. C#
// `Sabnzbd` doesn't override the base class's `MarkItemAsImported`;
// the base `DownloadClientBase.MarkItemAsImported` is a no-op for
// clients without a post-import move endpoint. The Go port returns
// [downloadclients.ErrMarkAsImportedUnsupported] so the orchestrator
// can branch — a no-op fallback would silently miss the user's
// "moved after import" expectation.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (s *Sabnzbd) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// isAbsolutePath returns true when p is an OS-absolute filesystem
// path. Mirrors the C# `OsPath.IsRooted` check at `:220`. Tolerates
// both Unix-style (`/...`) and Windows-style (`C:\...`) prefixes
// without depending on the runtime OS.
func isAbsolutePath(p string) bool {
	if p == "" {
		return false
	}
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return false
}

// joinPath glues a relative tail onto a base path using whichever
// separator the base uses. Mirrors the OsPath `+` operator at C#
// `:240` without dragging in a full OsPath port.
func joinPath(base, tail string) string {
	if base == "" {
		return tail
	}
	if tail == "" {
		return base
	}
	sep := "/"
	if strings.ContainsRune(base, '\\') {
		sep = "\\"
	}
	b := strings.TrimRight(base, "/\\")
	t := strings.TrimLeft(tail, "/\\")
	return b + sep + t
}

// containsCategory reports whether haystack contains needle. Mirrors
// the C# `ContainsCategory` helper used at `Sabnzbd.cs:265-275`.
func containsCategory(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// anyActiveSorterCovers reports whether any active sorter lists the
// requested category. Mirrors the C# inline predicate at `:265`.
func anyActiveSorterCovers(sorters []SabnzbdSorter, category string) bool {
	for _, s := range sorters {
		if !s.IsActive {
			continue
		}
		if containsCategory(s.SortCategories, category) {
			return true
		}
	}
	return false
}

// Test runs the SAB connection + version self-check plus (slice
// 5.J103) the TestAuthentication + TestGlobalConfig + TestCategory
// follow-ups from C# `Sabnzbd.Test` at `:286-538`.
//
// Algorithm tracks C# `:286-292` literally:
//   - TestConnectionAndVersion at `:368-414` — Go-port wires this
//     via GetVersion + version-parse + version-floor (>= 0.7.0).
//   - TestAuthentication at `:416-438` — fires GetConfig and pins
//     "API Key Incorrect" / "API Key Required" errors to the
//     APIKey field rather than surfacing them as Host failures.
//   - TestGlobalConfig at `:440-453` — fails when SAB's
//     `misc.pre_check` is on AND the server is older than 1.1
//     (the pre-check job uploads stage was added in 1.1).
//   - TestCategory at `:455-489` — fails when the configured
//     TvCategory is missing OR has a Dir ending in '*' (job-folders
//     disabled).
//
// The C# tree shares ONE GetConfig call across TestAuthentication +
// TestGlobalConfig + TestCategory; the Go port follows by fetching
// once and threading the result through each validator.
//
// Returns the FluentValidation-style failure list (empty = healthy).
// A non-nil error is reserved for "test was aborted due to
// unexpected exception" matching C# `DownloadClientBase.Test`
// outer catch at `:152-159`.
func (s *Sabnzbd) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	var failures []downloadclients.TestFailure

	// First: surface Settings validation failures up front so the
	// user sees them without hitting SAB. Matches C#
	// FluentValidation pipeline ordering at `Test` entry.
	if vFails := s.settings.Validate(); len(vFails) > 0 {
		// Filter out warning-only failures (TvCategory) — the C#
		// pipeline surfaces those as warnings, not blockers. The
		// Go TestFailure shape doesn't yet distinguish; the
		// downstream UI consumer treats the empty-category case
		// as informational. For now, surface all.
		failures = append(failures, vFails...)
	}

	rawVersion, err := s.proxy.GetVersion(ctx, s.settings)
	if err != nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to connect to SABnzbd: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}

	version := parseVersion(rawVersion)
	if version == nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unknown SABnzbd version: %s", rawVersion),
		})
		return failures, nil
	}

	// Version-floor check at C# `:389-404`: SAB >= 0.7.0 required.
	// Major>=1 passes; Major==0 && Minor>=7 passes.
	if version.major < 1 && version.minor < 7 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("SABnzbd %s is too old; required version is 0.7.0 or newer", rawVersion),
		})
		return failures, nil
	}

	// J103 — TestAuthentication + TestGlobalConfig + TestCategory.
	// All three share a single GetConfig call. Authentication
	// errors get pinned to APIKey; other errors get the connection-
	// shape failure (matches C# rethrow at :434).
	cfg, err := s.proxy.GetConfig(ctx, s.settings)
	if err != nil {
		if f := classifyAuthError(err); f != nil {
			failures = append(failures, *f)
			return failures, nil
		}
		failures = append(failures, downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to fetch SABnzbd config: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}
	if f := testGlobalConfig(cfg, version); f != nil {
		failures = append(failures, *f)
	}
	if f := testCategory(cfg, s.settings.TvCategory); f != nil {
		failures = append(failures, *f)
	}

	return failures, nil
}

// classifyAuthError ports the C# `TestAuthentication` arm at
// `Sabnzbd.cs:416-438`. Returns an APIKey-pinned failure when the
// error message matches the SAB-server-emitted strings; nil for
// other errors so the caller surfaces them as Host failures.
func classifyAuthError(err error) *downloadclients.TestFailure {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "api key incorrect"):
		return &downloadclients.TestFailure{
			PropertyName: "APIKey",
			ErrorMessage: "API Key Incorrect",
		}
	case strings.Contains(msg, "api key required"):
		return &downloadclients.TestFailure{
			PropertyName: "APIKey",
			ErrorMessage: "API Key Required",
		}
	}
	return nil
}

// testGlobalConfig ports C# `TestGlobalConfig` at
// `Sabnzbd.cs:440-453`. Fires a whole-form failure when SAB's
// `misc.pre_check` global is on AND the server is older than 1.1
// — pre-check requires the per-job staging that landed in 1.1.
func testGlobalConfig(cfg *SabnzbdConfig, version *versionTriple) *downloadclients.TestFailure {
	if cfg == nil || version == nil {
		return nil
	}
	if !cfg.Misc.PreCheck {
		return nil
	}
	// HasVersion(1, 1) at C# :294-332 — Major>=1 + Minor>=1.
	if version.major > 1 || (version.major == 1 && version.minor >= 1) {
		return nil
	}
	return &downloadclients.TestFailure{
		PropertyName:        "",
		ErrorMessage:        "SABnzbd's 'Check before download' setting requires SABnzbd 1.1.0 or newer; disable it or upgrade SABnzbd",
		DetailedDescription: "Open SABnzbd > Config > Switches and disable 'Check before download'.",
	}
}

// testCategory ports C# `TestCategory` at `Sabnzbd.cs:455-489`.
// Two-arm check:
//   - Configured TvCategory exists on the server: fail when its
//     Dir ends in '*' (SAB's "Use Job Folders" convention — Sonarr
//     requires job folders for the rename-on-import flow).
//   - Configured TvCategory is missing AND non-empty: fail with
//     "category missing".
//
// Empty TvCategory passes (the validator skips the probe; Sonarr
// is free to grab into the SAB default category).
func testCategory(cfg *SabnzbdConfig, tvCategory string) *downloadclients.TestFailure {
	tv := strings.TrimSpace(tvCategory)
	if tv == "" {
		return nil
	}
	if cfg == nil {
		return nil
	}
	var match *SabnzbdCategory
	for i := range cfg.Categories {
		if cfg.Categories[i].Name == tv {
			match = &cfg.Categories[i]
			break
		}
	}
	if match != nil {
		if strings.HasSuffix(strings.TrimSpace(match.Dir), "*") {
			return &downloadclients.TestFailure{
				PropertyName:        "TvCategory",
				ErrorMessage:        fmt.Sprintf("Enable Job Folders for the %q category in SABnzbd", tv),
				DetailedDescription: "SABnzbd's per-category folder ends in '*' which disables per-job folders; Sonarr requires job folders for the import handoff.",
			}
		}
		return nil
	}
	return &downloadclients.TestFailure{
		PropertyName:        "TvCategory",
		ErrorMessage:        fmt.Sprintf("Category %q does not exist on the SABnzbd server", tv),
		DetailedDescription: fmt.Sprintf("Open SABnzbd > Config > Categories and add a category named %q, or update Sonarr to use one that exists.", tv),
	}
}

// versionTriple holds a parsed SAB version. Mirrors C# `Version` use
// in `Sabnzbd.ParseVersion` at `:334-366`.
type versionTriple struct {
	major, minor, patch int
}

// versionPattern matches SAB version strings like "3.7.2" or "3.7.x".
// Mirrors C# `VersionRegex` at `Sabnzbd.cs:38` exactly. The "x" suffix
// is the git-build sentinel.
var versionPattern = regexp.MustCompile(`(?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+|x)`)

// parseVersion ports C# `Sabnzbd.ParseVersion` at `:334-366`. Returns
// nil on the unknown-version path.
func parseVersion(version string) *versionTriple {
	if version == "" {
		return nil
	}
	m := versionPattern.FindStringSubmatch(version)
	if len(m) == 0 {
		// C# `:353-362`: "develop" → synthetic 3.0.0.
		if version == "develop" {
			return &versionTriple{major: 3}
		}
		return nil
	}
	majorIdx := versionPattern.SubexpIndex("major")
	minorIdx := versionPattern.SubexpIndex("minor")
	patchIdx := versionPattern.SubexpIndex("patch")
	major, _ := strconv.Atoi(m[majorIdx])
	minor, _ := strconv.Atoi(m[minorIdx])
	patchStr := m[patchIdx]
	if patchStr == "x" {
		patchStr = "0"
	}
	patch, _ := strconv.Atoi(patchStr)
	return &versionTriple{major: major, minor: minor, patch: patch}
}

// sanitizeFilename strips characters the OS file API + SAB's
// multipart parser don't tolerate from a release title. Mirrors C#
// `FileNameBuilder.CleanFileName` at
// `src/NzbDrone.Core/Organizer/FileNameBuilder.cs` (the small
// subset used by `UsenetClientBase.Download` at
// `UsenetClientBase.cs:43`).
//
// Replacement strategy: <>:"/\|?* → ' '. Square brackets and
// other valid-but-noisy chars are left as-is.
func sanitizeFilename(s string) string {
	bad := `<>:"/\|?*`
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || containsByte(bad, c) {
			out = append(out, ' ')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// RequestAction is the per-impl action map. Sabnzbd ships
// `getCategories` + `getCustomCategories` actions in C# — the Go
// port keeps the entry point but returns the C# default (nil, nil)
// until the per-action body ports. Mirrors `DownloadClientBase.cs:165-167`.
func (s *Sabnzbd) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: Sabnzbd satisfies the Provider interface.
var _ downloadclients.Provider = (*Sabnzbd)(nil)

// constructor is the registry factory function. Deserialises the
// row's Settings JSON into a [Settings] value, constructs a Proxy
// over deps.HTTPClient (cast to an [HTTPDoer]; production binds
// `*httpclient.Client`), and returns a fully-wired Sabnzbd.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("sabnzbd: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("sabnzbd: settings unmarshal: %w", err)
		}
	}
	doer, ok := deps.HTTPClient.(HTTPDoer)
	if !ok || doer == nil {
		return nil, errors.New("sabnzbd: deps.HTTPClient does not satisfy HTTPDoer")
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	proxy, err := NewProxy(doer, log)
	if err != nil {
		return nil, err
	}
	sab := New(def, settings, proxy, log)
	// Slice 6.B3: cast the typed-as-any RemotePathMappingService to
	// the narrow RemotePathMapper interface. A nil dep or a cast
	// miss leaves sab.rpm nil — the per-call sites fall through to
	// the raw SAB-side path. Test harnesses + single-host setups
	// run with nil and behave identically to the pre-6.B port.
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		sab.SetRemotePathMapper(rpm)
	}
	// Slice 5.J116: same pattern for the DiskProvider; nil dep / cast
	// miss leaves sab.disk nil → the RemoveItem deleteData arm
	// degrades to a warn log (SAB's del_files still cleans up what
	// SAB knows about).
	if disk, ok := deps.DiskProvider.(downloadclients.DiskProvider); ok {
		sab.SetDiskProvider(disk)
	}
	return sab, nil
}

// init registers the SAB Provider with the package-global
// [downloadclients.Default] registry. Mirrors the C# DI auto-
// registration pattern (every IDownloadClient implementation is
// auto-wired by the DryIoc container at startup).
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
