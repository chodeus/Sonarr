// Package mediacover is the Go port of `NzbDrone.Core.MediaCover` —
// `src/NzbDrone.Core/MediaCover/`. Closes queue [17] (slice 3.BO).
//
// Scope: the local-disk + URL-rewrite surface (Phase-3, slice 3.BO)
// PLUS the cover-download arm (J363) — DownloadCover / EnsureCovers /
// CoverAlreadyExists / the IHandleAsync<SeriesUpdatedEvent> trigger,
// now that the IHttpClient surface has ported (the J363
// [CoverDownloader] optional dep, bound to httpclient.Client in
// production). MediaCoverProxy (the seriesId==0 pre-add referrer
// workaround) stays a no-op.
//
// Phase-3 production wiring uses [Service.ConvertToLocalUrls] to
// rewrite series.Images JSON-blob entries' Url fields to local
// MediaCover URLs the Phase-5 API surface will serve, and
// [Service.HandleSeriesDeleted] to wipe the per-series cover
// folder when a series is deleted. The cover-fetch arm is
// re-introduced once Phase 4 ports the IHttpClient surface.
//
// ImageResizer (the SixLabors.ImageSharp wrapper at
// MediaCover/ImageResizer.cs:15-59) is also deferred — it's only
// called from the HTTP-gated EnsureCovers chain. Re-introducing it
// requires either a stdlib-only resize (poor quality) or a
// new golang.org/x/image dep; both decisions warrant an explicit
// review and are out of scope for this slice.
package mediacover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/series"
)

// CoverType ports `MediaCoverTypes` at `MediaCover/MediaCover.cs:5-14`.
// Ordinals frozen — these values are persisted as ints in the
// Series.Images JSON blob and the C#/Go-port wire formats must agree.
type CoverType int

const (
	// CoverTypeUnknown is the default for entries missing or
	// unrecognised by the metadata source. Skipped by the URL-rewriter
	// (`ConvertToLocalUrls`) and download chain.
	CoverTypeUnknown CoverType = 0
	// CoverTypePoster is the portrait-orientation series poster.
	// Resized to 500px and 250px height variants by the C# tree.
	CoverTypePoster CoverType = 1
	// CoverTypeBanner is the landscape series banner. Resized to
	// 70px and 35px height variants.
	CoverTypeBanner CoverType = 2
	// CoverTypeFanart is the high-res landscape backdrop. Resized
	// to 360px and 180px height variants.
	CoverTypeFanart CoverType = 3
	// CoverTypeScreenshot is a full-frame still. Same resize tier
	// as Fanart.
	CoverTypeScreenshot CoverType = 4
	// CoverTypeHeadshot is a portrait headshot for actor-row
	// metadata. Same resize tier as Poster.
	CoverTypeHeadshot CoverType = 5
	// CoverTypeClearlogo is the transparent-background show logo.
	// Persisted as `.png` (alpha preservation); other types use
	// `.jpg`.
	CoverTypeClearlogo CoverType = 6
)

// String returns the lowercased C# enum name C# uses for the on-
// disk filename + the URL path segment. Mirrors the
// `coverType.ToString().ToLower()` at C# `MediaCoverService.cs:70,94`.
func (t CoverType) String() string {
	switch t {
	case CoverTypePoster:
		return "poster"
	case CoverTypeBanner:
		return "banner"
	case CoverTypeFanart:
		return "fanart"
	case CoverTypeScreenshot:
		return "screenshot"
	case CoverTypeHeadshot:
		return "headshot"
	case CoverTypeClearlogo:
		return "clearlogo"
	default:
		return "unknown"
	}
}

// Cover ports `MediaCover` at `MediaCover/MediaCover.cs:16-31`. Lives
// inside Series.Images as embedded JSON.
//
// JSON field tags use the lowerCamelCase shape the C# default
// JsonSerializer emits (Url + RemoteUrl spelled with the
// case-preserved capitalisation the C# property names round-trip
// to). Documented divergence: the Go-port uses `coverType` as the
// JSON key (matches the camelCase convention) while the C# tree's
// JSON serialisation produces `coverType` too — drift is not
// expected.
type Cover struct {
	CoverType CoverType `json:"coverType"`
	Url       string    `json:"url"`
	RemoteUrl string    `json:"remoteUrl"`
}

