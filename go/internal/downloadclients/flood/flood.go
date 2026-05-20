package flood

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
)

// Flood is the [downloadclients.Provider] implementation for the
// C# `Flood` class at
// `src/NzbDrone.Core/Download/Clients/Flood/Flood.cs:21-275`.
//
// Functionally complete after the 5.J71 → 5.J74 arc: Test runs
// AuthVerify; Download dispatches torrent-file vs magnet via the
// add-files/add-urls POST shape; GetItems walks the torrents map
// with the Tags + PostImportTags gates + 4-arm status table;
// RemoveItem fires the delete POST; MarkItemAsImported applies the
// PostImportTags PATCH; GetImportItem resolves the single-file vs
// shared-base OutputPath via the contents endpoint. GetStatus
// remains IsLocalhost-only — the C# `GetClientSettings` arm that
// populates OutputRootFolders from `client/settings.directoryDefault`
// is the only deferred piece (along with the seed-config cleanup
// gate in GetItems — both are post-import niceties, not load-
// bearing).
type Flood struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy

	// rpm is the optional remote-path mapper consulted by GetItems
	// to translate Flood-side `directory` into Sonarr-side paths.
	// Nil → pass-through.
	rpm downloadclients.RemotePathMapper

	// seedCfg is the optional seed-config lookup consulted by the
	// post-import cleanup-gate in [Flood.applyCleanupGate]. Nil
	// keeps the gate in its pre-J95 false-default state. Slice
	// 5.J95 wire-up.
	seedCfg downloadclients.SeedConfigProvider

	// now is the injectable clock the seed-time arm reads. Defaults
	// to time.Now; tests pin a fixed time to step over the
	// DateFinished + SeedTime predicate without time.Sleep.
	now func() time.Time
}

// New constructs a Flood Provider. proxy=nil binds a default
// [Proxy] wrapping [http.DefaultClient]; log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Flood {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &Flood{settings: settings, def: def, log: log, proxy: proxy, now: time.Now}
}

// Name returns the user-facing label. C# `Flood.cs:87`.
func (f *Flood) Name() string { return "Flood" }

// Protocol returns Torrent.
func (f *Flood) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (f *Flood) Definition() *downloadclients.DownloadClient { return f.def }

// Settings returns the bound Settings.
func (f *Flood) Settings() Settings { return f.settings }

// Download ports C# `Flood.cs:90-102` end-to-end. Dispatches:
//
//   - TorrentBody non-empty → [Proxy.AddTorrentByFile] with the
//     base64-encoded body + composed tags.
//   - MagnetURL non-empty → [Proxy.AddTorrentByUrl] with the magnet
//     URL + composed tags.
//
// Returns req.InfoHash uppercased (matches C# `:94,101` `return hash`
// — the orchestrator pre-computes the hash; C# uses lower-case-as-
// passed but Sonarr's other torrent clients consistently uppercase
// the queue join key — same convention applied here).
//
// Tag composition (J99 extends the J72 baseline): `Settings.Tags`
// applied verbatim, then per-grab AdditionalTags expanded from the
// GrabRequest.Subject when an AdditionalTags ID is configured. See
// [composeTags] for the per-ID arm table — TitleSlug / Quality /
// Languages / ReleaseGroup / Year / Indexer / Network ported from
// C# `Flood.cs:42-85`.
func (f *Flood) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("flood: nil GrabRequest")
	}
	if f.proxy == nil {
		return "", errors.New("flood: nil Proxy")
	}
	if strings.TrimSpace(req.InfoHash) == "" {
		return "", errors.New("flood: GrabRequest.InfoHash is empty (orchestrator must pre-compute)")
	}
	tags := composeTags(f.settings, req)

	switch {
	case len(req.TorrentBody) > 0:
		body := base64.StdEncoding.EncodeToString(req.TorrentBody)
		if err := f.proxy.AddTorrentByFile(ctx, f.settings, body, tags); err != nil {
			return "", fmt.Errorf("flood: AddTorrentByFile(%q): %w", req.ReleaseTitle, err)
		}
	case strings.TrimSpace(req.MagnetURL) != "":
		if err := f.proxy.AddTorrentByUrl(ctx, f.settings, req.MagnetURL, tags); err != nil {
			return "", fmt.Errorf("flood: AddTorrentByUrl(%q): %w", req.ReleaseTitle, err)
		}
	default:
		return "", errors.New("flood: GrabRequest has neither TorrentBody nor MagnetURL")
	}
	return strings.ToUpper(req.InfoHash), nil
}

