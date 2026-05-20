package nzbvortex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// NzbVortex is the [downloadclients.Provider] implementation for
// the C# `NzbVortex` class at
// `src/NzbDrone.Core/Download/Clients/NzbVortex/NzbVortex.cs:18-262`.
//
// Functionally complete after the 5.J66 → 5.J70 arc: Test runs
// the 4-arm probe chain (connection + API-version + auth +
// category); Download writes via multipart `nzb/add`; GetItems
// walks `nzb` + maps the 25-value state table + resolves multi-
// file output paths via `file/{id}`; RemoveItem cancels via
// `nzb/{id}/cancel` or `cancelDelete`. The auth-flow + session
// cache + retry-on-NotLoggedIn machinery in [Proxy] backs every
// authenticated call.
type NzbVortex struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy

	// rpm is the optional remote-path mapper consulted by GetItems
	// when resolving OutputPath from the NZBVortex-side DestinationPath.
	// Nil → DestinationPath flows through verbatim (preserves the
	// pre-Phase-6.B behaviour for unmapped setups).
	rpm downloadclients.RemotePathMapper
}

// New constructs an NzbVortex Provider. proxy=nil binds a default
// [Proxy] with a TLS-permissive HTTP client; log=nil binds
// [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *NzbVortex {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &NzbVortex{settings: settings, def: def, log: log, proxy: proxy}
}

// Name returns the user-facing label. C# `:49` — note the
// upper-cased "NZBVortex" form.
func (n *NzbVortex) Name() string { return "NZBVortex" }

// Protocol returns Usenet.
func (n *NzbVortex) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClient row.
func (n *NzbVortex) Definition() *downloadclients.DownloadClient { return n.def }

// Settings returns the bound Settings.
func (n *NzbVortex) Settings() Settings { return n.settings }

// Download ports `NzbVortex.AddFromNzbFile` at C# `:35-47`. Fires
// a multipart POST to `nzb/add` with the fetched NzbBody, the
// per-grab priority (Recent vs Older, selected by req.IsRecent),
// and the configured TvCategory as the groupname query param.
// Returns the add_uuid the server assigns. The C# code throws
// `DownloadClientException` on a null response; the Go port
// surfaces [ErrRPC] (already wrapped by [Proxy.DownloadNzb] when
// the server returns an empty add_uuid).
//
// Filename composition matches the C# pattern at AddFromNzbFile —
// `<ReleaseTitle>.nzb` (the orchestrator's pre-fetch step doesn't
// give the Provider a separate `filename` arg the way C#
// UsenetClientBase does, so the Go port derives from ReleaseTitle).
func (n *NzbVortex) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("nzbvortex: nil GrabRequest")
	}
	if n.proxy == nil {
		return "", errors.New("nzbvortex: nil Proxy")
	}
	if strings.TrimSpace(req.ReleaseTitle) == "" {
		return "", errors.New("nzbvortex: GrabRequest.ReleaseTitle is empty")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("nzbvortex: GrabRequest.NzbBody is empty")
	}
	priority := n.settings.OlderTvPriority
	if req.IsRecent {
		priority = n.settings.RecentTvPriority
	}
	filename := req.ReleaseTitle + ".nzb"
	id, err := n.proxy.DownloadNzb(ctx, n.settings, req.NzbBody, filename, priority)
	if err != nil {
		return "", fmt.Errorf("nzbvortex: DownloadNzb(%q): %w", req.ReleaseTitle, err)
	}
	return id, nil
}