// CoversUpdatedEvent ports `MediaCoversUpdatedEvent` at
// `MediaCover/MediaCoversUpdatedEvent.cs:1-17`. Published after
// the EnsureCovers chain so downstream consumers (Phase-5 SignalR
// push) can refresh the UI. The Updated flag is true when at least
// one cover file was actually downloaded this pass.
//
// The HTTP-gated EnsureCovers chain hasn't ported, so this event
// currently has no Phase-3 publishers. Defined here so the type
// surface is in place for the API + SignalR layers.
type CoversUpdatedEvent struct {
	Series  *series.Series
	Updated bool
}

// Filesystem narrows the disk read/write surface [Service] needs.
// Production wiring binds *disk.Provider via Go structural typing.
type Filesystem interface {
	FileExists(path string) bool
	FolderExists(path string) bool
	DeleteFolder(path string, recursive bool) error
	FileGetLastWrite(path string) (lastWriteTicks int64, err error)
	// GetFileSize returns the on-disk size in bytes. Used by the
	// J363 CoverAlreadyExists check (compares to the remote
	// Content-Length).
	GetFileSize(path string) (int64, error)
}

// CoverDownloader narrows the HTTP surface the J363 cover-download arm
// needs — a HEAD-style content-length probe + a file download. Mirrors
// the slice of `IHttpClient` the C# `MediaCoverService` +
// `CoverAlreadyExistsSpecification` reach for (`Head(...).Headers
// .ContentLength` + `DownloadFile`). Optional: when nil, [Service]
// skips the download arm entirely (the pre-J363 no-op posture).
// Production wiring binds an adapter over `*httpclient.Client`.
type CoverDownloader interface {
	// ContentLength issues a HEAD request and returns the remote
	// Content-Length. ok=false when the header is absent/unparseable
	// (the CoverAlreadyExists check then treats the cover as not
	// matching → re-download).
	ContentLength(ctx context.Context, url string) (length int64, ok bool, err error)
	// DownloadFile fetches url to path (via a temp .part + rename, per
	// the underlying client). Mirrors `IHttpClient.DownloadFile`.
	DownloadFile(ctx context.Context, url, path string) error
}

// AppFolders narrows the path-bundle surface to the one method
// [Service] reaches for. Mirrors the C# `IAppFolderInfo.GetMediaCoverPath()`
// extension method.
type AppFolders interface {
	// MediaCoverPath returns the absolute path of the per-instance
	// MediaCover folder. The C# tree resolves this via
	// `Path.Combine(GetAppDataPath(), "MediaCover")`.
	MediaCoverPath() string
}

// URLBaseReader narrows [configfile.Provider] to the UrlBase
// lookup. Phase-3 production wiring binds an adapter that reads
// the `UrlBase` config key.
type URLBaseReader interface {
	URLBase() string
}

// Service ports the local-disk + URL-rewrite portions of
// `MediaCoverService` at `MediaCover/MediaCoverService.cs:24-251`.
//
// Phase-3 surface:
//   - GetCoverPath / GetSeriesCoverPath — pure path computation;
//   - ConvertToLocalUrls — rewrites series.Images Url fields to
//     local /MediaCover/<seriesId>/<type>.ext URLs the Phase-5 API
//     serves. seriesId==0 (series not yet in Sonarr) takes the C#
//     proxy path which is HTTP-gated and stays a no-op here.
//   - HandleSeriesDeleted — wipes per-series cover folder.
//
// Phase-4 deferred (all gate on IHttpClient.DownloadFile +
// CoverAlreadyExistsSpecification's HEAD-request probe):
//   - EnsureCovers (download fan-out)
//   - HandleSeriesUpdated (which calls EnsureCovers)
//   - DownloadCover
//
// Goroutine-safe: stateless after construction.
type Service struct {
	fs       Filesystem
	folders  AppFolders
	urlBase  URLBaseReader
	log      *slog.Logger
	rootPath string
	// downloader is the optional J363 cover-download dep. nil → the
	// EnsureCovers download arm is skipped (pre-J363 posture). Set
	// post-construction via [Service.SetDownloader] so the existing
	// NewService signature (+ its callers) stay stable.
	downloader CoverDownloader
}

// SetDownloader binds the optional [CoverDownloader] that enables the
// J363 cover-download arm ([Service.EnsureCovers] /
// [Service.HandleSeriesUpdated]). Kept off the NewService signature so
// existing callers/tests are unaffected; production wiring calls this
// with an httpclient-backed adapter.
func (s *Service) SetDownloader(d CoverDownloader) {
	if s != nil {
		s.downloader = d
	}
}