// composeTags ports C# `HandleTags` at `Flood.cs:42-85`.
//
// Output is the union of:
//   - Settings.Tags applied verbatim (filtered for blank entries).
//   - Per-Subject AdditionalTags expansion: for each ID in
//     Settings.AdditionalTags, append the corresponding subject-
//     derived label.
//
// Deduplication preserves first-insertion order — the C# uses
// HashSet<string> which doesn't guarantee ordering, but Sonarr's
// queue UI is stable across runs so the deterministic-order Go port
// matches reasonable user expectation.
//
// Unknown AdditionalTags IDs are skipped (C# throws
// DownloadClientException at `:79`; the Go port logs to caller
// elsewhere and silently skips — a settings-validation slice should
// reject invalid IDs at save time, not blow up the download pipeline).
//
// req nil OR Subject not present OR Subject not *decisionengine.Subject
// → AdditionalTags expansion produces no extra tags (the Settings.Tags
// half still applies). Each AdditionalTag arm degrades gracefully on
// missing source fields (e.g. Series nil → TitleSlug/Year/Network
// arms produce nothing).
func composeTags(s Settings, req *downloadclients.GrabRequest) []string {
	seen := make(map[string]struct{}, len(s.Tags)+len(s.AdditionalTags))
	out := make([]string, 0, len(s.Tags)+len(s.AdditionalTags))
	add := func(tag string) {
		t := strings.TrimSpace(tag)
		if t == "" {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	for _, t := range s.Tags {
		add(t)
	}
	if len(s.AdditionalTags) == 0 {
		return out
	}
	subject, _ := req.Subject.(*decisionengine.Subject)
	if subject == nil {
		return out
	}
	rel := subjectReleaseInfo(subject)
	for _, id := range s.AdditionalTags {
		switch id {
		case AdditionalTagTitleSlug:
			if subject.Series != nil {
				add(subject.Series.TitleSlug)
			}
		case AdditionalTagQuality:
			if subject.ParsedEpisodeInfo != nil {
				add(subject.ParsedEpisodeInfo.Quality.Quality.Name)
			}
		case AdditionalTagLanguages:
			for _, l := range subject.Languages {
				add(l.Name)
			}
		case AdditionalTagReleaseGroup:
			if subject.ParsedEpisodeInfo != nil {
				add(subject.ParsedEpisodeInfo.ReleaseGroup)
			}
		case AdditionalTagYear:
			if subject.Series != nil && subject.Series.Year > 0 {
				add(strconv.Itoa(subject.Series.Year))
			}
		case AdditionalTagIndexer:
			if rel != nil {
				add(rel.Indexer)
			}
		case AdditionalTagNetwork:
			if subject.Series != nil {
				add(subject.Series.Network)
			}
		default:
			// Unknown AdditionalTag ID — skip rather than C#'s throw.
		}
	}
	return out
}

// subjectReleaseInfo narrows Subject.Release (any) to a
// [*parser.ReleaseInfo]. Mirrors the pattern in
// [historyhandlers.releaseInfoFromSubject] +
// [download.subjectReleaseInfo] — duplicated here per the Go-port
// convention of keeping the narrow type-assertion local to consumers
// so the indexer→DC import direction stays one-way.
func subjectReleaseInfo(s *decisionengine.Subject) *parser.ReleaseInfo {
	if s == nil {
		return nil
	}
	if r, ok := s.Release.(*parser.ReleaseInfo); ok && r != nil {
		return r
	}
	if t, ok := s.Release.(*parser.TorrentInfo); ok && t != nil {
		return &t.ReleaseInfo
	}
	return nil
}

// GetItems ports `Flood.GetItems` at C# `Flood.cs:104-188`. Walks
// the hash-keyed torrent map from [Proxy.GetTorrents], applies the
// Tags + PostImportTags gates, projects each entry into a
// [downloadclients.Item] via the 4-arm status table.
//
// Gates (priority order — first miss skips the row):
//
//   - Tags gate: skip when Settings.Tags has any tag NOT present
//     on the torrent (`!Tags.All(present)` at C# `:114`).
//   - PostImportTags skip: skip when EVERY PostImportTag IS present
//     on the torrent (the torrent has already been imported and
//     marked — C# `:119-122`).
//
// Status table (priority order — first match wins, C# `:144-159`):
//
//   - Status contains "seeding" OR "complete" → Completed
//   - Status contains "stopped" → Paused
//   - Status contains "error" → Warning
//   - Status contains "downloading" → Downloading
//   - default → status field left at zero (the C# code does the same
//     — no fall-through arm).
//
// Cleanup-gate (C# `:161-182`) wired at slice 5.J95 via
// [downloadclients.SeedConfigProvider]. See [Flood.applyCleanupGate].
// Nil seedCfg binding (composition root hasn't injected) leaves
// CanMoveFiles + CanBeRemoved at false — preserves the pre-J95
// posture for partial-wiring environments.
func (f *Flood) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if f.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	torrents, err := f.proxy.GetTorrents(ctx, f.settings)
	if err != nil {
		return nil, fmt.Errorf("flood: GetTorrents: %w", err)
	}
	hasPostImport := len(f.settings.PostImportTags) > 0
	info := f.itemClientInfo(hasPostImport)
	out := make([]*downloadclients.Item, 0, len(torrents))
	for hash, t := range torrents {
		if !containsAll(t.Tags, f.settings.Tags) {
			continue
		}
		if hasPostImport && containsAll(t.Tags, f.settings.PostImportTags) {
			continue
		}
		out = append(out, f.toItem(ctx, info, hash, t))
	}
	return out, nil
}

// toItem projects one [Torrent] into a [downloadclients.Item].
func (f *Flood) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, hash string, t *Torrent) *downloadclients.Item {
	dir := ospath.New(t.Directory)
	if f.rpm != nil && strings.TrimSpace(t.Directory) != "" {
		if mapped, err := f.rpm.RemapRemoteToLocal(ctx, f.settings.Host, dir); err == nil {
			dir = mapped
		} else {
			f.log.Debug("flood: RemapRemoteToLocal failed", "directory", t.Directory, "err", err)
		}
	}

	var category string
	if len(t.Tags) > 0 {
		category = t.Tags[0]
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         hash,
		Title:              t.Name,
		OutputPath:         dir,
		Category:           category,
		RemainingSize:      t.SizeBytes - t.BytesDone,
		TotalSize:          t.SizeBytes,
		Message:            t.Message,
	}
	ratio := t.Ratio
	item.SeedRatio = &ratio
	if t.Eta > 0 {
		d := time.Duration(t.Eta) * time.Second
		item.RemainingTime = &d
	}
	switch {
	case statusContains(t.Status, "seeding") || statusContains(t.Status, "complete"):
		item.Status = downloadclients.DownloadItemStatusCompleted
	case statusContains(t.Status, "stopped"):
		item.Status = downloadclients.DownloadItemStatusPaused
	case statusContains(t.Status, "error"):
		item.Status = downloadclients.DownloadItemStatusWarning
	case statusContains(t.Status, "downloading"):
		item.Status = downloadclients.DownloadItemStatusDownloading
	}
	f.applyCleanupGate(ctx, info, hash, t, item)
	return item
}