// GetItems ports `NzbVortex.GetItems` at C# `:51-112`. Walks the
// queue via [Proxy.GetQueue] with doneLimit=30 (matches C# `:53`),
// then projects each [QueueItem] into a [downloadclients.Item]
// with the status table from C# `:71-94` + the IsEncrypted gate
// at `:98-101` + the RemainingTime=zero set on Completed at
// `:103-106`. OutputPath is resolved via [resolveOutputPath]
// which mirrors C# `GetOutputPath` at `:232-260` (the basename-
// match short-circuit + the GetFiles fall-through for renamed
// completed downloads).
//
// Status table (priority order — IsPaused short-circuits the
// state-switch first):
//
//   - IsPaused → Paused
//   - State==Waiting → Queued
//   - State==Done → Completed
//   - State in {UncompressFailed, CheckFailedDataCorrupt,
//     BadlyEncoded} → Failed
//   - default → Downloading
func (n *NzbVortex) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if n.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	queue, err := n.proxy.GetQueue(ctx, n.settings, 30)
	if err != nil {
		return nil, fmt.Errorf("nzbvortex: GetQueue: %w", err)
	}
	info := n.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(queue))
	for _, q := range queue {
		out = append(out, n.toItem(ctx, info, q))
	}
	return out, nil
}

// toItem projects one [QueueItem] into a [downloadclients.Item].
func (n *NzbVortex) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, q *QueueItem) *downloadclients.Item {
	id := q.AddUUID
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("%d", q.ID)
	}
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         id,
		Category:           q.GroupName,
		Title:              q.UiTitle,
		TotalSize:          q.TotalDownloadSize,
		RemainingSize:      q.TotalDownloadSize - q.DownloadedSize,
		CanBeRemoved:       true,
		CanMoveFiles:       true,
	}

	switch {
	case q.IsPaused:
		item.Status = downloadclients.DownloadItemStatusPaused
	case q.State == StateWaiting:
		item.Status = downloadclients.DownloadItemStatusQueued
	case q.State == StateDone:
		item.Status = downloadclients.DownloadItemStatusCompleted
	case q.State == StateUncompressFailed ||
		q.State == StateCheckFailedDataCorrupt ||
		q.State == StateBadlyEncoded:
		item.Status = downloadclients.DownloadItemStatusFailed
	default:
		item.Status = downloadclients.DownloadItemStatusDownloading
	}

	item.OutputPath = n.resolveOutputPath(ctx, q, item)

	if q.State == StatePasswordRequest {
		item.IsEncrypted = true
	}
	if item.Status == downloadclients.DownloadItemStatusCompleted {
		d := time.Duration(0)
		item.RemainingTime = &d
	}
	return item
}

// resolveOutputPath ports `NzbVortex.GetOutputPath` at C# `:232-260`.
//
//  1. Remap DestinationPath through the bound [RemotePathMapper].
//  2. If the basename matches UiTitle → return the remapped path.
//  3. If State != Done → return empty OsPath (the C# code returns
//     `new OsPath(null)` at `:244`; the Go port substitutes the
//     zero OsPath value).
//  4. Call Proxy.GetFiles(id). If >1 file → mark item Warning
//     with a multi-files message (matches C# `:249-256`). Then
//     return `outputPath + filesResponse[0].FileName` (matches C# `:259`).
//
// The function may also mutate `item` (Status + Message in the
// multi-files branch) — same side-effect as the C# version that
// passes the DownloadClientItem by-ref into GetOutputPath.
func (n *NzbVortex) resolveOutputPath(ctx context.Context, q *QueueItem, item *downloadclients.Item) ospath.OsPath {
	remote := ospath.New(q.DestinationPath)
	if n.rpm != nil && strings.TrimSpace(q.DestinationPath) != "" {
		if mapped, err := n.rpm.RemapRemoteToLocal(ctx, n.settings.Host, remote); err == nil {
			remote = mapped
		} else {
			n.log.Debug("nzbvortex: RemapRemoteToLocal failed", "destinationPath", q.DestinationPath, "err", err)
		}
	}
	if remote.FileName() == q.UiTitle {
		return remote
	}
	if q.State != StateDone {
		return ospath.OsPath{}
	}
	files, err := n.proxy.GetFiles(ctx, n.settings, q.ID)
	if err != nil {
		n.log.Debug("nzbvortex: GetFiles failed", "id", q.ID, "err", err)
		return remote
	}
	if len(files) == 0 {
		return remote
	}
	if len(files) > 1 {
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = fmt.Sprintf("Multiple files in download path %q — Sonarr can't pick a canonical filename", remote.FullPath())
		n.log.Debug("nzbvortex: multi-file completed download", "outputPath", remote.FullPath(), "count", len(files))
	}
	return ospath.New(filepath.Join(remote.FullPath(), files[0].FileName))
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (n *NzbVortex) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: n.Protocol(),
		Type:     Implementation,
		Name:     n.Name(),
	}
	if n.def != nil {
		info.ID = n.def.ID
		info.Name = n.def.Name
		info.RemoveCompletedDownloads = n.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional remote-path mapper. Same
// pattern as Hadouken / Transmission / qBittorrent — production
// wires this via the registry constructor's cast from
// [downloadclients.Dependencies.RemotePathMappingService].
func (n *NzbVortex) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	n.rpm = rpm
}