// NewService binds the three narrow deps. nil-required-dep returns
// a typed error.
func NewService(fs Filesystem, folders AppFolders, urlBase URLBaseReader, log *slog.Logger) (*Service, error) {
	if fs == nil {
		return nil, errors.New("mediacover: NewService: fs is nil")
	}
	if folders == nil {
		return nil, errors.New("mediacover: NewService: folders is nil")
	}
	if urlBase == nil {
		return nil, errors.New("mediacover: NewService: urlBase is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		fs:       fs,
		folders:  folders,
		urlBase:  urlBase,
		log:      log,
		rootPath: folders.MediaCoverPath(),
	}, nil
}

// GetCoverPath ports `:66-71`. Returns the absolute path of a cover
// file on disk:
//
//	<MediaCoverPath>/<seriesId>/<type>[-<height>]<extension>
//
// height==0 omits the size suffix (the full-resolution master).
// height>0 produces the resize variant the EnsureResizedCovers
// chain writes (Phase-4 deferred — see [Service]).
func (s *Service) GetCoverPath(seriesID int, coverType CoverType, height int) string {
	if s == nil {
		return ""
	}
	suffix := ""
	if height > 0 {
		suffix = "-" + strconv.Itoa(height)
	}
	return filepath.Join(s.GetSeriesCoverPath(seriesID), coverType.String()+suffix+extensionFor(coverType))
}

// GetSeriesCoverPath ports `:105-108`. Returns the per-series cover
// folder path: <MediaCoverPath>/<seriesId>.
func (s *Service) GetSeriesCoverPath(seriesID int) string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.rootPath, strconv.Itoa(seriesID))
}

// ConvertToLocalUrls ports `:73-103`. Rewrites each cover's Url
// field IN PLACE so the Phase-5 API surface returns local URLs
// instead of the upstream skyhook/tvdb URLs.
//
// seriesID==0 is the C# "not yet in Sonarr" pre-add path — that
// uses [_mediaCoverProxy.RegisterUrl] which is HTTP-gated. The
// Go-port collapses the seriesID==0 arm to a no-op (no Url
// rewrite); when Phase 4 lands the proxy port the arm picks up
// without further changes here. Documented divergence.
//
// For seriesID>0:
//   - skips Cover entries whose CoverType==Unknown (matches C# `:87`);
//   - rewrites Url to `<URLBase>/MediaCover/<seriesId>/<type><ext>`;
//   - appends `?lastWrite=<ticks>` when the local file exists, so
//     the Phase-5 client cache busts on cover updates (matches
//     C# `:96-100`).
//
// The C# `lastWrite.Ticks` is the .NET tick count (100ns since
// year 0001). The Go-port re-uses [Filesystem.FileGetLastWrite]'s
// int64 return — production adapter computes ticks the same way.
func (s *Service) ConvertToLocalUrls(seriesID int, covers []Cover) {
	if s == nil || seriesID == 0 {
		// Phase-4 deferred: seriesID==0 takes the proxy-register
		// arm in C#; here we no-op so callers passing pre-add
		// covers see unchanged URLs (matches C# pre-3.BO fall-
		// through behaviour).
		return
	}
	urlBase := s.urlBase.URLBase()
	for i := range covers {
		if covers[i].CoverType == CoverTypeUnknown {
			continue
		}
		covers[i].Url = fmt.Sprintf("%s/MediaCover/%d/%s%s",
			urlBase,
			seriesID,
			covers[i].CoverType.String(),
			extensionFor(covers[i].CoverType),
		)
		filePath := s.GetCoverPath(seriesID, covers[i].CoverType, 0)
		if !s.fs.FileExists(filePath) {
			continue
		}
		ticks, err := s.fs.FileGetLastWrite(filePath)
		if err != nil {
			s.log.Debug("mediacover: lastWrite lookup failed", "path", filePath, "err", err)
			continue
		}
		covers[i].Url += "?lastWrite=" + strconv.FormatInt(ticks, 10)
	}
}