// applyCleanupGate ports C# `Flood.cs:161-182`.
//
// Differences vs RQBit's J94 gate (kept in mind so callers don't
// assume the gates are identical):
//   - Gates on item.Status == Completed (set by the "seeding" /
//     "complete" status arm at C# `:144-146`), NOT a separate
//     IsFinished bool. The C# `:161` checks
//     `RemoveCompletedDownloads && item.Status == DownloadItemStatus.Completed`.
//   - Ratio comparison uses item.SeedRatio directly — Flood does
//     NOT divide by 1000.0 like RQBit's C# does. The wire-shape
//     Ratio is a real number (e.g. 1.5).
//   - SeedTime arm requires DateFinished > 0 — Flood explicitly
//     skips the time-arm when the wire shape doesn't surface a
//     finished-at timestamp. Matches C# `:173` `properties.DateFinished is > 0`.
//
// Algorithm tracks C# verbatim:
//  1. `RemoveCompletedDownloads && Status==Completed && seedCfg!=nil`
//     AND-chain at C# `:161`. Any fail-short circuits the gate.
//  2. `seedConfig = GetSeedConfiguration(item.DownloadId)` at C# `:164`.
//     nil → no override → fail gate. Lookup errors logged at Debug
//     and treated as nil (transient DB errors mustn't aggressively
//     remove torrents).
//  3. Ratio arm: `item.SeedRatio >= seedConfig.Ratio` at C# `:168`.
//     nil-Ratio in seedConfig → arm doesn't fire (nullable-comparison
//     semantics).
//  4. SeedTime arm (only if ratio arm didn't fire): C# `:173-180`.
//     `DateFinished > 0 AND now - epoch(DateFinished) >= SeedTime`.
//     nil-SeedTime in seedConfig → arm doesn't fire.
//  5. Either arm sets CanMoveFiles = CanBeRemoved = true (C#
//     `:171,178` couples them).
func (f *Flood) applyCleanupGate(ctx context.Context, info *downloadclients.ItemClientInfo, hash string, t *Torrent, item *downloadclients.Item) {
	if info == nil || !info.RemoveCompletedDownloads || item.Status != downloadclients.DownloadItemStatusCompleted || f.seedCfg == nil {
		return
	}
	seedConfig, err := f.seedCfg.GetSeedConfiguration(ctx, hash)
	if err != nil {
		f.log.Debug("flood: GetSeedConfiguration failed; cleanup gate denied", "hash", hash, "err", err)
		return
	}
	if seedConfig == nil {
		return
	}

	if seedConfig.Ratio != nil && item.SeedRatio != nil && *item.SeedRatio >= *seedConfig.Ratio {
		f.log.Debug("flood: torrent met seed ratio goal", "hash", hash, "goal", *seedConfig.Ratio)
		item.CanMoveFiles = true
		item.CanBeRemoved = true
		return
	}
	if seedConfig.SeedTime != nil && t.DateFinished != nil && *t.DateFinished > 0 {
		finishedAt := time.Unix(*t.DateFinished, 0)
		if f.now().Sub(finishedAt) >= *seedConfig.SeedTime {
			f.log.Debug("flood: torrent met seed time goal", "hash", hash, "goal", seedConfig.SeedTime.String())
			item.CanMoveFiles = true
			item.CanBeRemoved = true
			return
		}
	}
	f.log.Debug("flood: torrent seeding goals not yet reached", "hash", hash)
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, hasPostImportCategory)`
// at `Flood.cs:126`. hasPostImportCategory is true when
// Settings.PostImportTags is non-empty.
func (f *Flood) itemClientInfo(hasPostImportCategory bool) *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol:              f.Protocol(),
		Type:                  Implementation,
		Name:                  f.Name(),
		HasPostImportCategory: hasPostImportCategory,
	}
	if f.def != nil {
		info.ID = f.def.ID
		info.Name = f.def.Name
		info.RemoveCompletedDownloads = f.def.RemoveCompletedDownloads
	}
	return info
}

// SetRemotePathMapper binds the optional remote-path mapper used
// by GetItems for the `directory` translation. Same Phase 6.B
// pattern as Transmission/qBittorrent/Hadouken/NzbVortex.
func (f *Flood) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	f.rpm = rpm
}

// SetSeedConfigProvider binds the optional
// [downloadclients.SeedConfigProvider] consumed by the cleanup-gate
// in [Flood.applyCleanupGate]. Slice 5.J95 wire-up.
func (f *Flood) SetSeedConfigProvider(s downloadclients.SeedConfigProvider) {
	f.seedCfg = s
}

// containsAll returns true when every entry in `wanted` is present
// in `set`. Empty `wanted` returns true (matches C# `Tags.All`'s
// behaviour on the empty enumerable). nil-safe on both sides.
func containsAll(set, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	have := make(map[string]bool, len(set))
	for _, s := range set {
		have[s] = true
	}
	for _, w := range wanted {
		if !have[w] {
			return false
		}
	}
	return true
}

// statusContains reports whether the status slice has any entry
// equal to needle (case-sensitive, matching C# `Status.Contains`
// which is a List<string>.Contains call).
func statusContains(status []string, needle string) bool {
	for _, s := range status {
		if s == needle {
			return true
		}
	}
	return false
}

// GetImportItem ports C# `Flood.GetImportItem` at `Flood.cs:190-222`.
// Calls Proxy.GetTorrentContentPaths and refines the cloned item's
// OutputPath:
//
//   - 0 contents → ErrUnavailable-wrapped error (matches C# `:198`'s
//     `DownloadClientUnavailableException`).
//   - 1 content → OutputPath = item.OutputPath + content[0].
//   - >1 contents that share a common first-segment base → OutputPath
//     = item.OutputPath + base. If first segments diverge, leave
//     OutputPath at the item's base directory (matches C# `:212-218`).
//
// First-segment extraction splits on both forward and back slashes
// (the C# code at `:210` uses `Split(new char[]{'\\','/'}, RemoveEmptyEntries)`).
func (f *Flood) GetImportItem(ctx context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	if item == nil {
		return nil, errors.New("flood: GetImportItem nil item")
	}
	if f.proxy == nil {
		return nil, errors.New("flood: nil Proxy")
	}
	result := item.Clone()
	paths, err := f.proxy.GetTorrentContentPaths(ctx, f.settings, item.DownloadID)
	if err != nil {
		return nil, fmt.Errorf("flood: GetImportItem(%s): %w", item.DownloadID, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: torrent %s reported zero content paths", ErrUnavailable, item.DownloadID)
	}
	if len(paths) == 1 {
		result.OutputPath = ospath.New(joinPath(item.OutputPath.FullPath(), paths[0]))
		return result, nil
	}
	first := firstSegment(paths[0])
	allSame := true
	for _, p := range paths[1:] {
		if firstSegment(p) != first {
			allSame = false
			break
		}
	}
	if allSame {
		result.OutputPath = ospath.New(joinPath(item.OutputPath.FullPath(), first))
	}
	return result, nil
}

// firstSegment returns the leading path component of p, splitting
// on either separator. Mirrors C# `:210`'s mixed-slash split.
func firstSegment(p string) string {
	p = strings.TrimLeft(p, "/\\")
	for i := 0; i < len(p); i++ {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return p
}

// joinPath wraps [filepath.Join] without the package needing to
// import path/filepath at the file head just for this one helper.
// The C# `OsPath + OsPath` operator concatenates with a separator;
// the Go port uses filepath.Join which collapses double-separators
// the same way.
func joinPath(base, suffix string) string {
	if base == "" {
		return suffix
	}
	if suffix == "" {
		return base
	}
	// Use forward slash join — Flood is POSIX-leaning + matches the
	// existing ospath helper conventions in nzbvortex/hadouken.
	b := strings.TrimRight(base, "/\\")
	s := strings.TrimLeft(suffix, "/\\")
	return b + "/" + s
}

// RemoveItem ports `Flood.RemoveItem` at C# `:239-242`. Forwards
// the DownloadID + deleteData straight through to
// [Proxy.DeleteTorrent].
func (f *Flood) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if f.proxy == nil {
		return errors.New("flood: nil Proxy")
	}
	if item == nil {
		return errors.New("flood: nil item")
	}
	if strings.TrimSpace(item.DownloadID) == "" {
		return errors.New("flood: item has empty DownloadID")
	}
	if err := f.proxy.DeleteTorrent(ctx, f.settings, item.DownloadID, deleteData); err != nil {
		return fmt.Errorf("flood: DeleteTorrent(%s): %w", item.DownloadID, err)
	}
	return nil
}

// MarkItemAsImported ports C# `Flood.MarkItemAsImported` at
// `:224-237`. When Settings.PostImportTags is non-empty:
//
//  1. Fetch the current torrent list.
//  2. If the DownloadID matches a hash in the list, PATCH the tags
//     with (existing-tags ∪ PostImportTags).
//  3. Otherwise no-op (matches the C# `if list.ContainsKey(...)`
//     guard which silently skips).
//
// When PostImportTags is empty, returns nil immediately (the C#
// `:226`'s `.Any()` guard short-circuits the whole branch).
//
// Errors from GetTorrents / SetTorrentsTags wrap the proxy sentinels.
func (f *Flood) MarkItemAsImported(ctx context.Context, item *downloadclients.Item) error {
	if f.proxy == nil {
		return errors.New("flood: nil Proxy")
	}
	if item == nil {
		return errors.New("flood: nil item")
	}
	if len(f.settings.PostImportTags) == 0 {
		return nil
	}
	if strings.TrimSpace(item.DownloadID) == "" {
		return errors.New("flood: item has empty DownloadID")
	}
	torrents, err := f.proxy.GetTorrents(ctx, f.settings)
	if err != nil {
		return fmt.Errorf("flood: MarkItemAsImported GetTorrents: %w", err)
	}
	existing, ok := torrents[item.DownloadID]
	if !ok {
		f.log.Debug("flood: MarkItemAsImported no matching torrent", "downloadID", item.DownloadID)
		return nil
	}
	merged := mergeTags(existing.Tags, f.settings.PostImportTags)
	if err := f.proxy.SetTorrentsTags(ctx, f.settings, item.DownloadID, merged); err != nil {
		return fmt.Errorf("flood: SetTorrentsTags(%s): %w", item.DownloadID, err)
	}
	return nil
}

// mergeTags returns the union of two string slices, preserving the
// `existing` order and appending any new entries from `additional`.
// Mirrors C# `existing.Concat(additional).ToImmutableHashSet()` at
// `Flood.cs:233-234` — but the Go port preserves order so the wire
// payload is reproducible across test runs.
func mergeTags(existing, additional []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+len(additional))
	for _, t := range existing {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range additional {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// GetStatus ports the IsLocalhost arm of C# `Flood.cs:244-258`.
// The OutputRootFolders arm (which reads `client/settings.directoryDefault`
// + applies RemotePathMapper) lands with the GetClientSettings
// follow-up. For now IsLocalhost is enough for the healthcheck path.
func (f *Flood) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(f.settings.Host)}, nil
}

// RequestAction is the C# base no-op.
func (f *Flood) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `Flood.Test(failures)` at C# `:260-274`. Runs
// Settings.Validate first, then drives the auth-verify probe.
// Auth failure surfaces as a Password-property failure (matches
// C# `:266-269`); transport failure surfaces as a Host-property
// failure (matches C# `:270-273`).
func (f *Flood) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := f.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	if err := f.proxy.AuthVerify(ctx, f.settings); err != nil {
		if errors.Is(err, ErrAuth) {
			return []downloadclients.TestFailure{{
				PropertyName: "Password",
				ErrorMessage: "Failed to authenticate with Flood",
			}}, nil
		}
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Flood: %v", err),
		}}, nil
	}
	return nil, nil
}

// isLocalHost mirrors C# `Settings.Host == "127.0.0.1" || ==
// "::1" || == "localhost"` at `Flood.cs:255`.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// Compile-time guard.
var _ downloadclients.Provider = (*Flood)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("flood: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("flood: settings unmarshal: %w", err)
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
	if seedCfg, ok := deps.SeedConfigProvider.(downloadclients.SeedConfigProvider); ok {
		f.SetSeedConfigProvider(seedCfg)
	}
	return f, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