// GetImportItem is the C# base passthrough.
func (n *NzbVortex) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `NzbVortex.RemoveItem` at C# `:114-132`. Two
// resolution paths for the numeric Hadouken-side id:
//
//   - DownloadID parses as an int → use it directly.
//   - Otherwise (it's an AddUUID string) → fetch the queue and
//     look up the matching AddUUID. If no match → silently
//     succeed (matches C# `:127` — the queueItem != null guard).
//
// deleteData picks the endpoint: `cancelDelete` vs `cancel`. The C#
// version returns void; the Go port surfaces proxy errors verbatim.
func (n *NzbVortex) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if n.proxy == nil {
		return errors.New("nzbvortex: nil Proxy")
	}
	if item == nil {
		return errors.New("nzbvortex: nil item")
	}
	if strings.TrimSpace(item.DownloadID) == "" {
		return errors.New("nzbvortex: item has empty DownloadID")
	}

	if id, err := strconv.Atoi(item.DownloadID); err == nil {
		if removeErr := n.proxy.Remove(ctx, n.settings, id, deleteData); removeErr != nil {
			return fmt.Errorf("nzbvortex: Remove(%d): %w", id, removeErr)
		}
		return nil
	}

	// AddUUID path: scan the queue, find the matching item, use its
	// numeric Id.
	queue, err := n.proxy.GetQueue(ctx, n.settings, 30)
	if err != nil {
		return fmt.Errorf("nzbvortex: RemoveItem GetQueue: %w", err)
	}
	for _, q := range queue {
		if q != nil && q.AddUUID == item.DownloadID {
			if removeErr := n.proxy.Remove(ctx, n.settings, q.ID, deleteData); removeErr != nil {
				return fmt.Errorf("nzbvortex: Remove(%d): %w", q.ID, removeErr)
			}
			return nil
		}
	}
	// No match → C# `:127`'s `if (queueItem != null)` guard skips
	// the Remove call. Same here: success without action.
	n.log.Debug("nzbvortex: RemoveItem no queue match", "downloadID", item.DownloadID)
	return nil
}

// GetStatus ports `NzbVortex.GetStatus` at C# `Hadouken.cs:139-147`
// — wait, that's the wrong file, the C# is at
// `NzbVortex.cs:139-147`. IsLocalhost-only; NZBVortex's
// OutputRootFolders are populated at the per-item level inside
// GetItems (DestinationPath) rather than via a global config
// probe, so this method stays minimal.
func (n *NzbVortex) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: downloadclients.IsLocalhostHost(n.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default.
func (n *NzbVortex) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (n *NzbVortex) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `NzbVortex.Test(failures)` at C# `:149-155`. Runs the
// full four-arm C# probe chain — TestConnection + TestApiVersion +
// TestAuthentication + TestCategory. Each arm short-circuits the
// next on failure so a dead host doesn't quadruple-fail.
func (n *NzbVortex) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := n.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testConnection(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testAPIVersion(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testAuthentication(ctx)...)
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, n.testCategory(ctx)...)
	return failures, nil
}