// HandleSeriesDeleted ports `:240-250`. Removes the per-series
// cover folder when the series is deleted. The C# version takes
// `SeriesDeletedEvent.Series` (a List<Series>); the Go-port
// HandleSeriesDeletedFor mirrors the bulk semantics by accepting
// a slice of series IDs.
//
// nil-receiver / empty-list / missing-folder all no-op.
func (s *Service) HandleSeriesDeleted(_ context.Context, seriesIDs []int) error {
	if s == nil {
		return nil
	}
	for _, id := range seriesIDs {
		path := s.GetSeriesCoverPath(id)
		if !s.fs.FolderExists(path) {
			continue
		}
		if err := s.fs.DeleteFolder(path, true); err != nil {
			s.log.Warn("mediacover: cleanup folder failed", "path", path, "err", err)
		}
	}
	return nil
}

// extensionFor ports `:221-231`. All cover types use `.jpg` except
// Clearlogo which uses `.png` (alpha preservation).
func extensionFor(t CoverType) string {
	if t == CoverTypeClearlogo {
		return ".png"
	}
	return ".jpg"
}

// PathExtensionFor exposes [extensionFor] for callers that need to
// compute a path without going through the Service (e.g. test
// helpers + the Phase-5 API surface that maps URL→path).
func PathExtensionFor(t CoverType) string {
	return extensionFor(t)
}

// ResizeHeights mirrors `EnsureResizedCovers.cs:178-198` — the
// per-cover-type resize tiers. Exposed publicly so the deferred
// Phase-4 EnsureCovers port + the Phase-5 API URL-builder can
// share a single source of truth instead of re-encoding the table.
//
// Returns nil for types without a resize tier (Unknown, Clearlogo).
func ResizeHeights(t CoverType) []int {
	switch t {
	case CoverTypePoster, CoverTypeHeadshot:
		return []int{500, 250}
	case CoverTypeBanner:
		return []int{70, 35}
	case CoverTypeFanart, CoverTypeScreenshot:
		return []int{360, 180}
	default:
		return nil
	}
}

// DecodeSeriesImages decodes the JSON blob stored on
// [series.Series.Images] into a typed Cover slice. Empty blob /
// pure-whitespace / "[]" all yield (nil, nil) — matches the C#
// behaviour where unparsed Images defaults to an empty list.
// Decode failures propagate; callers that want a swallow-and-continue
// semantic should pattern: `covers, _ := DecodeSeriesImages(s)`.
//
// Notifications providers that surface a per-series poster
// (Pushcut, Discord embed thumbnails) call this once per fan-out
// and then [FindCover] to locate the desired CoverType.
func DecodeSeriesImages(s *series.Series) ([]Cover, error) {
	if s == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(s.Images)
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil, nil
	}
	var out []Cover
	if err := json.Unmarshal([]byte(s.Images), &out); err != nil {
		return nil, fmt.Errorf("mediacover: decode series.Images: %w", err)
	}
	return out, nil
}

// FindCover returns the first Cover matching the given CoverType,
// or nil when no match exists. The C# tree's
// `series.Images.SingleOrDefault(c => c.CoverType == t)` is the
// same shape — a series carries at most one cover per type.
func FindCover(covers []Cover, t CoverType) *Cover {
	for i := range covers {
		if covers[i].CoverType == t {
			return &covers[i]
		}
	}
	return nil
}

// FindPosterURL returns the best per-series poster URL for
// notification payloads that want a single URL. Prefers
// CoverTypePoster's RemoteUrl (the upstream metadata source URL —
// publicly reachable from notification destinations) over Url
// (which routes through the Sonarr API and isn't accessible from
// external services like Pushbullet/Pushcut/Discord). Returns
// empty string when no poster is set.
//
// Callers that need the local-URL semantic (in-Sonarr UI consumers)
// should use FindCover + Url directly.
func FindPosterURL(covers []Cover) string {
	c := FindCover(covers, CoverTypePoster)
	if c == nil {
		return ""
	}
	if c.RemoteUrl != "" {
		return c.RemoteUrl
	}
	return c.Url
}

// ParseCoverType maps a lowercased name (`"poster"` etc.) back to
// the typed enum. Used by the Phase-5 API URL→type resolution
// when serving /MediaCover/<seriesId>/<type>.ext.
func ParseCoverType(name string) (CoverType, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "poster":
		return CoverTypePoster, true
	case "banner":
		return CoverTypeBanner, true
	case "fanart":
		return CoverTypeFanart, true
	case "screenshot":
		return CoverTypeScreenshot, true
	case "headshot":
		return CoverTypeHeadshot, true
	case "clearlogo":
		return CoverTypeClearlogo, true
	case "unknown":
		return CoverTypeUnknown, true
	}
	return CoverTypeUnknown, false
}