// testAuthentication ports `TestAuthentication` at C# `:200-212`.
// Fires `GetQueue(limit=1)` — the call requires auth, so a bad
// ApiKey surfaces as [ErrAuth]. The ApiKey-property failure
// matches C# `:208`; any other transport failure surfaces as
// Host (delegated to the [Proxy.get] error chain — those will
// already have been caught by TestConnection so reaching this
// point implies auth-only failure).
func (n *NzbVortex) testAuthentication(ctx context.Context) []downloadclients.TestFailure {
	if _, err := n.proxy.GetQueue(ctx, n.settings, 1); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "ApiKey",
				ErrorMessage: "API Key is incorrect",
			}}
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to authenticate against NZBVortex: %v", err),
		}}
	}
	return nil
}

// testCategory ports `TestCategory` at C# `:214-230`. Calls
// `Proxy.GetGroups`, looks up by name, fails when the configured
// TvCategory is non-empty AND no matching group exists. A blank
// TvCategory disables the check (matches C# `:220`).
func (n *NzbVortex) testCategory(ctx context.Context) []downloadclients.TestFailure {
	want := strings.TrimSpace(n.settings.TvCategory)
	if want == "" {
		return nil
	}
	groups, err := n.proxy.GetGroups(ctx, n.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to read NZBVortex groups: %v", err),
		}}
	}
	for _, g := range groups {
		if g != nil && g.GroupName == want {
			return nil
		}
	}
	return []downloadclients.TestFailure{{
		PropertyName: "TvCategory",
		ErrorMessage: fmt.Sprintf("Group %q does not exist on the NZBVortex server", want),
	}}
}

// testConnection ports `TestConnection` at C# `:157-174`. Calls
// `app/appversion`. Failure surfaces as a Host-property failure.
func (n *NzbVortex) testConnection(ctx context.Context) []downloadclients.TestFailure {
	if _, err := n.proxy.GetVersion(ctx, n.settings); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to NZBVortex: %v", err),
		}}
	}
	return nil
}

// testAPIVersion ports `TestApiVersion` at C# `:176-198`. Calls
// `app/apilevel`, parses the dotted-numeric version, rejects
// anything below 2.3 (matches the C# `Major < 2 || (Major == 2
// && Minor < 3)` clause). Transport failure surfaces as a Host-
// property failure.
func (n *NzbVortex) testAPIVersion(ctx context.Context) []downloadclients.TestFailure {
	resp, err := n.proxy.GetAPIVersion(ctx, n.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to read NZBVortex API version: %v", err),
		}}
	}
	v := strings.TrimSpace(resp.APILevel)
	if v == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: "NZBVortex did not report an API level",
		}}
	}
	if compareVersion(v, fmt.Sprintf("%d.%d", minAPIMajor, minAPIMinor)) < 0 {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("NZBVortex API level %s is below the required %d.%d", v, minAPIMajor, minAPIMinor),
		}}
	}
	return nil
}

// compareVersion compares two dotted-numeric strings. Returns -1
// / 0 / +1. Non-numeric segments collapse to 0. Capped at 4
// components.
func compareVersion(a, b string) int {
	ai := splitVersion(a)
	bi := splitVersion(b)
	n := len(ai)
	if len(bi) > n {
		n = len(bi)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(ai) {
			av = ai[i]
		}
		if i < len(bi) {
			bv = bi[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(v string) []int {
	parts := strings.SplitN(v, ".", 4)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, r := range strings.TrimSpace(p) {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}

// Compile-time guard.
var _ downloadclients.Provider = (*NzbVortex)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("nzbvortex: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("nzbvortex: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	n := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		n.SetRemotePathMapper(rpm)
	}
	return n, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
